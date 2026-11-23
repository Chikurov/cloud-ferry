#!/usr/bin/env python3
"""Build preview.html from working web assets + offline demo transport.

Usage:
  python3 scripts/build_preview.py          # write preview.html
  python3 scripts/build_preview.py --check  # fail if regeneration would change it
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
WEB = ROOT / "internal" / "httpapi" / "web"
OUT = ROOT / "preview.html"
DEMO = ROOT / "scripts" / "preview-demo.js"

DEMO_DIALOG = (
    '<dialog id="demo-provider" class="modal modal--confirm">'
    '<div class="modal-head"><h2 class="modal-title">Демо входа через Яндекс</h2></div>'
    '<p class="modal-text">Используется учебный аккаунт.</p>'
    '<form method="dialog" class="modal-actions">'
    '<button class="btn btn--primary" id="demo-approve" type="button">Подтвердить вход</button>'
    '<button class="btn btn--quiet">Назад</button></form></dialog>'
)

SHOT_STYLE = """<style id="shot-capture-style">
  html[data-shot] *, html[data-shot] *::before, html[data-shot] *::after { caret-color: transparent !important; }
  html[data-shot], html[data-shot] *, html[data-shot] *:hover { cursor: none !important; }
  html[data-shot] *:focus, html[data-shot] *:focus-visible {
    outline: none !important;
    box-shadow: none !important;
  }
</style>
"""

THEME_BOOT = """<script>
(function () {
  var params = new URLSearchParams(location.search);
  var forced = params.get('theme');
  var saved = null;
  try { saved = localStorage.getItem('cloudferry-theme'); } catch (e) { /* ignore */ }
  var theme = (forced === 'light' || forced === 'dark')
    ? forced
    : (saved === 'light' || saved === 'dark'
      ? saved
      : ((window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches) ? 'dark' : 'light'));
  document.documentElement.setAttribute('data-theme', theme);
  document.documentElement.style.colorScheme = theme;
  var meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute('content', theme === 'dark' ? '#16151a' : '#f5f4f8');
  if (params.get('shot') || params.has('freeze')) {
    document.documentElement.setAttribute('data-shot', params.get('shot') || 'freeze');
    try { localStorage.setItem('cloudferry-theme', theme); } catch (e) { /* ignore */ }
  }
})();
</script>
"""


def read(path: Path) -> str:
    return path.read_text(encoding="utf-8")


def inline_script(source: str) -> str:
    return f"<script>\n{source.rstrip()}\n</script>\n"


def build() -> str:
    index = read(WEB / "index.html")
    style = read(WEB / "style.css")
    auth = read(WEB / "auth-view.js")
    transfer = read(WEB / "transfer-view.js")
    app = read(WEB / "app.js")
    demo = read(DEMO)

    html = index
    html = html.replace("<title>Cloud Ferry</title>", "<title>Cloud Ferry · демо</title>", 1)
    html = re.sub(
        r'<script src="/theme\.js"></script>\s*',
        THEME_BOOT + SHOT_STYLE,
        html,
        count=1,
    )
    html = re.sub(
        r'<link rel="stylesheet" href="/style\.css">\s*',
        f"<style>/* inlined from internal/httpapi/web/style.css */\n{style}\n</style>\n",
        html,
        count=1,
    )
    # Drop product script tags; insert demo + inlined modules before </body>.
    html = re.sub(r'\s*<script src="/auth-view\.js"></script>\s*', "\n", html)
    html = re.sub(r'\s*<script src="/transfer-view\.js"></script>\s*', "\n", html)
    html = re.sub(r'\s*<script src="/app\.js"></script>\s*', "\n", html)

    bundle = (
        DEMO_DIALOG + "\n"
        + inline_script(demo)
        + inline_script(auth)
        + inline_script(transfer)
        + inline_script(app)
    )
    if "</body>" not in html:
        raise SystemExit("index.html missing </body>")
    html = html.replace("</body>", bundle + "</body>", 1)
    if not html.endswith("\n"):
        html += "\n"
    return html


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="fail if preview.html is stale")
    args = parser.parse_args()
    built = build()
    if args.check:
        if not OUT.is_file():
            print("preview.html missing", file=sys.stderr)
            return 1
        current = OUT.read_text(encoding="utf-8")
        if current != built:
            print("preview.html is stale; run: python3 scripts/build_preview.py", file=sys.stderr)
            return 1
        print("preview.html is up to date")
        return 0
    OUT.write_text(built, encoding="utf-8")
    print(f"wrote {OUT} ({len(built)} bytes)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
