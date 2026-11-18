#!/usr/bin/env python3
"""Task-7 cleanup in app.js: theme toggle, dead wizard steps, dead DOM/branches."""
import sys
from pathlib import Path

path = Path('internal/httpapi/web/app.js')
text = path.read_text()


def replace(old, new, count=1):
    global text
    found = text.count(old)
    if found != count:
        sys.exit(f'expected {count} occurrence(s), found {found}:\n{old[:240]}')
    text = text.replace(old, new)


# ── 1. Theme toggle ────────────────────────────────────────────────────────
replace('''/* --- header: account panel ------------------------------------------------ */

function renderHeader() {''',
        '''/* --- тема ----------------------------------------------------------------- */

/* Тема выбрана в <head> до style.css; здесь только переключение и состояние
   кнопки. Производные данные (meta theme-color, aria-pressed) держим
   синхронными с data-theme, чтобы скринридер и адресная строка не врали. */
const THEME_KEY = 'cloudferry-theme';
const THEME_COLORS = { light: '#f5f4f8', dark: '#16151a' };

function currentTheme() {
  return document.documentElement.getAttribute('data-theme') === 'dark' ? 'dark' : 'light';
}

function syncThemeControls() {
  const dark = currentTheme() === 'dark';
  for (const button of $$('.js-theme-toggle')) {
    button.setAttribute('aria-pressed', dark ? 'true' : 'false');
    button.setAttribute('aria-label', 'Тёмная тема');
    button.title = 'Тёмная тема';
  }
}

function applyTheme(theme, persist) {
  const next = theme === 'dark' ? 'dark' : 'light';
  document.documentElement.setAttribute('data-theme', next);
  document.documentElement.style.colorScheme = next;
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute('content', THEME_COLORS[next]);
  if (persist) {
    try { localStorage.setItem(THEME_KEY, next); } catch { /* приватный режим */ }
  }
  syncThemeControls();
}

for (const button of $$('.js-theme-toggle')) {
  button.addEventListener('click', () => {
    applyTheme(currentTheme() === 'dark' ? 'light' : 'dark', true);
  });
}
syncThemeControls();

/* --- header: account panel ------------------------------------------------ */

function renderHeader() {''')

# ── 2. Unreachable wizard steps 1–2 and the destination picker ─────────────
replace('''let wizard = {
  step: 0,
  method: 'webdav',
  dest: '/downloads',
  accountID: '',
  login: '',
  fields: {},
  methodTouched: false,
};
let preferredDest = '';
''',
        '''let wizard = {
  method: 'webdav',
  accountID: '',
  login: '',
  fields: {},
  methodTouched: false,
};
''')

replace('''  wizard = {
    step: 0,
    method: yandexDefault ? 'yandex' : 'webdav',
    dest: preferredDest || state.downloads || '/downloads',
    accountID: '',
    login: '',
    fields: {},
    tokenHelp: false,
    methodTouched: false,
    announcedStep: -1,
  };
''',
        '''  wizard = {
    method: yandexDefault ? 'yandex' : 'webdav',
    accountID: '',
    login: '',
    fields: {},
    tokenHelp: false,
    methodTouched: false,
  };
''')

replace('''  const map = {
    user: 'f-user', login: 'f-user', password: 'f-pass', pass: 'f-pass',
    token: 'f-token',
    destPath: 'f-dest', dest: 'f-dest', path: 'f-dest',
  };
''',
        '''  const map = {
    user: 'f-user', login: 'f-user', password: 'f-pass', pass: 'f-pass',
    token: 'f-token',
  };
''')

