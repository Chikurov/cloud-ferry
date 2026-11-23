#!/usr/bin/env python3
"""Capture README screenshots from preview.html (demo data only).

Usage:
  python3 scripts/screenshots.py
  python3 scripts/screenshots.py --out docs/readme

Requires: playwright, pillow. Uses system Chromium when present so the
script does not vendor browser binaries into the repo.
"""

from __future__ import annotations

import argparse
import http.server
import socketserver
import sys
import threading
import time
from pathlib import Path

from PIL import Image
from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_OUT = ROOT / "docs" / "readme"
CHROMIUM = Path("/Applications/Chromium.app/Contents/MacOS/Chromium")
VIEWPORT = {"width": 1100, "height": 800}
DPR = 2
MAX_BYTES = 500 * 1024


class QuietHandler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, directory=None, **kwargs):
        super().__init__(*args, directory=str(directory or ROOT), **kwargs)

    def log_message(self, format, *args):  # noqa: A003
        return


def serve(root: Path) -> tuple[socketserver.TCPServer, str]:
    handler = lambda *a, **k: QuietHandler(*a, directory=root, **k)  # noqa: E731
    httpd = socketserver.ThreadingTCPServer(("127.0.0.1", 0), handler)
    httpd.allow_reuse_address = True
    thread = threading.Thread(target=httpd.serve_forever, daemon=True)
    thread.start()
    host, port = httpd.server_address
    return httpd, f"http://{host}:{port}"


def compress_png(path: Path, max_bytes: int = MAX_BYTES) -> None:
    """Re-encode PNG with zlib optimize; fall back to light palette if needed."""
    img = Image.open(path).convert("RGBA")
    best = path.read_bytes()
    for level in (9, 6):
        buf_path = path.with_suffix(f".tmp{level}.png")
        img.save(buf_path, format="PNG", optimize=True, compress_level=level)
        data = buf_path.read_bytes()
        buf_path.unlink(missing_ok=True)
        if len(data) < len(best):
            best = data
    if len(best) > max_bytes:
        # Palette fallback keeps text readable for UI chrome; avoid dithering.
        quantized = img.convert("RGB").quantize(colors=256, method=Image.MEDIANCUT, dither=Image.NONE)
        buf_path = path.with_suffix(".tmpq.png")
        quantized.save(buf_path, format="PNG", optimize=True, compress_level=9)
        data = buf_path.read_bytes()
        buf_path.unlink(missing_ok=True)
        if len(data) < len(best):
            best = data
    path.write_bytes(best)


def wait_ready(page) -> None:
    page.wait_for_function("() => document.fonts && document.fonts.status === 'loaded'")
    page.wait_for_timeout(200)
    page.evaluate(
        """() => {
          document.activeElement && document.activeElement.blur && document.activeElement.blur();
          const toasts = document.querySelectorAll('#toasts, #toasts-flow, .toast');
          toasts.forEach((node) => node.remove());
        }"""
    )


def layout_report(page) -> dict:
    return page.evaluate(
        """() => {
          const html = document.documentElement;
          const body = document.body;
          return {
            innerWidth: window.innerWidth,
            innerHeight: window.innerHeight,
            scrollWidth: Math.max(html.scrollWidth, body.scrollWidth),
            scrollHeight: Math.max(html.scrollHeight, body.scrollHeight),
            overflowX: Math.max(html.scrollWidth, body.scrollWidth) > window.innerWidth + 1,
          };
        }"""
    )


def capture(page, path: Path, clip=None) -> dict:
    wait_ready(page)
    report = layout_report(page)
    kwargs = {"path": str(path), "type": "png", "animations": "disabled"}
    if clip:
        kwargs["clip"] = clip
    else:
        kwargs["full_page"] = False
    page.screenshot(**kwargs)
    compress_png(path)
    report["bytes"] = path.stat().st_size
    report["path"] = str(path)
    return report


