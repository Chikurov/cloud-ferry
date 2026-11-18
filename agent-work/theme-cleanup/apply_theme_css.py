#!/usr/bin/env python3
"""Apply theme token sets + contrast fixes to style.css (task-7)."""
import sys
from pathlib import Path

path = Path('internal/httpapi/web/style.css')
text = path.read_text()


def replace(old, new, count=1):
    global text
    found = text.count(old)
    if found != count:
        sys.exit(f'expected {count} occurrence(s), found {found}:\n{old[:200]}')
    text = text.replace(old, new)


# 1. Explicit light/dark token sets instead of light-dark() in the design layer.
OLD_ROOT = '''/* Cloud Ferry — рабочая область */
:root {
  color-scheme: light dark;
  --page: light-dark(#f5f4f8, #16151a);
  --surface: light-dark(#fff, #222127);
  --surface-raised: var(--surface);
  --ink: light-dark(#26222e, #f3eff9);
  --ink-soft: light-dark(#4b4555, #dad3e2);
  --muted: light-dark(#655e70, #b9b1c4);
  --accent: light-dark(#684c96, #b69add);
  --accent-hover: light-dark(#553d80, #c7abea);
  --accent-active: light-dark(#46326c, #a98ad5);
  --accent-soft: color-mix(in srgb, var(--accent) 13%, transparent);
  --hairline: light-dark(#dedbe5, #39353f);
  --line: var(--hairline);
  --border-strong: var(--hairline);
  --border-control: light-dark(#94909b, #777080);
  --border-hover: var(--border-control);
  --surface-sunken: light-dark(#f2f0f5, #2c2932);
  --focus: var(--accent);
  --focus-ring: var(--accent-soft);
  --ok: light-dark(#21704d, #96d2ac);
  --warn: light-dark(#795600, #ebcc8d);
  --err: light-dark(#a72b3d, #ff9ca9);
  --amber: light-dark(#825319, #ecc68e);
}
'''

NEW_ROOT = '''/* Cloud Ferry — рабочая область.
   Две полные темы: скрипт в <head> ставит data-theme на <html> до style.css,
   поэтому первый кадр уже в выбранной теме. Наборы ниже — явные значения на
   существующих токенах; light-dark() в теме не используется, чтобы выбор
   пользователя не зависел от системной настройки. */

:root,
:root[data-theme="light"] {
  color-scheme: light;
  --page: #f5f4f8;
  --surface: #fff;
  --surface-raised: #fff;
  --ink: #26222e;
  --ink-soft: #4b4555;
  --muted: #655e70;
  --accent: #684c96;
  --accent-hover: #553d80;
  --accent-active: #46326c;
  --accent-soft: color-mix(in srgb, var(--accent) 13%, transparent);
  --on-accent: #fff;
  --hairline: #dedbe5;
  --line: var(--hairline);
  --border-strong: var(--hairline);
  /* 3,25:1 к фону страницы и 3,12:1 к белой поверхности (WCAG 1.4.11). */
  --border-control: #8b8792;
  --border-hover: var(--border-control);
  --surface-sunken: #f2f0f5;
  --skeleton: #e7eaef;
  --switch-off: #d3d7dd;
  --check-off-border: #c7cbd1;
  --check-off-bg: #f3f4f5;
  --check-disabled: #8fa9de;
  --focus: var(--accent);
  --focus-ring: var(--accent-soft);
  --ok: #21704d;
  --warn: #795600;
  --err: #a72b3d;
  --amber: #825319;
}

:root[data-theme="dark"] {
  color-scheme: dark;
  --page: #16151a;
  --surface: #222127;
  --surface-raised: #222127;
  --ink: #f3eff9;
  --ink-soft: #dad3e2;
  --muted: #b9b1c4;
  --accent: #b69add;
  --accent-hover: #c7abea;
  --accent-active: #a98ad5;
  --accent-soft: color-mix(in srgb, var(--accent) 13%, transparent);
  --on-accent: #201827;
  --hairline: #39353f;
  --line: var(--hairline);
  --border-strong: var(--hairline);
  /* 3,31:1 к самой тёмной поверхности (--surface-sunken). */
  --border-control: #7d7787;
  --border-hover: var(--border-control);
  --surface-sunken: #2c2932;
  --skeleton: #34303c;
  --switch-off: #4a4552;
  --check-off-border: #595463;
  --check-off-bg: #302c38;
  --check-disabled: #7f6ca8;
  --focus: var(--accent);
  --focus-ring: var(--accent-soft);
  --ok: #96d2ac;
  --warn: #ebcc8d;
  --err: #ff9ca9;
  --amber: #ecc68e;
}
'''
replace(OLD_ROOT, NEW_ROOT)

# 2. Remaining light-dark() property values become tokens from the sets above.
replace('  background: light-dark(#e7eaef, #34303c);\n',
        '  background: var(--skeleton);\n')
replace('  background: light-dark(#d3d7dd, #4a4552);\n',
        '  background: var(--switch-off);\n')
