#!/usr/bin/env python3
"""Capture README screenshots from preview.html (demo data only).

Dependencies: playwright, pillow. Prefer a venv outside the repo.
Uses system Chromium when present so browsers are not vendored.

Usage:
  python3 scripts/build_preview.py
  python3 scripts/screenshots.py
  python3 scripts/screenshots.py --out docs/readme
"""

from __future__ import annotations

import argparse
import http.server
import socketserver
import sys
import threading
from pathlib import Path

from PIL import Image
from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_OUT = ROOT / "docs" / "readme"
CHROMIUM = Path("/Applications/Chromium.app/Contents/MacOS/Chromium")
VIEWPORT = {"width": 1100, "height": 800}
DPR = 2
MAX_BYTES = 500 * 1024
# Compact home frame: shell head + drive row + one job card, with padding.
HOME_CLIP = {"x": 180, "y": 0, "width": 740, "height": 348}


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
        quantized = img.convert("RGB").quantize(colors=256, method=Image.MEDIANCUT, dither=Image.NONE)
        buf_path = path.with_suffix(".tmpq.png")
        quantized.save(buf_path, format="PNG", optimize=True, compress_level=9)
        data = buf_path.read_bytes()
        buf_path.unlink(missing_ok=True)
        if len(data) < len(best):
            best = data
    path.write_bytes(best)


def assert_no_error_toast(page) -> None:
    bad = page.locator(".toast--error, .toast[data-type='error'], .toasts .toast--error")
    if bad.count() and bad.first.is_visible():
        text = bad.first.inner_text()
        raise RuntimeError(f"unexpected error toast: {text}")


def wait_ready(page) -> None:
    page.wait_for_function("() => document.fonts && document.fonts.status === 'loaded'")
    page.evaluate("() => { document.activeElement && document.activeElement.blur && document.activeElement.blur(); }")
    assert_no_error_toast(page)


def layout_report(page, dialog_sel: str | None = None) -> dict:
    return page.evaluate(
        """(dialogSel) => {
          const html = document.documentElement;
          const body = document.body;
          const report = {
            innerWidth: window.innerWidth,
            innerHeight: window.innerHeight,
            scrollWidth: Math.max(html.scrollWidth, body.scrollWidth),
            overflowX: Math.max(html.scrollWidth, body.scrollWidth) > window.innerWidth + 1,
            dialogClipped: false,
            primaryVisible: true,
          };
          if (dialogSel) {
            const dlg = document.querySelector(dialogSel);
            if (dlg && dlg.open) {
              const r = dlg.getBoundingClientRect();
              report.dialogClipped = r.left < -1 || r.top < -1
                || r.right > window.innerWidth + 1 || r.bottom > window.innerHeight + 1;
              const primary = dlg.querySelector('#folder-choose, #setup-next, .btn--primary');
              if (primary) {
                const pr = primary.getBoundingClientRect();
                const style = getComputedStyle(primary);
                report.primaryVisible = style.visibility !== 'hidden' && style.display !== 'none'
                  && pr.width > 0 && pr.height > 0
                  && pr.bottom <= window.innerHeight + 1 && pr.top >= -1;
              }
            }
          }
          return report;
        }""",
        dialog_sel,
    )


def capture(page, path: Path, clip=None, dialog_sel: str | None = None) -> dict:
    wait_ready(page)
    report = layout_report(page, dialog_sel)
    if report.get("overflowX"):
        raise RuntimeError(f"horizontal overflow before capture: {path.name}")
    if report.get("dialogClipped"):
        raise RuntimeError(f"dialog clipped before capture: {path.name}")
    if dialog_sel and not report.get("primaryVisible", True):
        raise RuntimeError(f"primary action not fully visible: {path.name}")
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


def new_page(browser, theme: str):
    context = browser.new_context(
        viewport=VIEWPORT,
        device_scale_factor=DPR,
        locale="ru-RU",
        color_scheme=theme,
    )
    page = context.new_page()
    page.add_init_script(
        """() => {
          try { localStorage.clear(); sessionStorage.clear(); } catch (e) {}
        }"""
    )
    return context, page


