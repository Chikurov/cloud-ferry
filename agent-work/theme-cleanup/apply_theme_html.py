#!/usr/bin/env python3
"""Add theme bootstrap + toggle buttons, drop dead wizard/step DOM (task-7)."""
import sys
from pathlib import Path

path = Path('internal/httpapi/web/index.html')
text = path.read_text()

THEME_BUTTON = '''<button class="btn btn--ghost btn--icon theme-toggle js-theme-toggle" type="button" aria-label="Тёмная тема" aria-pressed="false" title="Тёмная тема">
          <svg class="icon theme-icon theme-icon--moon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><path d="M20 14.5A8.5 8.5 0 0 1 9.5 4 8.5 8.5 0 1 0 20 14.5Z"/></svg>
          <svg class="icon theme-icon theme-icon--sun" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><circle cx="12" cy="12" r="4"/><path d="M12 3v2m0 14v2M5.6 5.6l1.4 1.4m10 10 1.4 1.4M3 12h2m14 0h2M5.6 18.4l1.4-1.4m10-10 1.4-1.4"/></svg>
        </button>'''


def replace(old, new, count=1):
    global text
    found = text.count(old)
    if found != count:
        sys.exit(f'expected {count} occurrence(s), found {found}:\n{old[:200]}')
    text = text.replace(old, new)


# 1. Theme before style.css: no flash, data-theme + color-scheme on <html>.
replace('''  <meta name="theme-color" content="#f3f5f9">
  <title>Cloud Ferry</title>
''',
        '''  <meta name="theme-color" content="#f5f4f8">
  <title>Cloud Ferry</title>
  <!-- Тема ставится до style.css: первый кадр уже в выбранной теме. -->
  <script>
    (function () {
      var theme = 'light';
      try {
        var saved = localStorage.getItem('cloudferry-theme');
        theme = saved === 'light' || saved === 'dark'
          ? saved
          : (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
      } catch (e) { /* localStorage может быть недоступен */ }
      document.documentElement.setAttribute('data-theme', theme);
      document.documentElement.style.colorScheme = theme;
      var meta = document.querySelector('meta[name="theme-color"]');
      if (meta) meta.setAttribute('content', theme === 'dark' ? '#16151a' : '#f5f4f8');
    })();
  </script>
''')

# 2. Toggle in the setup header (first run has no app header).
replace('''    <div class="setup-brand">
      <span class="logo" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M7.2 18a4.2 4.2 0 0 1-.4-8.38A5.6 5.6 0 0 1 17.6 10.6 3.7 3.7 0 0 1 17 18H7.2Z"/></svg>
      </span>
      <span>Cloud Ferry</span>
    </div>
''',
        f'''    <div class="setup-brand">
      <span class="logo" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M7.2 18a4.2 4.2 0 0 1-.4-8.38A5.6 5.6 0 0 1 17.6 10.6 3.7 3.7 0 0 1 17 18H7.2Z"/></svg>
      </span>
      <span>Cloud Ferry</span>
      {THEME_BUTTON.replace(chr(10) + "          ", chr(10) + "        ")}
    </div>
''')

# 3. Toggle in the app header. `id` stays on nothing: the class drives both.
replace('''      <div class="shell-actions">
        <span id="offline-dot" class="offline-dot" title="Нет связи с сервером" hidden><span class="sr-only">Нет связи с сервером</span></span>
''',
        f'''      <div class="shell-actions">
        {THEME_BUTTON}
        <span id="offline-dot" class="offline-dot" title="Нет связи с сервером" hidden><span class="sr-only">Нет связи с сервером</span></span>
''')

# 4. Dead wizard step indicator and invisible flow step counter.
replace('''      <p class="sr-only" id="setup-step-text"></p>
      <div class="setup-steps" id="setup-steps" aria-hidden="true"></div>
''', '')
replace('''        <span class="flow-step-pos" id="flow-step-pos" hidden></span>
''', '')

path.write_text(text)
print('index.html updated:', len(text), 'bytes')