replace('''const STEP_TITLES = ['Подключение', 'Папка на сервере', 'Готово'];

function setupStepBody() {
  if (wizard.step === 0) {
    return `
      <h1 class="setup-title" id="setup-title" tabindex="-1">${STEP_TITLES[0]}</h1>
      ${methodSwitch()}
      <div class="setup-fields" id="setup-fields">${methodFields()}</div>`;
  }
  if (wizard.step === 1) {
    return `
      <h1 class="setup-title" id="setup-title" tabindex="-1">${STEP_TITLES[1]}</h1>
      <div class="control">
        <div class="field-row">
          <input id="f-dest" aria-labelledby="setup-title" value="${escapeHtml(displayPath(wizard.dest))}" autocomplete="off" spellcheck="false">
          <button class="btn btn--secondary btn--browse" type="button" data-setup-browse aria-label="Выбрать папку на сервере">Обзор</button>
        </div>
      </div>`;
  }
  return `
    <div class="done-hero">
      <span class="done-icon" aria-hidden="true">${icon('check')}</span>
      <h1 class="setup-title setup-title--done" id="setup-title" tabindex="-1">Готово, диск подключён</h1>
    </div>`;
}

function renderSetup(options = {}) {
  const focusTitle = options.focusTitle !== false;
  $('#setup-step-text').textContent = `${STEP_TITLES[wizard.step]}`;
  /* Полоски шагов декоративные (aria-hidden у контейнера), поэтому
     aria-current="step" внутри них не нужен: текущий шаг озвучивается
     скрытым текстом и announce(). */
  $('#setup-steps').innerHTML = STEP_TITLES.map((label, index) => {
    const done = index <= wizard.step ? 'is-done' : '';
    return `<i data-label="${label}" class="${done}"></i>`;
  }).join('');
  $('#setup-body').innerHTML = setupStepBody();
  $('#setup-error').textContent = '';
  stopValidateHint();
  setSetupFieldsDisabled(false);
  const actions = $('#setup-actions');
  const next = $('#setup-next');
  const back = $('#setup-back');
  setBusy(next, false);
  if (actions) actions.hidden = false;
  next.hidden = false;
  if (wizard.step === 0) {
    back.hidden = false;
    back.textContent = 'Отмена';
    if (wizard.method === 'yandex') {
      /* У Яндекс-входа главное действие — в теле панели, а не в футере:
         «Подключиться» здесь ничего не значит, пока нет кода. */
      next.hidden = true;
      if (yandexAuth.status === 'waiting' && actions) actions.hidden = true;
    } else {
      next.textContent = 'Подключиться';
    }
  } else if (wizard.step === 1) {
    back.hidden = false;
    back.textContent = 'Назад';
    next.textContent = 'Продолжить';
  } else {
    /* Экран успеха: только подтверждение и переход к скачиванию. */
    back.hidden = true;
    next.textContent = 'Перейти к скачиванию';
  }
  if (wizard.announcedStep !== wizard.step) {
    wizard.announcedStep = wizard.step;
    announce(`${STEP_TITLES[wizard.step]}`);
  }
  /* Live-регион способа заполняем после вставки разметки: пустой узел,
     созданный вместе с панелью, не озвучивается, а изменение — да. */
  syncYandexLive();
  if (focusTitle) focusSetupTitle();
}
''',
        '''function setupStepBody() {
  return `
      <h1 class="setup-title" id="setup-title" tabindex="-1">Подключение</h1>
      ${methodSwitch()}
      <div class="setup-fields" id="setup-fields">${methodFields()}</div>`;
}

function renderSetup(options = {}) {
  const focusTitle = options.focusTitle !== false;
  $('#setup-body').innerHTML = setupStepBody();
  $('#setup-error').textContent = '';
  stopValidateHint();
  setSetupFieldsDisabled(false);
  const actions = $('#setup-actions');
  const next = $('#setup-next');
  setBusy(next, false);
  if (actions) actions.hidden = false;
  next.hidden = false;
  next.textContent = 'Подключиться';
  if (wizard.method === 'yandex') {
    /* У Яндекс-входа главное действие — в теле панели, а не в футере:
       «Подключиться» здесь ничего не значит, пока нет кода. */
    next.hidden = true;
    if (yandexAuth.status === 'waiting' && actions) actions.hidden = true;
  }
  /* Live-регион способа заполняем после вставки разметки: пустой узел,
     созданный вместе с панелью, не озвучивается, а изменение — да. */
  syncYandexLive();
  if (focusTitle) focusSetupTitle();
}
''')

replace('''  const dest = document.getElementById('f-dest');
  if (dest) wizard.dest = dest.value;
}
''', '}\n')

replace("  if (wizard.step !== 0 || wizard.method !== 'yandex') return '';\n",
        "  if (wizard.method !== 'yandex') return '';\n")