def open_preview(page, base: str, shot: str, theme: str, hash_path: str = "#/") -> None:
    url = f"{base}/preview.html?shot={shot}&freeze=1&theme={theme}{hash_path}"
    page.goto(url, wait_until="networkidle")
    page.wait_for_function(
        """() => {
          const boot = document.querySelector('#boot-view');
          return !boot || boot.hidden;
        }"""
    )
    page.wait_for_function(
        """(theme) => document.documentElement.getAttribute('data-theme') === theme""",
        arg=theme,
    )


def select_demo_folders(page) -> None:
    page.wait_for_selector("#flow-source-tree .folder-check, #flow-source-tree .folder-row")
    for folder_name in ("Фотографии", "Документы", "Видео"):
        checkbox = page.locator(
            f'#flow-source-tree .folder-check[data-path="{folder_name}"]'
        )
        if checkbox.count() == 0:
            page.locator("#flow-source-tree").get_by_text(folder_name, exact=True).first.click()
        elif not checkbox.first.is_checked():
            checkbox.first.check(force=True)
    page.wait_for_function(
        """() => {
          const disk = document.body.innerText.includes('Весь диск');
          const existing = document.body.innerText.includes('Показать папки на сервере');
          const checks = Array.from(document.querySelectorAll('#flow-source-tree .folder-check'))
            .filter((el) => el.checked).length;
          return disk && existing && checks >= 3;
        }"""
    )


def shot_home(browser, base: str, out: Path, theme: str, name: str) -> dict:
    context, page = new_page(browser, theme)
    try:
        open_preview(page, base, "home", theme)
        page.wait_for_selector("#app-view:not([hidden])")
        page.wait_for_selector("#job-list .job-row")
        page.wait_for_function(
            """() => {
              const login = document.querySelector('#account-cell-login');
              const stats = document.querySelector('#job-list .task-stats');
              return login && login.textContent.includes('alex.demo')
                && stats && /ГБ|МБ/.test(stats.textContent)
                && /\\/с|≈/.test(stats.textContent);
            }"""
        )
        page.locator("#flow-start").wait_for(state="visible")
        return capture(page, out / name, clip=HOME_CLIP)
    finally:
        context.close()


def shot_downloads(browser, base: str, out: Path, theme: str, name: str) -> dict:
    context, page = new_page(browser, theme)
    try:
        open_preview(page, base, "downloads", theme)
        page.wait_for_selector("#job-list .job-row")
        page.wait_for_function("() => document.querySelectorAll('#job-list .job-row').length >= 4")
        page.locator("#flow-start").wait_for(state="visible")
        return capture(page, out / name)
    finally:
        context.close()


def shot_connect(browser, base: str, out: Path, theme: str, name: str) -> dict:
    context, page = new_page(browser, theme)
    try:
        open_preview(page, base, "connect", theme, hash_path="#/setup")
        page.wait_for_selector("#setup-view:not([hidden])")
        page.locator("#method-webdav").click()
        page.wait_for_selector("#f-user")
        page.fill("#f-user", "")
        page.fill("#f-pass", "")
        page.evaluate("() => { document.activeElement && document.activeElement.blur(); }")
        page.wait_for_function(
            """() => {
              const methods = Array.from(document.querySelectorAll('.method-option, .method-primary, .method-alt'))
                .map((el) => el.textContent.trim());
              const user = document.querySelector('#f-user');
              const pwd = document.querySelector('#f-pass');
              const yandex = document.querySelector('#method-yandex');
              return methods.includes('Яндекс ID') && methods.includes('Пароль приложения') && methods.includes('Токен')
                && yandex && yandex.classList.contains('method-primary')
                && user && !user.value && pwd && !pwd.value;
            }"""
        )
        page.locator("#setup-next").wait_for(state="visible")
        return capture(page, out / name, dialog_sel="#setup-view")
    finally:
        context.close()


def shot_select_files(browser, base: str, out: Path, theme: str, name: str) -> dict:
    context, page = new_page(browser, theme)
    try:
        open_preview(page, base, "select", theme)
        page.locator("#flow-start").click()
        page.wait_for_selector("#folder-dialog[open]")
        select_demo_folders(page)
        page.locator("#folder-choose").wait_for(state="visible")
        return capture(page, out / name, dialog_sel="#folder-dialog")
    finally:
        context.close()