def open_preview(page, base: str, shot: str, theme: str = "light", hash_path: str = "#/") -> None:
    url = f"{base}/preview.html?shot={shot}&freeze=1&theme={theme}{hash_path}"
    page.goto(url, wait_until="networkidle")
    page.wait_for_timeout(400)
    page.evaluate(
        """(theme) => {
          if (typeof applyTheme === 'function') applyTheme(theme, true);
          document.documentElement.setAttribute('data-theme', theme);
          document.documentElement.setAttribute('data-shot', new URLSearchParams(location.search).get('shot') || 'freeze');
        }""",
        theme,
    )
    # Let the app settle after theme + demo fetch.
    page.wait_for_function(
        """() => {
          const boot = document.querySelector('#boot-view');
          return !boot || boot.hidden;
        }"""
    )
    page.wait_for_timeout(500)


def shot_home(page, base: str, out: Path, theme: str, name: str) -> dict:
    open_preview(page, base, "home", theme)
    page.wait_for_selector("#app-view:not([hidden])")
    page.wait_for_selector("#job-list .job-row")
    page.wait_for_function(
        """() => {
          const login = document.querySelector('#account-cell-login');
          const stats = document.querySelector('#job-list .task-stats');
          return login && login.textContent.includes('alex.demo')
            && stats && /ГБ|МБ|KB|B/.test(stats.textContent)
            && /\\/с|≈/.test(stats.textContent);
        }"""
    )
    return capture(page, out / name)


def shot_downloads(page, base: str, out: Path, theme: str, name: str) -> dict:
    open_preview(page, base, "downloads", theme)
    page.wait_for_selector("#job-list .job-row")
    page.wait_for_function("() => document.querySelectorAll('#job-list .job-row').length >= 4")
    return capture(page, out / name)


def shot_connect(page, base: str, out: Path, theme: str, name: str) -> dict:
    open_preview(page, base, "connect", theme, hash_path="#/setup")
    page.wait_for_selector("#setup-view:not([hidden])")
    # Prefer password method so empty fields are visible (no secrets).
    page.click("#method-webdav")
    page.wait_for_selector("#f-user")
    page.fill("#f-user", "")
    page.fill("#f-pass", "")
    page.evaluate("() => { document.activeElement && document.activeElement.blur(); }")
    page.wait_for_function(
        """() => {
          const methods = Array.from(document.querySelectorAll('.method-option')).map((el) => el.textContent.trim());
          const user = document.querySelector('#f-user');
          const pwd = document.querySelector('#f-pass');
          return methods.includes('Яндекс ID') && methods.includes('Пароль приложения') && methods.includes('Токен')
            && user && !user.value && pwd && !pwd.value;
        }"""
    )
    return capture(page, out / name)


def shot_select_files(page, base: str, out: Path, theme: str, name: str) -> dict:
    open_preview(page, base, "select", theme)
    page.wait_for_selector("#flow-start")
    page.click("#flow-start")
    page.wait_for_selector("#folder-dialog[open]")
    page.wait_for_selector("#flow-source-tree .folder-check, #flow-source-tree .folder-row")
    page.wait_for_timeout(600)
    # Check the three demo folders if present.
    for folder_name in ("Фотографии", "Документы", "Видео"):
        checkbox = page.locator(
            f'#flow-source-tree .folder-check[data-path="{folder_name}"], '
            f'#flow-source-tree input[data-path="{folder_name}"]'
        )
        if checkbox.count() == 0:
            # Fallback: click the row label text.
            row = page.locator("#flow-source-tree").get_by_text(folder_name, exact=True).first
            row.click()
        else:
            if not checkbox.first.is_checked():
                checkbox.first.check(force=True)
    page.wait_for_function(
        """() => {
          const disk = document.body.innerText.includes('Весь диск');
          const existing = document.body.innerText.includes('Показать уже скачанные');
          const checks = Array.from(document.querySelectorAll('#flow-source-tree .folder-check, #flow-source-tree input[type=checkbox]'))
            .filter((el) => el.checked).length;
          return disk && existing && checks >= 3;
        }"""
    )
    page.evaluate("() => { document.activeElement && document.activeElement.blur(); }")
    return capture(page, out / name)