replace('''    stopValidateHint();
    setupBusy = false;
    setSetupFieldsDisabled(false);
    if (wizard.step === 0) {
      setBusy($('#setup-next'), false);
      /* Фокус — на поле с ошибкой: скринридер прочитает её из aria-describedby.
         Если ошибка общая, возвращаем фокус на «Подключиться», а не на body. */
      const invalid = scope ? scope.querySelector('.control.has-error input, .control.has-error textarea') : null;
      if (invalid) invalid.focus({ preventScroll: true });
      else if (document.activeElement === document.body) $('#setup-next').focus({ preventScroll: true });
    }
  }
}
''',
        '''    stopValidateHint();
    setupBusy = false;
    setSetupFieldsDisabled(false);
    setBusy($('#setup-next'), false);
    /* Фокус — на поле с ошибкой: скринридер прочитает её из aria-describedby.
       Если ошибка общая, возвращаем фокус на «Подключиться», а не на body. */
    const invalid = scope ? scope.querySelector('.control.has-error input, .control.has-error textarea') : null;
    if (invalid) invalid.focus({ preventScroll: true });
    else if (document.activeElement === document.body) $('#setup-next').focus({ preventScroll: true });
  }
}
''')

replace('''async function onSetupNext() {
  const next = $('#setup-next');
  if (setupBusy || isBusyControl(next)) return;
  /* У Яндекс-входа своя кнопка «Получить код» в панели: футер не участвует. */
  if (wizard.step === 0 && wizard.method === 'yandex') return;
  setupError('');
  wizard.methodTouched = true;
  saveSetupFields();
  if (wizard.step === 0) {
    /* Настоящая форма: так менеджер паролей видит отправку и предлагает
       сохранить вход. Для токена форма тоже настоящая, просто без пароля. */
    const form = document.getElementById('setup-form');
    if (form && typeof form.requestSubmit === 'function') { form.requestSubmit(); return; }
    await runConnect();
    return;
  }
  if (wizard.step === 1) {
    const input = $('#f-dest');
    const dest = (input ? input.value : '').trim();
    if (!dest) {
      setFieldError(input, 'Укажите папку на сервере.');
      if (input) input.focus();
      return;
    }
    wizard.dest = dest;
    preferredDest = dest;
    wizard.step = 2;
    renderSetup();
    return;
  }
  preferredDest = wizard.dest;
  /* Главный экран мог быть уже отрисован (повторный вход из меню):
     тогда поле «Папка на сервере» не пересоберётся, поэтому пишем путь явно. */
  applyPreferredDest();
  resetWizardFields();
  focusStartAfterRender = true;
  location.hash = '#/';
}

function onSetupBack() {
  if (setupBusy) return;
  setupError('');
  if (wizard.step === 0) {
    resetWizardFields();
    stopYandexFlow({ cancel: true });
    location.hash = '#/';
    return;
  }
  saveSetupFields();
  if (wizard.step === 1) { wizard.step = 0; renderSetup(); return; }
  wizard.step = 1;
  renderSetup();
}
''',
        '''async function onSetupNext() {
  const next = $('#setup-next');
  if (setupBusy || isBusyControl(next)) return;
  /* У Яндекс-входа своя кнопка «Получить код» в панели: футер не участвует. */
  if (wizard.method === 'yandex') return;
  setupError('');
  wizard.methodTouched = true;
  saveSetupFields();
  /* Настоящая форма: так менеджер паролей видит отправку и предлагает
     сохранить вход. Для токена форма тоже настоящая, просто без пароля. */
  const form = document.getElementById('setup-form');
  if (form && typeof form.requestSubmit === 'function') { form.requestSubmit(); return; }
  await runConnect();
}

function onSetupBack() {
  if (setupBusy) return;
  setupError('');
  resetWizardFields();
  stopYandexFlow({ cancel: true });
  location.hash = '#/';
}
''')

replace("  if ($('#setup-view').hidden || wizard.step !== 0) return;\n",
        "  if ($('#setup-view').hidden) return;\n")
replace("  if ($('#setup-view').hidden || wizard.step !== 0 || wizard.method !== 'yandex' || yandexAuth.status !== 'starting') {\n",
        "  if ($('#setup-view').hidden || wizard.method !== 'yandex' || yandexAuth.status !== 'starting') {\n")