def shot_destination(browser, base: str, out: Path, theme: str, name: str) -> dict:
    """Real UI path: Скачать → select → Далее → Изменить (destination)."""
    context, page = new_page(browser, theme)
    try:
        open_preview(page, base, "destination", theme)
        page.locator("#flow-start").click()
        page.wait_for_selector("#folder-dialog[open]")
        select_demo_folders(page)
        page.locator("#folder-choose").click()  # Далее → review
        page.wait_for_function(
            """() => {
              const dlg = document.querySelector('#folder-dialog');
              return dlg && dlg.open && dlg.dataset.context === 'flow-review';
            }"""
        )
        # Wait until review shows destination edit control.
        page.locator('#folder-dialog button.review-change[data-edit="dest"]').wait_for(state="visible")
        page.locator('#folder-dialog button.review-change[data-edit="dest"]').click()
        page.wait_for_function(
            """() => {
              const dlg = document.querySelector('#folder-dialog');
              return dlg && dlg.open && dlg.dataset.context === 'flow-dest';
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
              return path && path.textContent.trim().length > 0
                && create && /Создать папку/.test(create.textContent)
                && primary && !primary.disabled && primary.getClientRects().length
                && body && (body.querySelectorAll('.folder-row, [data-path]').length >= 1
                  || /Фото|Видео|Документы|Музыка|Новые|подпапок/.test(text));
            }"""
        )
        # Prefer /downloads listing when current leaf is empty «Новые».
        path_text = page.locator("#folder-path").inner_text()
        if "Новые" in path_text:
            up = page.locator("#folder-up")
            if up.count() and up.first.is_enabled():
                up.first.click()
                page.wait_for_function(
                    """() => {
                      const path = document.querySelector('#folder-path');
                      const body = document.querySelector('#flow-dest-body');
                      return path && path.textContent.includes('/downloads')
                        && body && /Новые|Фото|Видео|Документы|Музыка/.test(body.innerText);
                    }"""
                )
        page.locator("#folder-choose").wait_for(state="visible")
        page.evaluate("() => { document.activeElement && document.activeElement.blur(); }")
        return capture(page, out / name, dialog_sel="#folder-dialog")
    finally:
        context.close()


def ensure_preview() -> None:
    sys.path.insert(0, str(Path(__file__).resolve().parent))
    from build_preview import build  # type: ignore

    built = build()
    preview = ROOT / "preview.html"
    if not preview.is_file() or preview.read_text(encoding="utf-8") != built:
        preview.write_text(built, encoding="utf-8")
        print("refreshed preview.html from web assets")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT)
    parser.add_argument("--chromium", type=Path, default=CHROMIUM)
    args = parser.parse_args()
    out: Path = args.out
    out.mkdir(parents=True, exist_ok=True)
    ensure_preview()

    httpd, base = serve(ROOT)
    reports = {}
    issues = []
    try:
        with sync_playwright() as p:
            launch_kwargs = {
                "headless": True,
                "args": ["--hide-scrollbars", "--disable-lcd-text", "--font-render-hinting=none"],
            }
            if args.chromium.exists():
                launch_kwargs["executable_path"] = str(args.chromium)
            browser = p.chromium.launch(**launch_kwargs)

            shots = [
                ("home-light.png", shot_home, "light"),
                ("home-dark.png", shot_home, "dark"),
                ("downloads-states.png", shot_downloads, "light"),
                ("downloads-states-dark.png", shot_downloads, "dark"),
                ("connect.png", shot_connect, "light"),
                ("connect-dark.png", shot_connect, "dark"),
                ("select-files.png", shot_select_files, "light"),
                ("select-files-dark.png", shot_select_files, "dark"),
                ("destination.png", shot_destination, "light"),
                ("destination-dark.png", shot_destination, "dark"),
            ]
            for name, fn, theme in shots:
                reports[name] = fn(browser, base, out, theme, name)

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