replace('''  border-color: light-dark(#c7cbd1, #595463);
  background: light-dark(#f3f4f5, #302c38);
''',
        '''  border-color: var(--check-off-border);
  background: var(--check-off-bg);
''')
replace('''  border-color: light-dark(#8fa9de, #7f6ca8);
  background: light-dark(#8fa9de, #7f6ca8);
''',
        '''  border-color: var(--check-disabled);
  background: var(--check-disabled);
''')
replace('.logo { width:36px; height:36px; background:var(--accent); color:light-dark(white,#22162e); border-radius:12px; }\n',
        '.logo { width:36px; height:36px; background:var(--accent); color:var(--on-accent); border-radius:12px; }\n')
replace('.btn--primary { color:light-dark(white,#201827); background:var(--accent); }\n',
        '.btn--primary { --spin: var(--on-accent); color:var(--on-accent); background:var(--accent); }\n')

# 3. Contrast fixes found in both themes (text and meaningful borders).
replace('.status-pill--running { background: var(--accent-soft); color: #0a5cbf; }\n',
        '.status-pill--running { background: var(--accent-soft); color: var(--accent); }\n')
replace('.account-probe-result--busy { background: var(--accent-soft); color: #0a5cbf; }\n',
        '.account-probe-result--busy { background: var(--accent-soft); color: var(--accent); }\n')
replace("input::placeholder, select::placeholder, textarea::placeholder { color: #6b7280; opacity: 1; }\n",
        'input::placeholder, select::placeholder, textarea::placeholder { color: var(--muted); opacity: 1; }\n')
replace('''  border-color: var(--accent);
  outline: none;
  box-shadow: 0 0 0 3px var(--focus-ring);
  background-color: #fff;
''',
        '''  border-color: var(--accent);
  outline: none;
  box-shadow: 0 0 0 3px var(--focus-ring);
  background-color: var(--surface-raised);
''')
# Checkmarks on the accent fill: --on-accent keeps them visible in both themes.
replace('''  left: 6px;
  top: 2px;
  width: 5px;
  height: 10px;
  border: solid #fff;
''',
        '''  left: 6px;
  top: 2px;
  width: 5px;
  height: 10px;
  border: solid var(--on-accent);
''', count=3)
# done-icon used #fff on --ok; the "Готово" screen is removed below, so drop it too.

# 4. Theme toggle button in the new design layer.
replace('.account-chip { width:40px; justify-content:center; padding:0; background:var(--surface); }\n',
        '''.account-chip { width:40px; justify-content:center; padding:0; background:var(--surface); }
/* Переключатель темы: иконка показывает текущую тему, aria-pressed —
   состояние для скринридера. */
.theme-toggle { flex:none; width:40px; min-width:40px; height:40px; padding:0; color:var(--muted); }
.theme-toggle:hover { color:var(--ink); background:var(--surface-sunken); }
.theme-toggle--setup { margin-left:4px; }
.theme-icon--sun { display:none; }
[data-theme="dark"] .theme-icon--sun { display:block; }
[data-theme="dark"] .theme-icon--moon { display:none; }
''')

# 5. Dead CSS of the unreachable wizard steps and the removed step counter.
replace('''#setup-body { display: grid; gap: 12px; }
.setup-steps { display: flex; gap: 8px; }
.setup-steps i {
  flex: 1;
  height: 3px;
  border-radius: var(--r-pill);
  background: var(--surface-sunken);
  transition: background-color var(--duration);
}
.setup-steps i.is-done { background: var(--accent); }
''',
        '#setup-body { display: grid; gap: 12px; }\n')
replace('''/* Шаг «Готово»: только подтверждение подключения и переход к скачиванию. */
.setup-title--done { font-size: var(--text-xl); line-height: 26px; }
.done-hero {
  display: grid;
  grid-template-columns: 40px minmax(0, 1fr);
  align-items: center;
  gap: 12px;
  padding: 16px;
  border: 1px solid rgba(6, 122, 87, .2);
  border-radius: var(--r-lg);
  background: var(--ok-tint);
}
.done-icon {
  display: grid;
  place-items: center;
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: var(--ok);
  color: #fff;
}
.done-icon .icon { width: 20px; height: 20px; stroke-width: 2; }
/* Футер мастера: вторичное действие слева, главное — справа. На узком
   экране главная кнопка переносится и остаётся выровненной вправо. */
''',
        '''/* Футер мастера: вторичное действие слева, главное — справа. На узком
   экране главная кнопка переносится и остаётся выровненной вправо. */
''')
replace('.setup-steps { display:none; }\n', '')
replace('''/* ── 20. Flow v3: шапка, меню источника, сводка ───────────────────────────
   Шапка называется «Новое скачивание» один раз; позиция шага — «N из 3» без
   полосок. Одна основная кнопка на представление, остальное — тихие действия. */
''',
        '''/* ── 20. Flow v3: шапка, меню источника, сводка ───────────────────────────
   Шапка называется «Новое скачивание» один раз. Одна основная кнопка на
   представление, остальное — тихие действия. */
''')
replace('''.flow-step-pos {
  color: var(--muted);
  font-size: var(--text-sm);
  line-height: 20px;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

''', '')

path.write_text(text)
print('style.css updated:', len(text), 'bytes')