replace("  if ($('#setup-view').hidden || wizard.step !== 0 || wizard.method !== 'yandex') return;\n",
        "  if ($('#setup-view').hidden || wizard.method !== 'yandex') return;\n", count=2)

# ── 3. Destination fallback chain without the wizard picker ────────────────
replace('''/* Папка, выбранная в мастере, становится начальным назначением шага 2:
   homeMarkup при повторном входе не пересобирается. */
function applyPreferredDest() {
  if (!preferredDest || flowDest) return;
  flowDest = displayPath(preferredDest);
  renderAccount();
}

''', '')
replace('''    if (focusStartAfterRender) {
      focusStartAfterRender = false;
      applyPreferredDest();
      const button = $('#flow-start');
''',
        '''    if (focusStartAfterRender) {
      focusStartAfterRender = false;
      const button = $('#flow-start');
''')
replace("  const dest = (flowDest || preferredDest || state.downloads || '/downloads').trim();\n",
        "  const dest = (flowDest || state.downloads || '/downloads').trim();\n")
replace("  return displayPath(flowDest || preferredDest || state.downloads || (root && root.path) || '/downloads');\n",
        "  return displayPath(flowDest || state.downloads || (root && root.path) || '/downloads');\n")
replace('''/* Одна сессия диалога «Новое скачивание» (и пикера папки в мастере).
   Шаги переключают context, но накопленные узлы дерева, раскрытые ветки,
''',
        '''/* Одна сессия диалога «Новое скачивание».
   Шаги переключают context, но накопленные узлы дерева, раскрытые ветки,
''')
replace("  chosen: false,\n};\n/* Создание папок", "};\n/* Создание папок")
replace("  context: 'setup', /* setup | flow-dest | flow-source | flow-review */",
        "  context: 'flow-dest', /* flow-dest | flow-source | flow-review */")
replace("    chosen: false,\n  };\n}", "  };\n}")
replace("  folder = folderSession('setup', null);\n}",
        "  folder = folderSession('flow-dest', null);\n}")
replace("  folder = folderSession('setup', null);\n  if (flowFocusNow) {",
        "  folder = folderSession('flow-dest', null);\n  if (flowFocusNow) {")

replace('''/* Пикер папки в мастере использует тот же диалог и тот же шаг назначения. */
function openFolderDialog(target, opener, context = 'setup') {
  /* Мастер выбирает только папку на сервере; облако — шаг 2 флоу. */
  if (target === 'cloud') return;
  const dialog = $('#folder-dialog');
  if (!dialog) return;
  folder = folderSession('setup', opener || null);
  folder.path = initialDestinationPath();
  if (!dialog.open) dialog.showModal();
  showFlowStep('setup');
}

''', '')
replace('''function initialDestinationPath() {
  const setupDest = document.getElementById('f-dest');
  const raw = String(
    flowDest
    || (setupDest && setupDest.value)
    || preferredDest
    || state.downloads
    || '/downloads'
  ).trim();
  return raw.startsWith('/') ? raw : `/${raw}`;
}
''',
        '''function initialDestinationPath() {
  const raw = String(flowDest || state.downloads || '/downloads').trim();
  return raw.startsWith('/') ? raw : `/${raw}`;
}
''')

# ── 4. Invisible flow step counter and the removed setup picker context ────
replace('''  'flow-review': { step: 3, title: 'Проверьте выбор' },
  setup: { step: 0, title: 'Папка на сервере' },
};''',
        '''  'flow-review': { step: 3, title: 'Проверьте выбор' },
};''')
replace("  const context = folder.context || 'setup';\n  const info = FLOW_STEPS[context] || FLOW_STEPS.setup;\n",
        "  const context = folder.context || 'flow-dest';\n  const info = FLOW_STEPS[context] || FLOW_STEPS['flow-dest'];\n")
replace('''  const stepTitle = $('#flow-step-title');
  const position = $('#flow-step-pos');
  const stepHead = $('#flow-step-head');
''',
        '''  const stepTitle = $('#flow-step-title');
  const stepHead = $('#flow-step-head');
''')
replace('''  if (position) {
    position.hidden = !info.step;
    position.textContent = info.step ? `${info.step} из 3` : '';
  }
''', '')
replace("  if (context === 'flow-dest' || context === 'setup') {\n",
        "  if (context === 'flow-dest') {\n")