def shot_destination(page, base: str, out: Path, theme: str, name: str) -> dict:
    open_preview(page, base, "destination", theme)
    page.wait_for_selector("#flow-start")
    page.click("#flow-start")
    page.wait_for_selector("#folder-dialog[open]")
    # New download opens on source; open Umbrel folder pick at /downloads
    # so the folder list (not the empty «Новые» leaf) is visible.
    page.evaluate(
        """async () => {
          if (typeof flowDest !== 'undefined') flowDest = '/downloads';
          if (typeof showFlowStep === 'function') showFlowStep('flow-dest');
          if (typeof loadFolder === 'function') await loadFolder('/downloads');
          else if (typeof renderFlow === 'function') renderFlow();
        }"""
    )
    page.wait_for_selector("#flow-dest-body")
    page.wait_for_function(
        """() => {
          const path = document.querySelector('#folder-path');
          const create = document.querySelector('#folder-create');
          const primary = document.querySelector('#folder-choose');
          const body = document.querySelector('#flow-dest-body');
          const text = (body && body.innerText) || '';
          return path && path.textContent.includes('/downloads')
            && create && /Создать папку/.test(create.textContent)
            && primary && !primary.disabled
            && body && (body.querySelectorAll('.folder-row, [data-path], button, a').length >= 2
              || /Фото|Видео|Документы|Музыка|Новые/.test(text));
        }"""
    )
    page.evaluate("() => { document.activeElement && document.activeElement.blur(); }")
    return capture(page, out / name)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT)
    parser.add_argument("--chromium", type=Path, default=CHROMIUM)
    args = parser.parse_args()
    out: Path = args.out
    out.mkdir(parents=True, exist_ok=True)

    httpd, base = serve(ROOT)
    reports = {}
    issues = []
    try:
        with sync_playwright() as p:
            launch_kwargs = {
                "headless": True,
                "args": [
                    "--hide-scrollbars",
                    "--disable-lcd-text",
                    "--font-render-hinting=none",
                ],
            }
            if args.chromium.exists():
                launch_kwargs["executable_path"] = str(args.chromium)
            browser = p.chromium.launch(**launch_kwargs)
            # color_scheme follows each shot via ?theme=; start light.
            context = browser.new_context(
                viewport=VIEWPORT,
                device_scale_factor=DPR,
                locale="ru-RU",
                color_scheme="light",
            )
            page = context.new_page()
            page.add_style_tag(
                content="*,*::before,*::after{caret-color:transparent!important} html{cursor:none!important}"
            )

            reports["home-light.png"] = shot_home(page, base, out, "light", "home-light.png")
            reports["home-dark.png"] = shot_home(page, base, out, "dark", "home-dark.png")
            reports["downloads-states.png"] = shot_downloads(page, base, out, "light", "downloads-states.png")
            reports["downloads-states-dark.png"] = shot_downloads(page, base, out, "dark", "downloads-states-dark.png")
            reports["connect.png"] = shot_connect(page, base, out, "light", "connect.png")
            reports["connect-dark.png"] = shot_connect(page, base, out, "dark", "connect-dark.png")
            reports["select-files.png"] = shot_select_files(page, base, out, "light", "select-files.png")
            reports["select-files-dark.png"] = shot_select_files(page, base, out, "dark", "select-files-dark.png")
            reports["destination.png"] = shot_destination(page, base, out, "light", "destination.png")
            reports["destination-dark.png"] = shot_destination(page, base, out, "dark", "destination-dark.png")

            browser.close()
    finally:
        httpd.shutdown()
        httpd.server_close()

    for name, info in reports.items():
        size = info.get("bytes", 0)
        flag = "OK" if size <= MAX_BYTES else "LARGE"
        print(f"{flag} {name}: {size} bytes; overflowX={info.get('overflowX')}")
        if info.get("overflowX"):
            issues.append(f"{name}: horizontal overflow")
        if size > MAX_BYTES:
            issues.append(f"{name}: {size} bytes > {MAX_BYTES}")

    if issues:
        print("ISSUES:", file=sys.stderr)
        for item in issues:
            print(" -", item, file=sys.stderr)
        return 1
    print("All screenshots written to", out)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