replace("  const info = FLOW_STEPS[context] || FLOW_STEPS.setup;\n",
        "  const info = FLOW_STEPS[context] || FLOW_STEPS['flow-dest'];\n")

# ── 5. Footer and close paths without the setup context ───────────────────
replace('''function renderFlowFooter() {
  const context = folder.context || 'setup';
''',
        '''function renderFlowFooter() {
  const context = folder.context || 'flow-dest';
''')
replace("  back.textContent = context === 'setup' ? 'Отмена' : 'Назад';\n",
        "  back.textContent = 'Назад';\n")
replace('''    noteText = folder.status || '';
    noteTone = folder.status ? 'status' : '';
  } else {
    back.hidden = false;
    back.textContent = 'Отмена';
    back.disabled = false;
    primary.textContent = 'Выбрать эту папку';
    primary.disabled = !!folder.loading || !!folder.error || !folder.path || folder.writable === false;
  }
''',
        '''    noteText = folder.status || '';
    noteTone = folder.status ? 'status' : '';
  }
''')

# ── 6. Dead setup picker actions, chooseSetupFolder and renderStorage ──────
replace('''  if (folder.context === 'flow-review') {
    startTransfer();
    return;
  }
  chooseSetupFolder();
}

function flowBackAction() {
''',
        '''  if (folder.context === 'flow-review') {
    startTransfer();
  }
}

function flowBackAction() {
''')
replace('''  if (folder.context === 'setup') {
    requestCloseFlow();
    return;
  }
''', '')
replace('''  folder.finished = true;
  const context = folder.context;
  const opener = folder.opener;
  const chosen = folder.chosen;
  const target = folder.target;
  cancelCloudFiles();
''',
        '''  folder.finished = true;
  const opener = folder.opener;
  cancelCloudFiles();
''')
replace('''  if (context === 'setup') {
    const field = chosen && target === 'server' ? document.getElementById('f-dest') : null;
    const nextFocus = field || opener;
    if (nextFocus && nextFocus.isConnected && nextFocus.getClientRects().length) {
      focusSoon(nextFocus, opener || null);
    }
    return;
  }
  const targetFocus = opener && opener.isConnected && opener.getClientRects().length ? opener : $('#flow-start');
''',
        '''  const targetFocus = opener && opener.isConnected && opener.getClientRects().length ? opener : $('#flow-start');
''')
replace('''/* Пикер в мастере: фиксируем папку и закрываем диалог. */
function chooseSetupFolder() {
  if (folder.loading || folder.error || !folder.path || folder.writable === false) return;
  const input = document.getElementById('f-dest');
  if (input) {
    input.value = displayPath(folder.path);
    input.title = displayPath(folder.path);
    clearFieldError(input);
  }
  saveSetupFields();
  renderStorage();
  folder.chosen = true;
  const dialog = $('#folder-dialog');
  if (dialog && dialog.open) dialog.close();
  finishFolderDialog();
  announce(`Папка на сервере: ${displayPath(folder.path)}`);
}

''', '')

# ── 7. Dead account-cell nodes and missing DOM handlers ───────────────────
replace('''function renderAccountCell() {
  const login = $('#account-cell-login');
  if (!login) return;
  const account = currentAccount();
  if (!account) return;
  const access = accountAccess(account);
  if (access.tone === 'auth') cloudUsage = null;
  const method = $('#account-cell-method');
  if (method) method.textContent = accountMethodLabel(account);
  login.textContent = accountLabel(account);
  login.nextElementSibling.hidden = login.textContent === 'Яндекс Диск';
  const status = $('#account-cell-status');
  if (status) status.innerHTML = accessPill(access);
  const value = $('#account-cell-usage');
  if (value) {
    const text = cloudUsageShort(cloudUsage);
    value.textContent = text || '—';
    const stale = cloudUsageIsStale(cloudUsage);
    value.classList.toggle('is-stale', stale);
    value.title = cloudUsage ? usageText(cloudUsage) : '';
  }
  const note = $('#account-cell-note');
  if (note) {
    const stale = cloudUsageIsStale(cloudUsage);
    const text = access.tone === 'auth'
      ? 'Войдите заново, чтобы увидеть место'
      : (stale ? 'последние данные' : '');
    note.textContent = text;
    note.hidden = !text;
    note.classList.toggle('is-stale', stale);
  }
}
''',
        '''function renderAccountCell() {
  const login = $('#account-cell-login');
  if (!login) return;
  const account = currentAccount();
  if (!account) return;
  /* В карточке на главном только логин: способ входа, доступ и занятое
     место живут в панели аккаунта. */
  if (accountAccess(account).tone === 'auth') cloudUsage = null;
  login.textContent = accountLabel(account);
  login.nextElementSibling.hidden = login.textContent === 'Яндекс Диск';
}
''')
replace('''$('#account-reconnect')?.addEventListener('click', () => {
  closeAccountPanel(false);
  location.hash = '#/setup';
});
$('#account-logout')?.addEventListener('click', () => { openExitDialog(); });
''', '')
replace('''$('#home-view').addEventListener('click', async (event) => {
  const browse = event.target.closest('[data-browse]');
  if (browse && browse.dataset.browse) { openFolderDialog(browse.dataset.browse, browse); return; }

  const trigger = event.target.closest('button[data-action]');
''',
        '''$('#home-view').addEventListener('click', async (event) => {
  const trigger = event.target.closest('button[data-action]');
''')
replace('''  if (event.target.closest('[data-setup-browse]')) {
    openFolderDialog('server', event.target.closest('[data-setup-browse]'));
    return;
  }
''', '')

# ── 8. Branches for fields the backend never sends ────────────────────────
replace('''  if (code === 'auth_failed') {
    /* 401 от WebDAV: пароль приложения мог ещё не активироваться. */
    if (data.retryAfterHint === true) {
      return { field: 'password', message: 'Пароль приложения ещё не активировался — подождите 2–3 часа.' };
    }
    if (tokenMode) return { field: 'token', message: 'Токен не подошёл или истёк. Проверьте, что скопировали его целиком.' };
''',
        '''  if (code === 'auth_failed') {
    if (tokenMode) return { field: 'token', message: 'Токен не подошёл или истёк. Проверьте, что скопировали его целиком.' };
''')
replace('''      } else if (view.kind === 'nofit') {
        flowAnnounce(`Не хватает ${humanBytes(estimateShortage(view))}`);
      } else if (view.partial) {
        flowAnnounce(`Учтено ${formatFiles(view.files)} · ${humanBytes(view.bytes)}. Часть файлов не удалось оценить`);
      } else {
''',
        '''      } else if (view.kind === 'nofit') {
        flowAnnounce(`Не хватает ${humanBytes(estimateShortage(view))}`);
      } else {
''')
replace('''      files: 0,
      bytes: 0,
      free: 0,
      partial: false,
    };
  }
  if (cached.status === 'error') {
    return { kind: 'error', timeout: preflightTimeout(cached.error), files: 0, bytes: 0, free: 0, partial: false };
  }
''',
        '''      files: 0,
      bytes: 0,
      free: 0,
    };
  }
  if (cached.status === 'error') {
    return { kind: 'error', timeout: preflightTimeout(cached.error), files: 0, bytes: 0, free: 0 };
  }
''')
replace('''  /* Частичный результат: сервер сам сообщает, что часть файлов не оценена.
     Без этого флага объём считается полным. */
  const partial = data.partial === true || (Number(data.unknownFiles) || 0) > 0;
  return { kind: data.fits === false ? 'nofit' : 'ready', files, bytes, free, partial };
''',
        '''  return { kind: data.fits === false ? 'nofit' : 'ready', files, bytes, free };
''')
replace('''  if (view.kind === 'nofit') {
    html += `<span class="review-short">${icon('warning', 'review-short-icon')}Не хватает ${escapeHtml(humanBytes(estimateShortage(view)))}</span>`;
  } else if (view.partial) {
    html += `<span class="review-sub">Часть файлов не удалось оценить</span>
      <button class="btn btn--quiet btn--sm review-retry" type="button" id="review-retry">Повторить оценку</button>`;
  }
''',
        '''  if (view.kind === 'nofit') {
    html += `<span class="review-short">${icon('warning', 'review-short-icon')}Не хватает ${escapeHtml(humanBytes(estimateShortage(view)))}</span>`;
  }
''')

path.write_text(text)
print('app.js updated:', len(text), 'bytes')
