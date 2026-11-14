'use strict';

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => Array.from(document.querySelectorAll(selector));

/* --- vocabulary ----------------------------------------------------------- */

/* Job statuses: five labels, no synonyms. */
const JOB_STATUS = {
  queued: ['В очереди', 'status-pill--queued'],
  running: ['Идёт скачивание', 'status-pill--running'],
  verifying: ['Проверяем файлы', 'status-pill--running'],
  done: ['Готово', 'status-pill--ok'],
  failed: ['Ошибка', 'status-pill--failed'],
  interrupted: ['Пауза', 'status-pill--paused'],
  canceled: ['Пауза', 'status-pill--paused'],
};
const ACTIVE_STATUSES = ['running', 'verifying', 'queued'];
const PAUSED_STATUSES = ['failed', 'interrupted', 'canceled'];
const KNOWN_JOB_STATUSES = new Set(Object.keys(JOB_STATUS));

/* One icon set: 24 grid, stroke 1.6, round caps, currentColor. */
const ICON_PATHS = {
  cloud: '<path d="M7.2 18a4.2 4.2 0 0 1-.4-8.38A5.6 5.6 0 0 1 17.6 10.6 3.7 3.7 0 0 1 17 18H7.2Z"/>',
  transfer: '<path d="M12 4v10m0 0-3.6-3.6M12 14l3.6-3.6"/><path d="M5 19h14"/>',
  pause: '<path d="M9 7v10M15 7v10"/>',
  resume: '<path d="M9.5 6.6v10.8L18.6 12 9.5 6.6Z"/>',
  check: '<path d="m5.5 12.5 4.2 4.2 8.8-9.4"/>',
  warning: '<path d="M10.4 5.4 3.6 17.2A1.9 1.9 0 0 0 5.25 20h13.5a1.9 1.9 0 0 0 1.65-2.8L13.6 5.4a1.85 1.85 0 0 0-3.2 0Z"/><path d="M12 9.6v4M12 16.6h.01"/>',
  close: '<path d="m6.5 6.5 11 11m0-11-11 11"/>',
  clock: '<circle cx="12" cy="12" r="8.2"/><path d="M12 7.6V12l2.8 1.7"/>',
  key: '<circle cx="8" cy="12" r="3.4"/><path d="M11.4 12H20m-3 0v3m-3-3v2"/>',
  phone: '<rect x="7" y="3" width="10" height="18" rx="3"/><path d="M11 18h2"/>',
  code: '<path d="m9 8-4 4 4 4m6-8 4 4-4 4"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  folder: '<path d="M4 7a2 2 0 0 1 2-2h3.6l2 2H18a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V7Z"/>',
  storage: '<path d="M4 8c0-1.66 3.58-3 8-3s8 1.34 8 3-3.58 3-8 3-8-1.34-8-3Z"/><path d="M4 8v8c0 1.66 3.58 3 8 3s8-1.34 8-3V8"/>',
  chevron: '<path d="m6 9 6 6 6-6"/>',
  chevronUp: '<path d="m6 15 6-6 6 6"/>',
};

function icon(name, extraClass = '') {
  const path = ICON_PATHS[name] || '';
  return `<svg class="icon${extraClass ? ` ${extraClass}` : ''}" aria-hidden="true" focusable="false" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">${path}</svg>`;
}

/* --- state ---------------------------------------------------------------- */

let state = { accounts: [], jobs: [], downloads: '/downloads', roots: [] };
let system = { roots: [] };
let wizard = {
  step: 0,
  method: 'webdav',
  dest: '/downloads',
  accountID: '',
  fields: {},
  openedFromMenu: false,
};
let device = { id: null, timer: null, userCode: '', verificationUrl: '', expiresIn: 0 };
let preferredDest = '';
let homeMode = '';
let lastStatuses = null;
let suppressed = new Map();
let offlineShown = false;
let menuOpen = false;
let probeBusy = false;
let cloudUsage = null;
let cloudBusy = false;
let lastCloudAttempt = 0;
let exitBusy = false;
let transferBusy = false;
let setupBusy = false;
const jobActionBusy = new Set();
let focusStartAfterRender = false;
let pendingFocusKey = '';
let folder = {
  target: 'cloud',
  path: '',
  parent: null,
  roots: [],
  entries: [],
  free: null,
  total: null,
  writable: true,
  loading: false,
  error: null,
  opener: null,
};
let folderToken = 0;

/* Selected cloud folders (paths relative to the cloud root, sorted). An empty
   list means the whole disk. */
const MAX_SOURCE_FOLDERS = 20;
let cloudSelection = [];
let cloudSelectionAccount = '';

function folderWord(count) {
  const mod10 = count % 10;
  const mod100 = count % 100;
  if (mod10 === 1 && mod100 !== 11) return 'папка';
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return 'папки';
  return 'папок';
}

function pluralFolders(count) {
  return `${count}\u00A0${folderWord(count)}`;
}

/* --- helpers -------------------------------------------------------------- */

async function api(path, options = {}) {
  const response = await fetch(path, { headers: { 'Content-Type': 'application/json' }, ...options });
  const text = await response.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = { error: text }; }
  if (!response.ok) {
    const error = new Error((data && data.error) || `HTTP ${response.status}`);
    error.status = response.status;
    error.data = data;
    error.field = String((data && (data.field || data.param)) || '');
    throw error;
  }
  return data;
}

/* Один вежливый live-region для статусов: пилюли и шаги не объявляют себя сами. */
function announce(text) {
  const live = $('#live');
  if (!live) return;
  live.textContent = text || '';
}

const escapeHtml = (value) => String(value ?? '').replace(/[&<>"']/g, (c) => (
  { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
));
const cssEscape = (value) => (window.CSS && CSS.escape ? CSS.escape(String(value)) : String(value).replace(/["\\]/g, '\\$&'));

function humanBytes(value) {
  let n = Number(value) || 0;
  const units = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ'];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i += 1; }
  const number = n.toFixed(i === 0 ? 0 : 1).replace('.', ',').replace(/,0$/, '');
  return `${number}\u00A0${units[i]}`;
}
function humanEta(seconds) {
  seconds = Number(seconds) || 0;
  if (seconds <= 0) return '';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  return h > 0 ? `${h}\u00A0ч ${m}\u00A0мин` : m > 0 ? `${m}\u00A0мин` : `${Math.round(seconds)}\u00A0с`;
}
const WHEN_FORMAT = new Intl.DateTimeFormat('ru-RU', {
  day: 'numeric', month: 'long', hour: '2-digit', minute: '2-digit',
});
function formatWhen(value) {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime()) || date.getFullYear() < 2000) return '';
  const parts = WHEN_FORMAT.formatToParts(date);
  const pick = (type) => (parts.find((part) => part.type === type) || {}).value || '';
  const day = pick('day');
  const month = pick('month');
  const hour = pick('hour');
  const minute = pick('minute');
  if (!day || !month) return '';
  return `${day}\u00A0${month}${hour ? `, ${hour}:${minute}` : ''}`;
}
const cloudApiPath = (value) => String(value ?? '').trim().replace(/^\/+|\/+$/g, '');
/* Только отображение: API хранит свой формат, интерфейс всегда показывает «/». */
const displayPath = (value) => String(value ?? '').replace(/\\/g, '/');
const displayCloudPath = (value) => {
  const path = cloudApiPath(displayPath(value));
  return path ? `/${path}` : '';
};
const shortPath = (path) => {
  const parts = String(path || '').split('/').filter(Boolean);
  return parts.length ? parts[parts.length - 1] : '';
};
const currentAccount = () => (state.accounts.length ? state.accounts[state.accounts.length - 1] : null);
const statusOf = (job) => JOB_STATUS[job.status] || [job.status, ''];

function friendlyError(error) {
  const message = displayPath(String((error && error.message) || error || ''));
  return !message || /^HTTP \d+$/.test(message) ? 'Сервер не ответил. Попробуйте ещё раз.' : message;
}

function setBusy(button, busy) {
  if (!button) return;
  button.setAttribute('aria-busy', busy ? 'true' : 'false');
  button.disabled = !!busy;
}

/* Ошибка поля: aria-invalid + aria-describedby и текст под контролом.
   Снимается при вводе. Ошибка сервера помечает только поле из ответа. */
function fieldControlElement(target) {
  if (!target || !target.matches || !target.closest) return null;
  const selector = 'input, select, textarea, button';
  return target.matches(selector) ? target : target.closest(selector);
}

function fieldErrorNode(controlElement) {
  const control = controlElement && controlElement.closest ? controlElement.closest('.control') : null;
  if (!control) return null;
  const id = `${controlElement.id || controlElement.name || 'field'}-error`;
  let node = control.querySelector(`[data-field-error="${cssEscape(id)}"]`);
  if (!node) {
    node = document.createElement('p');
    node.className = 'control-error';
    node.id = id;
    node.dataset.fieldError = id;
    node.hidden = true;
    control.append(node);
  }
  return node;
}

function setFieldError(target, message) {
  const element = fieldControlElement(target);
  if (!element) return;
  const control = element.closest('.control');
  const node = fieldErrorNode(element);
  if (control) control.classList.add('has-error');
  element.setAttribute('aria-invalid', 'true');
  if (node) {
    node.textContent = message || '';
    node.hidden = !message;
    element.setAttribute('aria-describedby', node.id);
  }
}

function clearFieldError(target) {
  const element = fieldControlElement(target);
  if (!element) return;
  const control = element.closest('.control');
  if (control) control.classList.remove('has-error');
  element.removeAttribute('aria-invalid');
  const node = control ? control.querySelector(`[data-field-error="${cssEscape(`${element.id || element.name || 'field'}-error`)}"]`) : null;
  if (node) { node.textContent = ''; node.hidden = true; }
  element.removeAttribute('aria-describedby');
}

function clearFieldErrors(scope) {
  if (!scope) return;
  for (const element of scope.querySelectorAll('.control.has-error input, .control.has-error select, .control.has-error textarea, .control.has-error button')) {
    clearFieldError(element);
  }
}

/* Имя поля из ответа сервера → конкретный контрол в форме/мастере. */
function serverFieldTarget(scope, error) {
  const field = String((error && error.field) || '').trim();
  if (!field || !scope) return null;
  const named = scope.querySelector(`[name="${cssEscape(field)}"]`);
  if (named) return named;
  const byID = document.getElementById(`f-${field}`);
  if (byID && scope.contains(byID)) return byID;
  const map = {
    user: 'f-user', login: 'f-user', password: 'f-pass', pass: 'f-pass',
    token: 'f-token', client: 'f-client', clientId: 'f-client',
    secret: 'f-secret', clientSecret: 'f-secret',
    destPath: 'dest-path', dest: 'dest-path', path: 'dest-path',
    sourcePath: 'source-summary', sourcePaths: 'source-summary', source: 'source-summary',
  };
  const id = map[field];
  const target = id ? document.getElementById(id) : null;
  return target && scope.contains(target) ? target : null;
}

/* --- toasts --------------------------------------------------------------- */

const TOAST_ICONS = { ok: 'check', warn: 'warning', error: 'warning', info: 'cloud' };

function setToastMessage(item, text) {
  let element = item.querySelector('.toast-message');
  if (!element) {
    element = document.createElement('span');
    element.className = 'toast-message';
    item.querySelector('.toast-content').append(element);
  }
  element.textContent = text;
}

/* Успех скрывается сам через 6 с; пауза на hover и focus. Остальные ждут закрытия. */
const TOAST_TIMEOUT = 6000;

function showToast({ type = 'info', title, message = '', action = null, id = '', persistent } = {}) {
  const host = $('#toasts');
  if (!host) return null;
  if (id) {
    const existing = host.querySelector(`[data-toast-id="${cssEscape(id)}"]`);
    if (existing) return existing;
  }
  const keep = persistent !== undefined ? persistent : type !== 'ok';
  const item = document.createElement('div');
  item.className = `toast toast--${type}`;
  if (id) item.dataset.toastId = id;
  item.setAttribute('role', type === 'error' ? 'alert' : 'status');
  item.innerHTML = `
    <span class="toast-icon" aria-hidden="true">${icon(TOAST_ICONS[type] || 'cloud')}</span>
    <div class="toast-content">
      <span class="toast-title"></span>
      ${message ? '<span class="toast-message"></span>' : ''}
      ${action ? `<div class="toast-actions"><button class="btn ${action.variant === 'danger' ? 'btn--danger' : 'btn--secondary'} btn--sm" type="button">${escapeHtml(action.label)}</button></div>` : ''}
    </div>
    <button class="toast-close" type="button" aria-label="Скрыть уведомление">${icon('close')}</button>`;
  item.querySelector('.toast-title').textContent = title;
  if (message) setToastMessage(item, message);

  const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  let hideTimer = null;
  let deadline = 0;
  let remaining = 0;
  const close = (restoreFocus = false) => {
    if (hideTimer) { clearTimeout(hideTimer); hideTimer = null; }
    if (!item.isConnected) return;
    item.remove();
    if (restoreFocus && previousFocus && previousFocus !== document.body && previousFocus.isConnected) {
      previousFocus.focus({ preventScroll: true });
    }
  };
  const pause = () => {
    if (!hideTimer) return;
    clearTimeout(hideTimer);
    hideTimer = null;
    remaining = Math.max(0, deadline - Date.now());
  };
  const resume = () => {
    if (hideTimer || keep) return;
    if (remaining <= 0) { close(false); return; }
    deadline = Date.now() + remaining;
    hideTimer = setTimeout(() => close(false), remaining);
  };
  item.querySelector('.toast-close').addEventListener('click', () => close(true));

  const actionButton = item.querySelector('.toast-actions .btn');
  if (actionButton && action) {
    actionButton.addEventListener('click', async () => {
      setBusy(actionButton, true);
      try {
        await action.onClick();
        close(false);
      } catch (error) {
        setBusy(actionButton, false);
        setToastMessage(item, friendlyError(error));
      }
    });
  }

  if (!keep) {
    remaining = TOAST_TIMEOUT;
    deadline = Date.now() + remaining;
    hideTimer = setTimeout(() => close(false), remaining);
    item.addEventListener('pointerenter', pause);
    item.addEventListener('pointerleave', resume);
    item.addEventListener('focusin', pause);
    item.addEventListener('focusout', (event) => {
      if (!item.contains(event.relatedTarget)) resume();
    });
  }
  host.append(item);
  while (host.children.length > 4) host.firstElementChild.remove();
  return item;
}

function closeToast(id) {
  const item = id ? $(`#toasts [data-toast-id="${cssEscape(id)}"]`) : null;
  if (item) item.remove();
}

/* --- connection state ----------------------------------------------------- */

async function retryConnection() {
  try {
    await loadData();
    if ($('#app-view').hidden && $('#setup-view').hidden) route();
    else render();
    startPolling();
  } catch {
    setOffline(true);
  }
}

function setOffline(value) {
  const dot = $('#offline-dot');
  if (dot) dot.hidden = !value;
  if (value && !offlineShown) {
    offlineShown = true;
    announce('Нет связи с сервером');
    showToast({
      type: 'error', id: 'offline',
      title: 'Нет связи с сервером',
      message: 'Проверьте, что приложение запущено, и повторите попытку.',
      action: { label: 'Повторить', onClick: retryConnection },
    });
  }
  if (!value) {
    if (offlineShown) announce('Связь с сервером восстановлена');
    offlineShown = false;
    closeToast('offline');
  }
}

/* --- boot screen and first request ---------------------------------------- */

let pollTimer = null;

function showBoot() {
  const boot = $('#boot-view');
  if (boot) { boot.hidden = false; boot.setAttribute('aria-busy', 'true'); }
}

function hideBoot() {
  const boot = $('#boot-view');
  if (boot) { boot.hidden = true; boot.removeAttribute('aria-busy'); }
}

function showOffline() {
  hideBoot();
  const offline = $('#offline-view');
  if (offline) offline.hidden = false;
  announce('Нет связи с сервером');
}

function startPolling() {
  if (pollTimer) return;
  pollTimer = setInterval(tick, 2500);
}

async function bootAgain() {
  const offline = $('#offline-view');
  if (offline) offline.hidden = true;
  showBoot();
  try {
    await loadData();
    hideBoot();
    route();
    startPolling();
  } catch {
    showOffline();
  }
}

/* --- data ----------------------------------------------------------------- */

async function loadData() {
  const [snapshot, sys] = await Promise.all([api('/api/state'), api('/api/system').catch(() => null)]);
  state = snapshot || { accounts: [], jobs: [], downloads: '/downloads' };
  if (sys) system = sys;
  syncJobNotifications();
  setOffline(false);
}

async function tick() {
  try {
    await loadData();
    if ($('#app-view').hidden && $('#setup-view').hidden) { route(); return; }
    render();
  } catch {
    setOffline(true);
  }
}

/* --- routing -------------------------------------------------------------- */

function route() {
  let hash = location.hash || '#/';
  if (hash !== '#/' && hash !== '#/setup') {
    history.replaceState(null, '', '#/');
    hash = '#/';
  }
  hideBoot();
  const offline = $('#offline-view');
  if (offline) offline.hidden = true;
  if (hash === '#/setup') { showSetup(); return; }
  stopDevice(true);
  showApp();
  render();
}

function showApp() {
  $('#setup-view').hidden = true;
  $('#app-view').hidden = false;
  hideBoot();
}

function showSetup() {
  $('#app-view').hidden = true;
  $('#setup-view').hidden = false;
  hideBoot();
  const account = currentAccount();
  wizard = {
    step: 0,
    method: 'webdav',
    dest: preferredDest || state.downloads || '/downloads',
    accountID: '',
    fields: {},
    openedFromMenu: !!account,
    announcedStep: -1,
  };
  device = { id: null, timer: null, userCode: '', verificationUrl: '', expiresIn: 0 };
  homeMode = '';
  renderSetup();
}

/* --- setup wizard --------------------------------------------------------- */

const METHODS = [
  { id: 'webdav', icon: icon('key'), title: 'Пароль приложения' },
  { id: 'yandex-oauth', icon: icon('phone'), title: 'Войти через Яндекс' },
  { id: 'yandex-token', icon: icon('code'), title: 'Токен' },
];

function setupFieldValue(name) {
  return escapeHtml(wizard.fields[name] || '');
}

function methodFields() {
  if (wizard.method === 'webdav') {
    return `
      <div class="control">
        <label class="control-label" for="f-user">Логин</label>
        <input id="f-user" autocomplete="username" placeholder="Логин на Яндексе" value="${setupFieldValue('user')}">
      </div>
      <div class="control">
        <label class="control-label" for="f-pass">Пароль приложения</label>
        <input id="f-pass" type="password" placeholder="Из Яндекс ID" value="${setupFieldValue('pass')}">
      </div>
      <p class="note">Способ работает только на тарифе <strong>Яндекс 360</strong>. Создайте пароль в <a href="https://id.yandex.ru/security/app-passwords" target="_blank" rel="noreferrer">Яндекс ID → Безопасность → Пароли приложений</a>, тип «Файлы WebDAV». Пароль начнёт действовать через 2–3 часа — если вход не удался сразу, повторите позже.</p>`;
  }
  if (wizard.method === 'yandex-oauth') {
    const waiting = !!device.id;
    const expires = Number(device.expiresIn) > 0 ? Math.max(1, Math.round(Number(device.expiresIn) / 60)) : 10;
    const panel = waiting ? `
      <div class="device-panel" aria-live="polite">
        <span class="note">Код для входа</span>
        <div class="device-code" id="device-code">${escapeHtml(device.userCode)}</div>
        <p class="note">Откройте <a id="device-link" href="${escapeHtml(device.verificationUrl || 'https://oauth.yandex.ru/device')}" target="_blank" rel="noreferrer">oauth.yandex.ru/device</a> и введите код. Он действует ${expires}\u00A0мин.</p>
        <div class="device-wait">
          <span class="note" role="status">Ждём подтверждение…</span>
          <button class="btn btn--ghost btn--sm" type="button" id="device-cancel">Отменить</button>
        </div>
      </div>` : '';
    return `
      <p class="note">Нужно своё OAuth-приложение: создайте его на <a href="https://oauth.yandex.ru/" target="_blank" rel="noreferrer">oauth.yandex.ru</a> (тип «для авторизации пользователей»). В правах включите <code>cloud_api:disk.read</code> и <code>cloud_api:disk.info</code>, Redirect URI — <code>https://oauth.yandex.ru/verification_code</code>. ID и секрет приложения — в его карточке.</p>
      <div class="control">
        <label class="control-label" for="f-client">ID приложения</label>
        <input id="f-client" placeholder="ac39b43b…" value="${setupFieldValue('client')}"${waiting ? ' disabled' : ''}>
      </div>
      <div class="control">
        <label class="control-label" for="f-secret">Секрет приложения</label>
        <input id="f-secret" type="password" value="${setupFieldValue('secret')}"${waiting ? ' disabled' : ''}>
      </div>
      ${panel}`;
  }
  return `
    <div class="control">
      <label class="control-label" for="f-token">Токен (JSON)</label>
      <textarea id="f-token" rows="4" spellcheck="false" placeholder='{"access_token":"…","refresh_token":"…","expiry":"…"}'>${setupFieldValue('token')}</textarea>
    </div>
    <p class="note">Вставьте полный JSON из OAuth-ответа Яндекса, а не только <code>access_token</code>: в нём должны быть <code>access_token</code>, <code>refresh_token</code> и <code>expiry</code>. Без refresh_token доступ перестанет обновляться.</p>`;
}

const STEP_TITLES = ['Подключение', 'Папка на сервере', 'Готово'];

function setupStepBody() {
  if (wizard.step === 0) {
    const account = currentAccount();
    const notice = wizard.openedFromMenu && account
      ? `<p class="notice">Сейчас подключён «${escapeHtml(account.name)}». Новое подключение заменит его — останется один аккаунт.</p>`
      : '';
    return `
      <h1 class="setup-title" id="setup-title" tabindex="-1">${STEP_TITLES[0]}</h1>
      ${notice}
      <div class="choices" role="group" aria-label="Способ подключения">
        ${METHODS.map((method) => `
          <button type="button" class="choice ${wizard.method === method.id ? 'is-active' : ''}" data-method="${method.id}" aria-pressed="${wizard.method === method.id}">
            <span class="choice-icon" aria-hidden="true">${method.icon}</span>
            <span class="choice-title">${method.title}</span>
            <span class="choice-mark" aria-hidden="true"></span>
          </button>`).join('')}
      </div>
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
    <h1 class="setup-title" id="setup-title" tabindex="-1">${STEP_TITLES[2]}</h1>
    <div class="summary">
      <div class="summary-row"><span>Аккаунт</span><strong>Яндекс Диск</strong></div>
      <div class="summary-row"><span>Папка</span><strong title="${escapeHtml(displayPath(wizard.dest))}">${escapeHtml(displayPath(wizard.dest))}</strong></div>
    </div>`;
}

function renderSetup(options = {}) {
  const focusTitle = options.focusTitle !== false;
  $('#setup-step-text').textContent = `Шаг ${wizard.step + 1} из 3`;
  $('#setup-steps').innerHTML = STEP_TITLES.map((label, index) => {
    const done = index <= wizard.step ? 'is-done' : '';
    const current = index === wizard.step ? ' aria-current="step"' : '';
    return `<i data-label="${label}" class="${done}"${current}></i>`;
  }).join('');
  $('#setup-body').innerHTML = setupStepBody();
  $('#setup-error').textContent = '';
  const next = $('#setup-next');
  const back = $('#setup-back');
  setBusy(next, false);
  if (wizard.step === 0) {
    back.hidden = false;
    back.textContent = 'Отмена';
    next.textContent = 'Подключиться';
  } else if (wizard.step === 1) {
    back.hidden = false;
    back.textContent = 'Назад';
    next.textContent = 'Продолжить';
  } else {
    back.hidden = false;
    back.textContent = 'Изменить папку';
    next.textContent = 'Перейти к скачиванию';
  }
  if (device.id) setBusy(next, true);
  if (wizard.announcedStep !== wizard.step) {
    wizard.announcedStep = wizard.step;
    announce(`Шаг ${wizard.step + 1} из 3: ${STEP_TITLES[wizard.step]}`);
  }
  if (focusTitle) focusSetupTitle();
}

function focusSetupTitle() {
  const title = $('#setup-body .setup-title');
  if (!title) return;
  title.setAttribute('tabindex', '-1');
  title.focus({ preventScroll: true });
}

function setupError(message) { $('#setup-error').textContent = message || ''; }

function saveSetupFields() {
  const map = { 'f-user': 'user', 'f-pass': 'pass', 'f-client': 'client', 'f-secret': 'secret', 'f-token': 'token' };
  for (const [id, key] of Object.entries(map)) {
    const element = document.getElementById(id);
    if (element) wizard.fields[key] = element.value;
  }
  const dest = document.getElementById('f-dest');
  if (dest) wizard.dest = dest.value;
}

function chooseMethod(method) {
  if (!method || method === wizard.method) return;
  saveSetupFields();
  if (device.id) stopDevice(true);
  wizard.method = method;
  renderSetup({ focusTitle: false });
  const active = $(`#setup-body .choice[data-method="${method}"]`);
  if (active) active.focus({ preventScroll: true });
}

async function enforceSingleAccount(keepID) {
  const stale = state.accounts.filter((account) => account.id !== keepID);
  if (!stale.length) return;
  let failed = 0;
  for (const account of stale) {
    try { await api(`/api/accounts/${encodeURIComponent(account.id)}`, { method: 'DELETE' }); }
    catch { failed += 1; }
  }
  try { await loadData(); } catch { /* keep the state we have */ }
  if (failed) {
    showToast({
      type: 'warn', id: 'multi-account',
      title: 'Не удалось удалить старое облако',
      message: 'По нему, возможно, идёт скачивание. Приостановите его на главном экране и подключите облако ещё раз — тогда старое подключение удалится.',
    });
  }
}

async function finishConnect(account) {
  stopDevice(false);
  wizard.accountID = account.id;
  try { await loadData(); } catch { /* the wizard stays usable */ }
  await enforceSingleAccount(account.id);
  wizard.step = 1;
  renderSetup();
  showToast({ type: 'ok', title: 'Яндекс Диск подключён' });
  announce('Яндекс Диск подключён');
  lastCloudAttempt = 0;
  ensureCloudUsage(true);
}

async function connectCloud() {
  const scope = $('#setup-body');
  clearFieldErrors(scope);
  if (wizard.method === 'webdav') {
    const user = (wizard.fields.user || '').trim();
    const password = wizard.fields.pass || '';
    if (!user) { setFieldError($('#f-user'), 'Укажите логин.'); return; }
    if (!password) { setFieldError($('#f-pass'), 'Укажите пароль приложения.'); return; }
    const account = await api('/api/accounts', { method: 'POST', body: JSON.stringify({
      name: 'Яндекс Диск', provider: 'webdav', url: 'https://webdav.yandex.ru', user, password }) });
    await finishConnect(account);
    return;
  }
  const token = (wizard.fields.token || '').trim();
  if (!token) {
    setFieldError($('#f-token'), 'Вставьте JSON-токен.');
    return;
  }
  let parsedToken;
  try { parsedToken = JSON.parse(token); } catch {
    setFieldError($('#f-token'), 'Токен должен быть JSON-объектом, а не строкой.');
    return;
  }
  if (!parsedToken || typeof parsedToken !== 'object' || Array.isArray(parsedToken) || !parsedToken.access_token) {
    setFieldError($('#f-token'), 'В JSON нет поля access_token.');
    return;
  }
  if (!parsedToken.refresh_token || parsedToken.expiry == null || parsedToken.expiry === '') {
    setFieldError($('#f-token'), 'В JSON нужны access_token, refresh_token и expiry — вставьте полный ответ OAuth.');
    return;
  }
  const account = await api('/api/accounts', { method: 'POST', body: JSON.stringify({
    name: 'Яндекс Диск', provider: 'yandex-token', token }) });
  await finishConnect(account);
}

async function startDevice() {
  if (device.id) return;
  clearFieldErrors($('#setup-body'));
  const clientId = (wizard.fields.client || '').trim();
  const clientSecret = (wizard.fields.secret || '').trim();
  if (!clientId) { setFieldError($('#f-client'), 'Укажите ID приложения.'); return; }
  if (!clientSecret) { setFieldError($('#f-secret'), 'Укажите секрет приложения.'); return; }
  const next = $('#setup-next');
  setBusy(next, true);
  try {
    const data = await api('/api/oauth/yandex/device/start', { method: 'POST', body: JSON.stringify({
      name: 'Яндекс Диск', clientId, clientSecret }) });
    device = {
      id: data.flowId,
      timer: null,
      userCode: data.userCode || '',
      verificationUrl: data.verificationUrl || 'https://oauth.yandex.ru/device',
      expiresIn: Number(data.expiresIn) || 0,
    };
    renderSetup({ focusTitle: false });
    setBusy($('#setup-next'), true);
    announce(`Код для входа: ${device.userCode || ''}. Откройте oauth.yandex.ru/device и введите код.`);
    pollDevice(data.interval || 5);
  } catch (error) {
    setBusy(next, false);
    const target = serverFieldTarget($('#setup-body'), error);
    if (target) setFieldError(target, friendlyError(error));
    else setupError(friendlyError(error));
  }
}

function stopDevice(notifyServer) {
  clearTimeout(device.timer);
  const flow = device.id;
  device = { id: null, timer: null, userCode: '', verificationUrl: '', expiresIn: 0 };
  if (notifyServer && flow) api(`/api/oauth/yandex/device/${encodeURIComponent(flow)}`, { method: 'DELETE' }).catch(() => {});
}

function cancelDevice() {
  stopDevice(true);
  renderSetup({ focusTitle: false });
  announce('Вход по коду отменён');
  const next = $('#setup-next');
  if (next) next.focus({ preventScroll: true });
}

function pollDevice(seconds) {
  clearTimeout(device.timer);
  device.timer = setTimeout(async () => {
    if (!device.id) return;
    const flow = device.id;
    try {
      const data = await api(`/api/oauth/yandex/device/${encodeURIComponent(flow)}`);
      if (!device.id || flow !== device.id) return;
      if (data.status === 'pending') { pollDevice(5); return; }
      if (data.status === 'error') {
        stopDevice(false);
        renderSetup({ focusTitle: false });
        setupError(data.message || 'Вход не удался. Попробуйте ещё раз.');
        const next = $('#setup-next');
        if (next) next.focus({ preventScroll: true });
        return;
      }
      if (data.status === 'done') { await finishConnect(data.account); return; }
      if (data.status === 'canceled') { stopDevice(false); renderSetup({ focusTitle: false }); }
    } catch (error) {
      stopDevice(false);
      renderSetup({ focusTitle: false });
      setupError(friendlyError(error));
      const next = $('#setup-next');
      if (next) next.focus({ preventScroll: true });
    }
  }, seconds * 1000);
}

async function onSetupNext() {
  const next = $('#setup-next');
  if (next && next.disabled) return;
  setupError('');
  saveSetupFields();
  const scope = $('#setup-body');
  if (wizard.step === 0) {
    if (wizard.method === 'yandex-oauth') { await startDevice(); return; }
    const next = $('#setup-next');
    setBusy(next, true);
    setupBusy = true;
    try {
      await connectCloud();
    } catch (error) {
      const target = serverFieldTarget(scope, error);
      if (target) setFieldError(target, friendlyError(error));
      else setupError(friendlyError(error));
    } finally {
      setupBusy = false;
      if (wizard.step === 0) setBusy($('#setup-next'), false);
    }
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
  focusStartAfterRender = true;
  location.hash = '#/';
}

function onSetupBack() {
  if (setupBusy) return;
  setupError('');
  if (wizard.step === 0) {
    stopDevice(true);
    location.hash = '#/';
    return;
  }
  saveSetupFields();
  if (wizard.step === 1) { wizard.step = 0; renderSetup(); return; }
  wizard.step = 1;
  renderSetup();
}

/* --- header: account chip and menu ---------------------------------------- */

function renderHeader() {
  const account = currentAccount();
  const wrap = $('#account-wrap');
  if (!account) {
    if (wrap) wrap.hidden = true;
    if (menuOpen) closeMenu(false);
    return;
  }
  if (wrap) wrap.hidden = false;
  const name = $('#account-chip-name');
  if (name) name.textContent = account.name || 'Яндекс Диск';
  const chip = $('#account-chip');
  if (chip) chip.title = account.name || 'Яндекс Диск';
}

function openMenu(focus = 'first') {
  const menu = $('#account-menu');
  const chip = $('#account-chip');
  const wrap = $('#account-wrap');
  if (!menu || !chip || !wrap || wrap.hidden || menuOpen) return;
  menu.hidden = false;
  menuOpen = true;
  chip.setAttribute('aria-expanded', 'true');
  const items = $$('#account-menu .menu-item');
  if (!items.length) return;
  const index = focus === 'last' ? items.length - 1 : 0;
  setRovingTabindex(items, index);
  items[index].focus({ preventScroll: true });
}

/* Roving tabindex: Tab не перебирает все пункты меню, только текущий. */
function setRovingTabindex(items, active) {
  items.forEach((item, index) => { item.tabIndex = index === active ? 0 : -1; });
}

function closeMenu(restoreFocus = true) {
  const menu = $('#account-menu');
  const chip = $('#account-chip');
  if (menu) menu.hidden = true;
  if (chip) chip.setAttribute('aria-expanded', 'false');
  if (!menuOpen) return;
  menuOpen = false;
  if (restoreFocus && chip && chip.isConnected) chip.focus({ preventScroll: true });
}

function toggleMenu() {
  if (menuOpen) closeMenu(true); else openMenu();
}

function usageText(about) {
  if (!about) return '';
  const total = Number(about.total) || 0;
  const used = Number(about.used) || 0;
  if (total > 0) return `Занято ${humanBytes(used)} из ${humanBytes(total)}`;
  if (used > 0) return `Занято ${humanBytes(used)}`;
  return '';
}

async function probeFromMenu(item) {
  const account = currentAccount();
  if (!item || !account || probeBusy) return;
  probeBusy = true;
  item.setAttribute('aria-busy', 'true');
  try {
    const data = await api(`/api/accounts/${encodeURIComponent(account.id)}/probe`, { method: 'POST' });
    const about = data.about || {};
    if (Number(about.total) > 0 || Number(about.used) > 0) {
      cloudUsage = { used: Number(about.used) || 0, total: Number(about.total) || 0, checkedAt: Date.now() };
    }
    lastCloudAttempt = Date.now();
    if (data.warning) {
      showToast({ type: 'warn', title: 'Подключение работает', message: data.warning });
    } else {
      showToast({ type: 'ok', title: 'Подключение работает', message: usageText(about) });
    }
    await loadData();
    render();
  } catch (error) {
    let authExpired = false;
    try { await loadData(); } catch { /* ignore */ }
    const fresh = currentAccount();
    if (fresh && fresh.id === account.id && fresh.status === 'needs-auth') authExpired = true;
    if (authExpired) {
      showToast({
        type: 'warn', id: 'needs-auth',
        title: 'Нужно войти заново',
        message: 'Яндекс Диск не принял сохранённый доступ.',
        action: { label: 'Войти заново', onClick: () => { location.hash = '#/setup'; } },
      });
    } else {
      showToast({
        type: 'error',
        title: 'Не удалось проверить подключение',
        message: friendlyError(error),
        action: { label: 'Повторить', onClick: () => probeFromMenu($('#account-menu [data-menu="probe"]')) },
      });
    }
    render();
  } finally {
    probeBusy = false;
    if (item.isConnected) item.removeAttribute('aria-busy');
  }
}

async function reconnectFromMenu() {
  closeMenu(false);
  const active = activeJobs();
  if (active.length) {
    let paused = false;
    for (const job of active) {
      suppressStatus(job.id, ['interrupted', 'canceled']);
      try { await api(`/api/jobs/${encodeURIComponent(job.id)}/stop`, { method: 'POST' }); paused = true; }
      catch { /* keep going */ }
    }
    if (paused) {
      try { await loadData(); } catch { /* ignore */ }
      showToast({ type: 'warn', title: 'Скачивание приостановлено' });
    }
  }
  location.hash = '#/setup';
}

function openExitDialog() {
  closeMenu(false);
  const dialog = $('#exit-dialog');
  if (!dialog || dialog.open) return;
  const error = $('#exit-error');
  if (error) { error.hidden = true; error.textContent = ''; }
  dialog.showModal();
  const title = $('#exit-title');
  if (title) title.focus({ preventScroll: true });
}

async function confirmExit() {
  if (exitBusy) return;
  const dialog = $('#exit-dialog');
  const error = $('#exit-error');
  if (error) { error.hidden = true; error.textContent = ''; }
  const account = currentAccount();
  if (!account) { if (dialog && dialog.open) dialog.close(); return; }
  exitBusy = true;
  if (dialog) dialog.setAttribute('aria-busy', 'true');
  const button = $('#exit-confirm');
  setBusy(button, true);
  try {
    for (const job of activeJobs()) {
      suppressStatus(job.id, ['interrupted', 'canceled']);
      try { await api(`/api/jobs/${encodeURIComponent(job.id)}/stop`, { method: 'POST' }); }
      catch { /* the account delete below will report the real problem */ }
    }
    await api(`/api/accounts/${encodeURIComponent(account.id)}`, { method: 'DELETE' });
    if (dialog && dialog.open) dialog.close();
    cloudUsage = null;
    lastCloudAttempt = 0;
    await loadData();
    render();
    showToast({ type: 'ok', title: 'Вы вышли из Яндекс Диска', message: 'Подключить снова можно в любой момент.' });
    announce('Вы вышли из Яндекс Диска');
    const plus = $('.plus-tile');
    if (plus) plus.focus({ preventScroll: true });
  } catch (failed) {
    /* Ошибка открытого диалога — внутри диалога, не тостом под top layer. */
    if (error && dialog && dialog.open) {
      error.hidden = false;
      error.textContent = friendlyError(failed);
    } else {
      showToast({ type: 'error', title: 'Не удалось выйти из Яндекс Диска', message: friendlyError(failed) });
    }
  } finally {
    exitBusy = false;
    if (button && button.isConnected) setBusy(button, false);
    if (dialog) dialog.removeAttribute('aria-busy');
  }
}

/* --- storage informer ----------------------------------------------------- */

function serverRoot() {
  const roots = Array.isArray(system.roots) ? system.roots : [];
  const input = $('#dest-path');
  const dest = ((input && input.value) || preferredDest || state.downloads || '/downloads').trim();
  const match = roots.find((root) => {
    if (!root || !root.path) return false;
    const base = String(root.path).replace(/\/+$/, '');
    return dest === base || dest.startsWith(`${base}/`);
  });
  return match || roots[0] || null;
}

/* Cloud usage comes from GET /api/system (background cache, no per-render
   network); the probe is only a fallback for when that cache has no numbers. */
function systemCloudUsage() {
  const cloud = system && system.cloud;
  if (!cloud || cloud.status !== 'ok') return null;
  const total = cloud.total == null ? null : Number(cloud.total);
  const used = cloud.used == null ? null : Number(cloud.used);
  if (total == null && used == null) return null;
  return {
    used: Number.isFinite(used) ? used : 0,
    total: Number.isFinite(total) ? total : 0,
    stale: !!cloud.stale,
  };
}

function ensureCloudUsage(force = false) {
  const account = currentAccount();
  if (!account) return;
  if (!force) {
    const cached = systemCloudUsage();
    if (cached && (cached.total > 0 || cached.used > 0)) {
      cloudUsage = { used: cached.used, total: cached.total, checkedAt: Date.now(), fromSystem: true };
      return;
    }
    if (system && system.cloud && system.cloud.status === 'needs-auth') return;
  }
  if (cloudBusy) return;
  if (account.status === 'needs-auth') return;
  if (!force && Date.now() - lastCloudAttempt < 60000) return;
  if (!force && cloudUsage && Date.now() - cloudUsage.checkedAt < 60000) return;
  cloudBusy = true;
  lastCloudAttempt = Date.now();
  api(`/api/accounts/${encodeURIComponent(account.id)}/probe`, { method: 'POST' })
    .then((data) => {
      const about = data && data.about;
      if (about) {
        cloudUsage = {
          used: Number(about.used) || 0,
          total: Number(about.total) || 0,
          checkedAt: Date.now(),
          fromSystem: false,
        };
      }
    })
    .catch(() => { /* «—» in the informer, no toast for the silent check */ })
    .finally(() => {
      cloudBusy = false;
      renderStorage();
    });
}

function renderStorage() {
  const cloudValue = $('#cloud-usage');
  const cloudBar = $('#cloud-bar');
  const serverValue = $('#server-usage');
  const serverBar = $('#server-bar');
  if (!cloudValue || !serverValue) return;

  const account = currentAccount();
  const systemCloud = system && system.cloud;
  const needsAuth = !!account && (account.status === 'needs-auth' || (systemCloud && systemCloud.status === 'needs-auth'));
  if (needsAuth) {
    cloudUsage = null;
  }
  if (needsAuth) {
    cloudValue.textContent = 'Нужен вход';
    if (cloudBar) cloudBar.style.width = '0%';
  } else if (cloudUsage && (cloudUsage.total > 0 || cloudUsage.used > 0)) {
    cloudValue.textContent = cloudUsage.total > 0
      ? `Занято ${humanBytes(cloudUsage.used)} из ${humanBytes(cloudUsage.total)}`
      : `Занято ${humanBytes(cloudUsage.used)}`;
    if (cloudBar) {
      const percent = cloudUsage.total > 0 ? Math.min(100, Math.round((cloudUsage.used / cloudUsage.total) * 100)) : 0;
      cloudBar.style.width = `${percent}%`;
    }
  } else {
    cloudValue.textContent = '—';
    if (cloudBar) cloudBar.style.width = '0%';
  }

  const root = serverRoot();
  if (root && Number.isFinite(Number(root.free))) {
    const free = Number(root.free);
    const total = Number(root.total);
    serverValue.textContent = Number.isFinite(total) && total > 0
      ? `Свободно ${humanBytes(free)} из ${humanBytes(total)}`
      : `Свободно ${humanBytes(free)}`;
    if (serverBar) {
      const used = Number.isFinite(total) && total > 0 ? Math.max(0, total - free) : 0;
      const percent = Number.isFinite(total) && total > 0 ? Math.min(100, Math.round((used / total) * 100)) : 0;
      serverBar.style.width = `${percent}%`;
    }
  } else {
    serverValue.textContent = '—';
    if (serverBar) serverBar.style.width = '0%';
  }
}

/* --- cloud source selection ----------------------------------------------- */

/* Cloud folders of a job: the new sourcePaths list, or the path after the
   remote name for jobs recorded by older builds ("cf_x:/Фото"). */
function jobSourcePaths(job) {
  if (Array.isArray(job.sourcePaths) && job.sourcePaths.length) {
    return job.sourcePaths.map((value) => cloudApiPath(value)).filter(Boolean);
  }
  const source = String(job.source || '');
  const colon = source.indexOf(':');
  const raw = colon >= 0 ? source.slice(colon + 1) : source;
  const path = cloudApiPath(raw);
  return path ? [path] : [];
}

function sourceLabel(paths) {
  if (!paths.length) return 'Весь Диск';
  if (paths.length === 1) return displayCloudPath(paths[0]);
  return pluralFolders(paths.length);
}

/* One readable line for «Сейчас» and toasts: "3 папки: A, B → /downloads". */
function jobSourceText(job) {
  const paths = jobSourcePaths(job);
  const dest = displayPath(job.dest);
  if (paths.length > 1) {
    const names = paths.map((value) => displayPath(value)).join(', ');
    return `${pluralFolders(paths.length)}: ${names} → ${dest}`;
  }
  return `${sourceLabel(paths)} → ${dest}`;
}

function sourceSummaryText() {
  if (!cloudSelection.length) {
    return { value: 'Весь Диск', action: 'Выбрать', title: 'Скачаем весь Диск' };
  }
  if (cloudSelection.length === 1) {
    const path = displayCloudPath(cloudSelection[0]);
    return { value: path, action: 'Изменить', title: path };
  }
  return {
    value: pluralFolders(cloudSelection.length),
    action: 'Изменить',
    title: cloudSelection.map((value) => displayCloudPath(value)).join('\n'),
  };
}

function renderSourceSummary() {
  const button = $('#source-summary');
  const value = $('#source-value');
  const action = $('#source-action');
  if (!button || !value || !action) return;
  const summary = sourceSummaryText();
  value.textContent = summary.value;
  action.textContent = summary.action;
  button.title = summary.title;
  button.setAttribute('aria-label', `Из облака: ${summary.value}. ${summary.action}`);
}

/* Selection belongs to one account: a new/disconnected cloud starts clean. */
function syncSelectionAccount() {
  const account = currentAccount();
  if (!cloudSelection.length) {
    cloudSelectionAccount = account ? account.id : '';
    return;
  }
  if (!account || account.id !== cloudSelectionAccount) {
    cloudSelection = [];
    cloudSelectionAccount = account ? account.id : '';
  }
}

/* --- home: cells ---------------------------------------------------------- */

function activeJobs() {
  return state.jobs
    .filter((job) => ACTIVE_STATUSES.includes(job.status))
    .sort((a, b) => {
      const order = { running: 0, verifying: 0, queued: 1 };
      return (order[a.status] ?? 2) - (order[b.status] ?? 2);
    });
}

function latestJob() {
  return state.jobs.length ? state.jobs[0] : null;
}

function percentOf(job) {
  const stats = job.stats || {};
  if (job.status === 'done') return 100;
  if (stats.totalBytes > 0) return Math.min(100, Math.round((stats.bytes / stats.totalBytes) * 100));
  return 0;
}

function jobSourceLabel(job) {
  return sourceLabel(jobSourcePaths(job));
}
function jobTitle(job) { return `${jobSourceLabel(job)} → ${displayPath(job.dest)}`; }

function jobMeta(job) {
  if (job.status === 'queued') return 'Ожидает запуска';
  if (job.status === 'verifying') return 'Проверяем целостность';
  const stats = job.stats || {};
  const parts = [];
  if (Number(stats.speed) > 0) parts.push(`${humanBytes(stats.speed)}/с`);
  if (Number(stats.eta) > 0) parts.push(`≈ ${humanEta(stats.eta)}`);
  if (!parts.length) parts.push('Идёт скачивание');
  return parts.join(' · ');
}

/* Сумма в одной единице для пары «12,4 из 29,0 ГБ». */
function humanPair(used, total) {
  const units = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ'];
  let unit = 0;
  let base = Number(total) || 0;
  while (base >= 1024 && unit < units.length - 1) { base /= 1024; unit += 1; }
  const format = (value) => {
    const number = (Number(value) || 0) / (1024 ** unit);
    return number.toFixed(unit === 0 ? 0 : 1).replace('.', ',');
  };
  return `${format(used)} из ${format(total)}\u00A0${units[unit]}`;
}

/* «3 папки · 12,4 из 29,0 ГБ» — понятный объём в «Сейчас». */
function jobSizeText(job) {
  const label = jobSourceLabel(job);
  const stats = job.stats || {};
  const total = Number(stats.totalBytes) || 0;
  if (!(total > 0)) return label;
  const used = Number(stats.bytes) || 0;
  return `${label} · ${humanPair(used, total)}`;
}

function homeMarkup() {
  return `
    <section id="cell-now" class="cell cell--now" data-mode="idle" aria-labelledby="now-title"></section>

    <section class="cell cell--form" aria-labelledby="form-title">
      <div class="cell-head">
        ${icon('transfer')}
        <h2 class="cell-title" id="form-title">Новое скачивание</h2>
      </div>
      <form id="job-form" class="cell-body transfer-form" novalidate>
        <div class="field-stack">
          <div class="control control--source">
            <button type="button" class="source-summary" id="source-summary" data-browse="cloud">
              <span class="source-summary-icon" aria-hidden="true">${icon('cloud')}</span>
              <span class="source-summary-value" id="source-value">Весь Диск</span>
              <span class="source-summary-action" id="source-action">Выбрать</span>
              ${icon('chevron')}
            </button>
          </div>
          <div class="control">
            <div class="field-row">
              <div class="field-with-icon">
                <span class="field-icon" aria-hidden="true">${icon('folder')}</span>
                <input name="destPath" id="dest-path" aria-label="Папка на сервере" placeholder="Папка на сервере" title="${escapeHtml(displayPath(preferredDest || state.downloads || '/downloads'))}" value="${escapeHtml(displayPath(preferredDest || state.downloads || '/downloads'))}" required autocomplete="off" spellcheck="false">
              </div>
              <button class="btn btn--secondary btn--browse" type="button" data-browse="server" aria-label="Выбрать папку на сервере">Обзор</button>
            </div>
          </div>
        </div>
        <p class="form-error" id="job-error" role="alert" hidden></p>
        <div class="form-actions">
          <button class="btn btn--primary btn--start" type="submit" id="start-btn">${icon('transfer')}<span>Скачать</span></button>
        </div>
      </form>
    </section>

    <section class="cell cell--storage" aria-labelledby="storage-title">
      <div class="cell-head">
        ${icon('storage')}
        <h2 class="cell-title" id="storage-title">Хранилище</h2>
      </div>
      <div class="cell-body storage-list">
        <div class="storage-tile">
          <div class="storage-tile-head">
            <span class="storage-tile-icon" aria-hidden="true">${icon('cloud')}</span>
            <span class="storage-tile-label">Яндекс Диск</span>
          </div>
          <span class="storage-tile-value" id="cloud-usage">—</span>
          <span class="meter-bar" aria-hidden="true"><i id="cloud-bar"></i></span>
        </div>
        <div class="storage-tile">
          <div class="storage-tile-head">
            <span class="storage-tile-icon" aria-hidden="true">${icon('storage')}</span>
            <span class="storage-tile-label">Сервер Umbrel</span>
          </div>
          <span class="storage-tile-value" id="server-usage">—</span>
          <span class="meter-bar" aria-hidden="true"><i id="server-bar"></i></span>
        </div>
      </div>
    </section>`;
}

function emptyMarkup() {
  return `
    <section class="plus-cell">
      <button class="plus-tile" type="button" data-action="connect" aria-label="Подключить Яндекс Диск">
        <span class="plus-icon" aria-hidden="true">${icon('plus')}</span>
        <span class="plus-title">Подключить Яндекс Диск</span>
        <span class="plus-note">Понадобится аккаунт Яндекса</span>
      </button>
    </section>`;
}

function render() {
  const app = $('#app-view');
  if (!app || app.hidden) return;
  renderHeader();
  const account = currentAccount();
  const mode = account ? 'app' : 'empty';
  if (homeMode !== mode) {
    homeMode = mode;
    const view = $('#home-view');
    view.className = mode === 'app' ? 'dashboard' : 'dashboard dashboard--empty';
    view.innerHTML = mode === 'app' ? homeMarkup() : emptyMarkup();
  }
  if (mode === 'app') {
    syncSelectionAccount();
    renderSourceSummary();
    renderNow();
    ensureCloudUsage();
    renderStorage();
    if (focusStartAfterRender) {
      focusStartAfterRender = false;
      const button = $('#start-btn');
      if (button) button.focus({ preventScroll: true });
    }
  }
}

function nowKey(mode, job, activeCount, account) {
  return [mode, job ? job.id : '', job ? job.status : '', activeCount, account ? account.status : ''].join('|');
}

function nowHead(right = '') {
  return `
    <div class="cell-head">
      ${icon('clock')}
      <h2 class="cell-title" id="now-title">Сейчас</h2>
      <span class="now-status">${right}</span>
    </div>`;
}

function nowMarkup(mode, job, activeCount, account) {
  if (mode === 'active') {
    const percent = percentOf(job);
    const [label, pill] = statusOf(job);
    const live = job.status === 'running' || job.status === 'verifying';
    const counter = activeCount > 1 ? ` · ${activeCount} в работе` : '';
    /* Очередь тоже можно остановить; проверку целостности API прервать не даёт. */
    const canPause = job.status === 'running' || job.status === 'queued';
    const pause = canPause
      ? `<button class="btn btn--secondary" type="button" data-action="stop" data-id="${escapeHtml(job.id)}" data-focus-key="stop">${icon('pause')}Приостановить</button>`
      : '';
    return `
      ${nowHead(`<span class="status-pill ${pill}">${escapeHtml(label)}</span>`)}
      <div class="cell-body now-body">
        <div class="now-top-row">
          <span class="now-percent" id="now-percent">${percent}\u00A0%</span>
          ${pause}
        </div>
        <div class="progress now-progress${live ? ' progress--live' : ''}" id="now-progress" role="progressbar" aria-label="Прогресс скачивания" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${percent}" aria-valuetext="${percent}\u00A0%"><i id="now-bar" style="width:${percent}%"></i></div>
        <span class="now-meta" id="now-meta">${escapeHtml(jobMeta(job) + counter)}</span>
        <p class="now-facts" id="now-facts" title="${escapeHtml(jobSourceText(job))}">
          <span class="now-size" id="now-size">${escapeHtml(jobSizeText(job))}</span>
          <span class="now-dest" id="now-dest" title="${escapeHtml(displayPath(job.dest))}">→ ${escapeHtml(displayPath(job.dest))}</span>
        </p>
      </div>`;
  }

  if (mode === 'paused' || mode === 'error') {
    const failed = mode === 'error';
    const pill = failed ? 'status-pill--failed' : 'status-pill--paused';
    const pillLabel = failed ? 'Ошибка' : 'Пауза';
    const title = failed ? (job.errorTitle || 'Скачивание не завершено') : 'Скачивание приостановлено';
    const note = failed
      ? (job.errorAdvice || 'Проверьте место на сервере и повторите.')
      : 'Продолжите — скачанные файлы не будут скачаны снова.';
    const failureCount = Math.max(0, Number(job.failureCount) || 0);
    const failures = Array.isArray(job.failures) ? job.failures : [];
    const partial = failed && failureCount > 0 ? `
          <div class="now-failures">
            <span class="now-failures-text">Не скачались: ${failureCount}</span>
            ${failureCount > 0 ? `<button class="btn btn--secondary btn--sm" type="button" data-action="copy-failures" data-id="${escapeHtml(job.id)}" data-focus-key="copy-failures">Скопировать список</button>` : ''}
          </div>` : '';
    return `
      ${nowHead(`<span class="status-pill ${pill}">${pillLabel}</span>`)}
      <div class="cell-body">
        <div class="now-row">
          <div class="now-text">
            <p class="now-title">${escapeHtml(title)}</p>
            <p class="now-note">${escapeHtml(note)}</p>
          </div>
          <button class="btn btn--secondary" type="button" data-action="resume" data-id="${escapeHtml(job.id)}" data-focus-key="resume">${icon('resume')}${failed ? 'Повторить' : 'Продолжить'}</button>
        </div>${partial}
        <p class="now-source" title="${escapeHtml(jobSourceText(job))}">${escapeHtml(jobSourceText(job))}</p>
      </div>`;
  }

  if (mode === 'auth') {
    return `
      ${nowHead('<span class="status-pill status-pill--auth">Нужен вход</span>')}
      <div class="cell-body">
        <div class="now-row">
          <div class="now-text">
            <p class="now-title">Яндекс Диск просит войти заново</p>
            <p class="now-note">Доступ к облаку истёк.</p>
          </div>
          <button class="btn btn--secondary" type="button" data-action="reconnect">${icon('key')}Войти заново</button>
        </div>
      </div>`;
  }

  if (mode === 'done') {
    const when = formatWhen(job.finishedAt || job.startedAt || job.createdAt);
    return `
      ${nowHead('<span class="status-pill status-pill--ok">Готово</span>')}
      <div class="cell-body">
        ${when ? `<p class="now-note">Последнее: ${escapeHtml(when)}</p>` : ''}
        <p class="now-source" title="${escapeHtml(jobSourceText(job))}">${escapeHtml(jobSourceText(job))}</p>
      </div>`;
  }

  if (mode === 'unknown') {
    return `
      ${nowHead(`<span class="status-pill" title="${escapeHtml(job.status)}">Неизвестный статус</span>`)}
      <div class="cell-body">
        <div class="now-row">
          <div class="now-text">
            <p class="now-title">Неизвестное состояние скачивания</p>
            <p class="now-note">Возможно, приложение обновилось. Обновите данные, чтобы увидеть актуальный статус.</p>
          </div>
          <button class="btn btn--secondary" type="button" data-action="refresh" data-focus-key="refresh">Обновить</button>
        </div>
        <p class="now-source" title="${escapeHtml(jobSourceText(job))}">${escapeHtml(jobSourceText(job))}</p>
      </div>`;
  }

  return nowHead('<span class="cell-muted">Скачиваний ещё не было</span>');
}

function renderNow() {
  const cell = $('#cell-now');
  if (!cell) return;
  const account = currentAccount();
  const active = activeJobs();
  const latest = latestJob();
  let mode = 'idle';
  let job = latest;
  if (active.length) {
    mode = 'active';
    job = active[0];
  } else if (latest && PAUSED_STATUSES.includes(latest.status)) {
    mode = latest.status === 'failed' ? 'error' : 'paused';
  } else if (account && account.status === 'needs-auth') {
    mode = 'auth';
  } else if (latest && latest.status === 'done') {
    mode = 'done';
  } else if (latest && !KNOWN_JOB_STATUSES.has(latest.status)) {
    mode = 'unknown';
  }
  const key = nowKey(mode, job, active.length, account);
  if (cell.dataset.key === key) {
    if (mode === 'active') updateNowValues(job);
    return;
  }
  const focused = document.activeElement;
  const focusKey = (focused && cell.contains(focused) ? focused.dataset.focusKey || '' : '') || pendingFocusKey;
  pendingFocusKey = '';
  cell.dataset.key = key;
  cell.dataset.mode = mode;
  cell.innerHTML = nowMarkup(mode, job, active.length, account);
  if (focusKey) {
    const target = cell.querySelector(`[data-focus-key="${cssEscape(focusKey)}"]`)
      || cell.querySelector('button[data-action]');
    if (target) target.focus({ preventScroll: true });
  }
}

function updateNowValues(job) {
  const percent = percentOf(job);
  const percentElement = $('#now-percent');
  if (percentElement) percentElement.textContent = `${percent}\u00A0%`;
  const bar = $('#now-bar');
  if (bar) bar.style.width = `${percent}%`;
  const progress = $('#now-progress');
  if (progress) {
    progress.setAttribute('aria-valuenow', String(percent));
    progress.setAttribute('aria-valuetext', `${percent}\u00A0%`);
  }
  const meta = $('#now-meta');
  if (meta) {
    const counter = activeJobs().length > 1 ? ` · ${activeJobs().length} в работе` : '';
    meta.textContent = jobMeta(job) + counter;
  }
  const size = $('#now-size');
  if (size) size.textContent = jobSizeText(job);
  const dest = $('#now-dest');
  if (dest) {
    const path = displayPath(job.dest);
    dest.textContent = `→ ${path}`;
    dest.title = path;
  }
  const facts = $('#now-facts');
  if (facts) facts.title = jobSourceText(job);
}

/* --- job status notifications -------------------------------------------- */

function suppressStatus(id, statuses) {
  const set = new Set(statuses);
  suppressed.set(id, set);
  return set;
}

function notifyJobStatus(job) {
  const skip = suppressed.get(job.id);
  if (skip && skip.has(job.status)) {
    skip.delete(job.status);
    if (!skip.size) suppressed.delete(job.id);
    return;
  }
  if (job.status === 'running' || job.status === 'verifying' || job.status === 'queued') {
    announce(`${statusOf(job)[0]}: ${jobSourceLabel(job)}`);
    return;
  }
  if (job.status === 'done') {
    announce('Скачивание завершено');
    showToast({ type: 'ok', title: 'Скачивание завершено', message: `${jobSourceLabel(job)} · ${displayPath(job.dest)}` });
    return;
  }
  if (job.status === 'failed') {
    announce(job.errorTitle || 'Скачивание не завершено');
    showToast({
      type: 'error', id: `job-${job.id}`,
      title: job.errorTitle || 'Скачивание не завершено',
      message: `${jobSourceLabel(job)} · ${displayPath(job.dest)}. ${job.errorAdvice || 'Проверьте место на сервере и повторите.'}`,
      action: { label: 'Повторить', onClick: () => resumeJob(job.id, null) },
    });
    return;
  }
  if (job.status === 'interrupted' || job.status === 'canceled') {
    announce('Скачивание приостановлено');
    showToast({
      type: 'warn', id: `job-${job.id}`,
      title: 'Скачивание приостановлено',
      message: `${jobSourceLabel(job)} · ${displayPath(job.dest)}. Продолжите — скачанные файлы не будут скачаны снова.`,
      action: { label: 'Продолжить', onClick: () => resumeJob(job.id, null) },
    });
  }
}

function syncJobNotifications() {
  const next = new Map();
  for (const job of state.jobs) next.set(job.id, job.status);
  if (!lastStatuses) { lastStatuses = next; return; }
  for (const job of state.jobs) {
    if (!lastStatuses.has(job.id)) continue;
    if (lastStatuses.get(job.id) === job.status) continue;
    notifyJobStatus(job);
  }
  lastStatuses = next;
}

/* --- actions -------------------------------------------------------------- */

async function stopJob(id, trigger) {
  const busyKey = `stop:${id}`;
  if (jobActionBusy.has(busyKey)) return;
  jobActionBusy.add(busyKey);
  suppressStatus(id, ['interrupted', 'canceled']);
  setBusy(trigger, true);
  try {
    const data = await api(`/api/jobs/${encodeURIComponent(id)}/stop`, { method: 'POST' });
    const job = state.jobs.find((item) => item.id === id);
    pendingFocusKey = trigger && trigger.dataset ? (trigger.dataset.focusKey || '') : '';
    showToast({
      type: data.warning ? 'warn' : 'ok',
      title: data.warning ? 'Скачивание приостановлено не сразу' : 'Скачивание приостановлено',
      message: data.warning || (job ? `${jobSourceLabel(job)} · ${displayPath(job.dest)}` : ''),
    });
    await loadData();
    render();
  } catch (error) {
    const skip = suppressed.get(id);
    if (skip) skip.clear();
    throw error;
  } finally {
    pendingFocusKey = '';
    jobActionBusy.delete(busyKey);
    setBusy(trigger, false);
  }
}

async function resumeJob(id, trigger) {
  const busyKey = `resume:${id}`;
  if (jobActionBusy.has(busyKey)) return;
  jobActionBusy.add(busyKey);
  setBusy(trigger, true);
  try {
    await api(`/api/jobs/${encodeURIComponent(id)}/resume`, { method: 'POST' });
    const job = state.jobs.find((item) => item.id === id);
    pendingFocusKey = trigger && trigger.dataset ? (trigger.dataset.focusKey || '') : '';
    showToast({
      type: 'ok', title: 'Скачивание продолжено',
      message: job
        ? `${jobSourceLabel(job)} · ${displayPath(job.dest)}. Скачанные файлы не будут скачаны снова.`
        : 'Скачанные файлы не будут скачаны снова.',
    });
    await loadData();
    render();
  } finally {
    pendingFocusKey = '';
    jobActionBusy.delete(busyKey);
    setBusy(trigger, false);
  }
}

async function copyText(text) {
  if (navigator.clipboard && navigator.clipboard.writeText) {
    try { await navigator.clipboard.writeText(text); return; }
    catch { /* fall back to a selection copy */ }
  }
  const area = document.createElement('textarea');
  area.value = text;
  area.setAttribute('readonly', '');
  area.style.position = 'fixed';
  area.style.opacity = '0';
  document.body.append(area);
  area.select();
  const copied = document.execCommand && document.execCommand('copy');
  area.remove();
  if (!copied) throw new Error('copy failed');
}

async function copyFailures(id, trigger) {
  const job = state.jobs.find((item) => item.id === id);
  if (!job) return;
  const failures = Array.isArray(job.failures) ? job.failures.filter(Boolean) : [];
  const count = Math.max(0, Number(job.failureCount) || failures.length);
  const text = failures.length ? failures.map(displayPath).join('\n') : `Не скачались: ${count}`;
  setBusy(trigger, true);
  try {
    await copyText(text);
    showToast({
      type: 'ok', title: 'Список скопирован',
      message: failures.length ? `Не скачались: ${count}` : `Файлов в списке: ${count}`,
    });
  } catch {
    showToast({
      type: 'error', title: 'Не удалось скопировать',
      message: 'Попробуйте ещё раз.',
      action: { label: 'Повторить', onClick: () => copyFailures(id, null) },
    });
  } finally {
    if (trigger && trigger.isConnected) setBusy(trigger, false);
  }
}

async function startTransfer(form) {
  if (transferBusy) return;
  const button = $('#start-btn');
  const banner = $('#job-error');
  const account = currentAccount();
  if (!account) { location.hash = '#/setup'; return; }
  if (account.status === 'needs-auth') {
    if (banner) {
      banner.hidden = false;
      banner.textContent = 'Доступ к облаку истёк. Войдите заново и повторите скачивание.';
    }
    const reconnect = document.querySelector('[data-action="reconnect"]');
    if (reconnect) reconnect.focus({ preventScroll: true });
    announce('Доступ к облаку истёк. Войдите заново.');
    return;
  }
  clearFieldErrors(form);
  if (banner) { banner.hidden = true; banner.textContent = ''; }
  const data = new FormData(form);
  const destPath = String(data.get('destPath') || '').trim();
  if (!destPath) {
    setFieldError($('#dest-path'), 'Укажите папку на сервере.');
    const dest = $('#dest-path');
    if (dest) dest.focus({ preventScroll: true });
    return;
  }
  const sourcePaths = cloudSelection.slice();
  transferBusy = true;
  setBusy(button, true);
  form.setAttribute('aria-busy', 'true');
  try {
    await api('/api/jobs', { method: 'POST', body: JSON.stringify({
      accountId: account.id,
      sourcePaths,
      destPath,
      /* Интерфейс всегда только добавляет и обновляет файлы: mode=copy. */
      mode: 'copy',
    }) });
    cloudSelection = [];
    cloudSelectionAccount = account.id;
    renderSourceSummary();
    showToast({
      type: 'ok', title: 'Скачивание началось',
      message: `${sourceLabel(sourcePaths)} → ${displayPath(destPath)}`,
    });
    await loadData();
    render();
    const next = $('#start-btn');
    if (next) next.focus({ preventScroll: true });
  } catch (failed) {
    /* Ошибка сервера помечает только поле из ответа; общий сбой — баннер. */
    const target = serverFieldTarget(form, failed);
    if (target) {
      setFieldError(target, friendlyError(failed));
      target.focus({ preventScroll: true });
    } else if (banner) {
      banner.hidden = false;
      banner.textContent = friendlyError(failed);
    } else {
      showToast({ type: 'error', title: 'Не удалось начать скачивание', message: friendlyError(failed) });
    }
    announce(friendlyError(failed));
  } finally {
    transferBusy = false;
    if (button && button.isConnected) setBusy(button, false);
    if (form.isConnected) form.removeAttribute('aria-busy');
  }
}

/* --- folder picker -------------------------------------------------------- */

function folderRootPath() {
  if (folder.target === 'cloud') return '';
  return folder.roots && folder.roots[0] ? String(folder.roots[0]) : (state.downloads || '/downloads');
}

function folderSegments() {
  if (folder.target === 'cloud') return [];
  const root = folderRootPath().replace(/\/+$/, '');
  if (folder.path === root) return [];
  return String(folder.path).slice(root.length).replace(/^\/+/, '').split('/').filter(Boolean);
}

function folderPathForIndex(index) {
  const segments = folderSegments();
  if (folder.target === 'cloud') return segments.slice(0, index + 1).join('/');
  const root = folderRootPath().replace(/\/+$/, '');
  return `${root}/${segments.slice(0, index + 1).join('/')}`;
}

function folderBusy() {
  if (folder.target === 'cloud') {
    const root = folder.nodes.get('');
    return !root || root.loading;
  }
  return folder.loading;
}

function normalizeCloudDirs(list, base) {
  const dirs = [];
  for (const entry of Array.isArray(list) ? list : []) {
    if (!entry || (!entry.name && !entry.path)) continue;
    const name = String(entry.name || shortPath(entry.path) || 'Папка');
    const raw = entry.path != null && entry.path !== ''
      ? entry.path
      : (base ? `${base}/${name}` : name);
    const cloudPath = cloudApiPath(raw);
    if (cloudPath) dirs.push({ name, path: cloudPath });
  }
  return dirs;
}

function updateFolderChrome() {
  const cloud = folder.target === 'cloud';
  const title = $('#folder-title');
  const note = $('#folder-note');
  if (title) title.textContent = cloud ? 'Выберите папки' : 'Папка на сервере';
  if (note) {
    note.textContent = cloud
      ? 'Отметьте папки, которые нужно скачать.'
      : 'Сюда будут скачиваться файлы.';
  }
  const crumbs = $('#folder-crumbs');
  if (crumbs) crumbs.hidden = cloud;
  const free = $('#folder-free');
  if (free) free.hidden = cloud;
  const choose = $('#folder-choose');
  if (choose) choose.textContent = cloud ? 'Сохранить' : 'Выбрать папку';
  renderFolderFooter();
}

function openFolderDialog(target, opener) {
  const account = currentAccount();
  if (target === 'cloud' && !account) return;
  if (target === 'cloud') clearFieldError($('#source-summary'));
  if (target === 'cloud' && account && account.status === 'needs-auth') {
    showToast({
      type: 'warn', title: 'Нужно войти заново',
      message: 'Доступ к облаку истёк — выберите папки после входа.',
      action: { label: 'Войти заново', onClick: () => { location.hash = '#/setup'; } },
    });
    return;
  }
  const active = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const destInput = $('#dest-path');
  const setupDest = document.getElementById('f-dest');
  const initialDest = ((destInput && destInput.value.trim())
    || (setupDest && setupDest.value.trim())
    || state.downloads
    || '/downloads');
  folder = {
    target,
    path: target === 'cloud' ? '' : (initialDest.startsWith('/') ? initialDest : `/${initialDest}`),
    parent: null,
    roots: [],
    entries: [],
    free: null,
    total: null,
    writable: true,
    loading: target !== 'cloud',
    error: null,
    chosen: false,
    opener: opener || active,
    selected: target === 'cloud' ? cloudSelection.slice() : [],
    expanded: new Set(),
    nodes: new Map(),
    limitError: '',
    finished: false,
  };
  folderToken += 1;
  updateFolderChrome();
  renderFolderDialog();
  const dialog = $('#folder-dialog');
  if (dialog && !dialog.open) dialog.showModal();
  const title = $('#folder-title');
  if (title) title.focus({ preventScroll: true });
  if (target === 'cloud') loadCloudNode('');
  else loadFolder(folder.path, false);
}

function renderFolderCrumbs() {
  const crumbs = $('#folder-crumbs');
  if (!crumbs) return;
  if (folder.target === 'cloud') {
    crumbs.innerHTML = '';
    return;
  }
  const root = folderRootPath();
  const segments = folderSegments();
  const atRoot = folder.path === root;
  let html = `<button class="crumb-up" type="button" data-folder-up aria-label="На уровень выше"${atRoot ? ' hidden' : ''}>${icon('chevronUp')}</button>`;
  const rootLabel = displayPath(root);
  html += `<button class="crumb" type="button" data-folder-path="${escapeHtml(root)}" title="${escapeHtml(rootLabel)}">${escapeHtml(rootLabel)}</button>`;
  segments.forEach((segment, index) => {
    html += '<span class="crumb-sep" aria-hidden="true">/</span>';
    const segmentLabel = displayPath(segment);
    if (index === segments.length - 1) {
      html += `<span class="crumb" aria-current="page" title="${escapeHtml(segmentLabel)}">${escapeHtml(segmentLabel)}</span>`;
    } else {
      html += `<button class="crumb" type="button" data-folder-path="${escapeHtml(folderPathForIndex(index))}" title="${escapeHtml(displayPath(folderPathForIndex(index)))}">${escapeHtml(segmentLabel)}</button>`;
    }
  });
  crumbs.innerHTML = html;
}

function folderFocusSelector(element) {
  if (!element || !element.matches) return '';
  if (element.matches('.folder-check')) return `.folder-check[data-path="${cssEscape(element.dataset.path || '')}"]`;
  if (element.matches('.folder-expand')) return `.folder-expand[data-expand="${cssEscape(element.dataset.expand || '')}"]`;
  if (element.matches('[data-retry-node]')) return `[data-retry-node="${cssEscape(element.dataset.retryNode || '')}"]`;
  if (element.matches('[data-folder-retry]')) return '[data-folder-retry]';
  if (element.matches('.folder-row')) return `.folder-row[data-folder-path="${cssEscape(element.dataset.folderPath || '')}"]`;
  return '';
}

function renderFolderDialog() {
  renderFolderCrumbs();
  const body = $('#folder-body');
  if (body) {
    const focused = document.activeElement;
    const focusSelector = focused && body.contains(focused) ? folderFocusSelector(focused) : '';
    if (folder.target === 'cloud') renderCloudBody(body);
    else renderServerBody(body);
    if (focusSelector) {
      const target = body.querySelector(focusSelector);
      if (target && !target.disabled) target.focus({ preventScroll: true });
    }
  }
  const dialog = $('#folder-dialog');
  if (dialog) {
    if (folderBusy()) dialog.setAttribute('aria-busy', 'true');
    else dialog.removeAttribute('aria-busy');
  }
  renderFolderFooter();
}

function renderServerBody(body) {
  if (folder.loading) {
    body.innerHTML = '<p class="folder-state" aria-busy="true">Загружаем папки…</p>';
    return;
  }
  if (folder.error) {
    const notFound = folder.error.status === 404;
    body.innerHTML = `
      <div class="folder-state folder-state--error">
        <p>Не удалось открыть папку.</p>
        ${notFound ? '<p class="folder-detail">Выбор папок недоступен — введите путь вручную.</p>' : ''}
        <button class="btn btn--secondary btn--sm" type="button" data-folder-retry>Повторить</button>
      </div>`;
    return;
  }
  if (!folder.entries.length) {
    body.innerHTML = '<p class="folder-state">Здесь нет подпапок.</p>';
    return;
  }
  body.innerHTML = `<div class="folder-list" role="list">${folder.entries.map((entry) => {
    /* В путь идёт исходное имя, в интерфейс — нормализованное для отображения. */
    const rawName = String(entry.name || shortPath(entry.path) || 'Папка');
    const path = normFolderEntryPath(entry.path, rawName);
    return `<button class="folder-row" type="button" role="listitem" data-folder-path="${escapeHtml(path)}">
      ${icon('folder')}
      <span class="folder-row-name" title="${escapeHtml(displayPath(path))}">${escapeHtml(displayPath(rawName))}</span>
      ${icon('chevron')}
    </button>`;
  }).join('')}</div>`;
}

function renderCloudBody(body) {
  const root = folder.nodes.get('');
  if (!root || root.loading) {
    body.innerHTML = '<p class="folder-state" aria-busy="true">Загружаем папки…</p>';
    return;
  }
  if (root.error) {
    const notFound = root.error.status === 404;
    body.innerHTML = `
      <div class="folder-state folder-state--error">
        <p>Не удалось открыть папки.</p>
        ${notFound ? '<p class="folder-detail">Похоже, облако отключено. Проверьте подключение и повторите.</p>' : ''}
        <button class="btn btn--secondary btn--sm" type="button" data-folder-retry>Повторить</button>
      </div>`;
    return;
  }
  if (!root.dirs.length) {
    body.innerHTML = '<p class="folder-state">Здесь нет подпапок.</p>';
    return;
  }
  let html = `<div class="folder-tree" role="list">${root.dirs.map((entry) => cloudNodeMarkup(entry.path, 0)).join('')}</div>`;
  const truncated = Array.from(folder.nodes.values()).some((node) => node && node.truncated);
  if (truncated) html += '<p class="folder-truncated">Показаны первые 500 папок.</p>';
  body.innerHTML = html;
  for (const checkbox of body.querySelectorAll('.folder-check')) {
    const path = checkbox.dataset.path || '';
    checkbox.indeterminate = !checkbox.checked
      && !checkbox.disabled
      && folder.selected.some((selected) => selected.startsWith(path + '/'));
  }
}

function cloudNodeMarkup(path, depth) {
  const name = displayPath(shortPath(path) || path);
  const selected = folder.selected.includes(path);
  const covered = !selected && folder.selected.some((ancestor) => path.startsWith(ancestor + '/'));
  const expanded = folder.expanded.has(path);
  const node = folder.nodes.get(path);
  const canExpand = !node || !node.loaded || (node.dirs && node.dirs.length > 0);
  let html = `<div class="folder-node" style="--depth:${depth}" role="listitem">
    <label class="folder-node-main">
      <input class="folder-check" type="checkbox" data-path="${escapeHtml(path)}"${selected ? ' checked' : ''}${covered ? ' disabled' : ''} aria-label="Скачать папку «${escapeHtml(name)}»">
      <span class="folder-check-mark" aria-hidden="true"></span>
      <span class="folder-row-name" title="${escapeHtml(displayPath(path))}">${escapeHtml(name)}</span>
    </label>
    ${canExpand || expanded ? `<button class="folder-expand" type="button" data-expand="${escapeHtml(path)}" aria-expanded="${expanded}" aria-label="${expanded ? 'Скрыть' : 'Показать'} папки внутри «${escapeHtml(name)}»">${icon('chevron')}</button>` : ''}
  </div>`;
  if (!expanded) return html;
  if (!node || node.loading) {
    return html + `<p class="folder-node-state" style="--depth:${depth + 1}">Загружаем папки…</p>`;
  }
  if (node.error) {
    return html + `<div class="folder-node-state" style="--depth:${depth + 1}"><span>Не удалось открыть папки.</span><button class="btn btn--ghost btn--sm" type="button" data-retry-node="${escapeHtml(path)}">Повторить</button></div>`;
  }
  if (!node.dirs.length) {
    return html + `<p class="folder-node-state" style="--depth:${depth + 1}">Здесь нет подпапок.</p>`;
  }
  return html + node.dirs.map((entry) => cloudNodeMarkup(entry.path, depth + 1)).join('');
}

function renderFolderFooter() {
  const cloud = folder.target === 'cloud';
  const chips = $('#folder-chips');
  if (chips) {
    chips.hidden = !cloud || !folder.selected.length;
    if (cloud && folder.selected.length) {
      chips.innerHTML = folder.selected.map((path) => `
        <span class="folder-chip" role="listitem">
          <span class="folder-chip-name" title="${escapeHtml(displayCloudPath(path))}">${escapeHtml(displayCloudPath(path))}</span>
          <button class="folder-chip-remove" type="button" data-remove="${escapeHtml(path)}" aria-label="Убрать «${escapeHtml(displayCloudPath(path))}»">${icon('close')}</button>
        </span>`).join('');
    }
  }
  const count = $('#folder-count');
  if (count) {
    count.hidden = !cloud;
    if (cloud) {
      count.textContent = folder.selected.length
        ? `Выбрано: ${pluralFolders(folder.selected.length)}`
        : 'Ничего не выбрано — скачаем весь Диск';
    }
  }
  const limit = $('#folder-limit');
  if (limit) {
    limit.hidden = !folder.limitError;
    limit.textContent = folder.limitError || '';
  }
  const choose = $('#folder-choose');
  if (choose) {
    choose.disabled = cloud ? folderBusy() : (folder.loading || !!folder.error);
  }
  const free = $('#folder-free');
  if (free && !cloud) {
    if (Number.isFinite(Number(folder.free))) {
      const total = Number(folder.total);
      free.hidden = false;
      free.textContent = Number.isFinite(total) && total > 0
        ? `Свободно ${humanBytes(folder.free)} из ${humanBytes(total)}`
        : `Свободно ${humanBytes(folder.free)}`;
    } else {
      free.hidden = true;
      free.textContent = '';
    }
  }
}

async function loadCloudNode(path) {
  const token = folderToken;
  const previous = folder.nodes.get(path) || {};
  folder.nodes.set(path, { ...previous, loading: true, error: null });
  renderFolderDialog();
  const account = currentAccount();
  try {
    if (!account) throw Object.assign(new Error('Подключение не найдено.'), { status: 404 });
    const data = await api(`/api/accounts/${encodeURIComponent(account.id)}/folders?path=${encodeURIComponent(path)}`);
    if (token !== folderToken) return;
    folder.nodes.set(path, {
      loading: false,
      loaded: true,
      error: null,
      dirs: normalizeCloudDirs(data.dirs || data.entries, path),
      truncated: !!data.truncated,
    });
  } catch (error) {
    if (token !== folderToken) return;
    folder.nodes.set(path, { loading: false, loaded: false, error, dirs: [] });
  }
  renderFolderDialog();
}

function toggleCloudNode(path) {
  if (folder.expanded.has(path)) {
    folder.expanded.delete(path);
    renderFolderDialog();
    return;
  }
  folder.expanded.add(path);
  const node = folder.nodes.get(path);
  if (!node || !node.loaded || node.error) loadCloudNode(path);
  else renderFolderDialog();
}

function toggleCloudSelection(path, checked) {
  if (!path) return;
  if (checked) {
    if (folder.selected.some((ancestor) => path.startsWith(ancestor + '/'))) {
      renderFolderDialog();
      return;
    }
    const rest = folder.selected.filter((selected) => !selected.startsWith(path + '/'));
    if (rest.length + 1 > MAX_SOURCE_FOLDERS) {
      folder.limitError = `Можно выбрать не больше ${MAX_SOURCE_FOLDERS} папок за один раз.`;
      renderFolderDialog();
      return;
    }
    folder.limitError = '';
    folder.selected = [...rest, path];
  } else {
    folder.selected = folder.selected.filter((selected) => selected !== path);
    folder.limitError = '';
  }
  renderFolderDialog();
}

function normFolderEntryPath(entryPath, name) {
  if (!entryPath) {
    if (folder.target === 'cloud') return folder.path ? `${folder.path}/${name}` : name;
    return `${folderRootPath().replace(/\/+$/, '')}/${name}`;
  }
  if (folder.target === 'cloud') return cloudApiPath(entryPath);
  return String(entryPath);
}

async function loadFolder(path, focusAfter) {
  if (folder.target === 'cloud') return;
  const token = ++folderToken;
  folder.path = path;
  folder.loading = true;
  folder.error = null;
  renderFolderDialog();
  try {
    const data = await api(`/api/fs/folders?path=${encodeURIComponent(path)}`);
    if (token !== folderToken) return;
    folder.path = String(data.path != null ? data.path : path);
    folder.parent = data.parent != null ? String(data.parent) : null;
    folder.roots = Array.isArray(data.roots) ? data.roots.map(String) : [];
    folder.entries = Array.isArray(data.entries)
      ? data.entries.filter((entry) => entry && (entry.name || entry.path))
      : [];
    folder.free = data.free != null && Number.isFinite(Number(data.free)) ? Number(data.free) : null;
    folder.total = data.total != null && Number.isFinite(Number(data.total)) ? Number(data.total) : null;
    folder.writable = data.writable !== false;
    folder.error = null;
  } catch (error) {
    if (token !== folderToken) return;
    folder.error = error;
  } finally {
    if (token === folderToken) {
      folder.loading = false;
      renderFolderDialog();
      if (focusAfter) focusFolderContent();
    }
  }
}

function focusFolderContent() {
  if (folderBusy()) return;
  const target = document.querySelector('#folder-body .folder-row')
    || document.querySelector('#folder-body .folder-check')
    || document.querySelector('#folder-body [data-folder-retry]')
    || document.getElementById('folder-choose')
    || document.getElementById('folder-dialog');
  if (target && typeof target.focus === 'function' && !target.disabled) target.focus({ preventScroll: true });
}

function chooseFolder() {
  if (folder.target === 'cloud') {
    if (folderBusy()) return;
    cloudSelection = folder.selected.slice().sort();
    cloudSelectionAccount = currentAccount() ? currentAccount().id : '';
    folder.chosen = true;
    const summary = $('#source-summary');
    if (summary) clearFieldError(summary);
    renderSourceSummary();
    announce(`Из облака: ${sourceSummaryText().value}`);
    const dialog = $('#folder-dialog');
    if (dialog && dialog.open) dialog.close();
    finishFolderDialog();
    return;
  }
  if (folder.loading || folder.error) return;
  const input = document.getElementById('f-dest') || $('#dest-path');
  if (input) {
    input.value = displayPath(folder.path);
    input.title = displayPath(folder.path);
    clearFieldError(input);
  }
  saveSetupFields();
  renderStorage();
  folder.chosen = true;
  announce(`Папка на сервере: ${displayPath(folder.path)}`);
  const dialog = $('#folder-dialog');
  if (dialog && dialog.open) dialog.close();
  finishFolderDialog();
}

function folderNavigate(path) {
  if (folder.target !== 'server' || folder.loading) return;
  loadFolder(path, true);
}

/* --- events --------------------------------------------------------------- */

$('#home-view').addEventListener('click', async (event) => {
  const browse = event.target.closest('[data-browse]');
  if (browse && browse.dataset.browse) { openFolderDialog(browse.dataset.browse, browse); return; }

  const trigger = event.target.closest('button[data-action]');
  if (!trigger) return;
  const action = trigger.dataset.action;
  const id = trigger.dataset.id || '';

  if (action === 'connect' || action === 'reconnect') {
    location.hash = '#/setup';
    return;
  }

  try {
    if (action === 'copy-failures') { await copyFailures(id, trigger); return; }
    if (action === 'refresh') {
      setBusy(trigger, true);
      pendingFocusKey = trigger.dataset ? (trigger.dataset.focusKey || '') : '';
      try {
        await loadData();
        render();
        announce('Данные обновлены');
      } catch (error) {
        showToast({
          type: 'error', title: 'Не удалось обновить данные', message: friendlyError(error),
          action: { label: 'Повторить', onClick: () => {
            const current = $('#cell-now [data-action="refresh"]');
            if (current) current.click();
          } },
        });
      } finally {
        pendingFocusKey = '';
        if (trigger.isConnected) setBusy(trigger, false);
      }
      return;
    }
    if (action === 'stop') await stopJob(id, trigger);
    else if (action === 'resume') await resumeJob(id, trigger);
  } catch (error) {
    const retry = action === 'stop'
      ? () => stopJob(id, null)
      : () => resumeJob(id, null);
    showToast({
      type: 'error',
      title: action === 'stop' ? 'Не удалось приостановить скачивание' : 'Не удалось продолжить скачивание',
      message: friendlyError(error),
      action: { label: 'Повторить', onClick: retry },
    });
  }
});

$('#home-view').addEventListener('submit', (event) => {
  const form = event.target.closest('#job-form');
  if (!form) return;
  event.preventDefault();
  startTransfer(form);
});

$('#home-view').addEventListener('input', (event) => {
  clearFieldError(event.target);
  const error = $('#job-error');
  if (error) { error.hidden = true; error.textContent = ''; }
  if (event.target && event.target.id === 'dest-path') {
    event.target.title = displayPath(event.target.value);
    renderStorage();
  }
});
$('#home-view').addEventListener('change', (event) => {
  clearFieldError(event.target);
  if (event.target && event.target.id === 'dest-path') renderStorage();
});

$('#account-chip')?.addEventListener('click', () => toggleMenu());

$('#account-chip')?.addEventListener('keydown', (event) => {
  if (event.key === 'ArrowDown') { event.preventDefault(); openMenu('first'); return; }
  if (event.key === 'ArrowUp') { event.preventDefault(); openMenu('last'); }
});

$('#account-wrap')?.addEventListener('focusout', (event) => {
  if (!menuOpen) return;
  const next = event.relatedTarget;
  if (next instanceof Node && event.currentTarget.contains(next)) return;
  closeMenu(false);
});

$('#account-menu')?.addEventListener('click', (event) => {
  const item = event.target.closest('[data-menu]');
  if (!item) return;
  const action = item.dataset.menu;
  if (action === 'probe') { probeFromMenu(item); return; }
  closeMenu(true);
  if (action === 'reconnect') { reconnectFromMenu(); return; }
  if (action === 'disconnect') openExitDialog();
});

$('#account-menu')?.addEventListener('keydown', (event) => {
  const items = $$('#account-menu .menu-item');
  if (!items.length) return;
  const index = Math.max(0, items.indexOf(document.activeElement));
  if (event.key === 'Escape') { event.preventDefault(); closeMenu(true); return; }
  let next = -1;
  if (event.key === 'ArrowDown') next = (index + 1) % items.length;
  else if (event.key === 'ArrowUp') next = (index - 1 + items.length) % items.length;
  else if (event.key === 'Home') next = 0;
  else if (event.key === 'End') next = items.length - 1;
  if (next >= 0) {
    event.preventDefault();
    setRovingTabindex(items, next);
    items[next].focus();
  }
});

$('#account-menu')?.addEventListener('focusin', (event) => {
  const items = $$('#account-menu .menu-item');
  const index = items.indexOf(event.target.closest ? event.target.closest('.menu-item') : null);
  if (index >= 0) setRovingTabindex(items, index);
});

document.addEventListener('pointerdown', (event) => {
  if (!menuOpen) return;
  if (!event.target.closest('#account-wrap')) closeMenu(false);
});

document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape' && menuOpen) {
    event.preventDefault();
    closeMenu(true);
  }
});

/* setup events */
$('#setup-body').addEventListener('keydown', (event) => {
  if (event.key !== 'Enter' || event.shiftKey) return;
  const target = event.target;
  if (!target || target.tagName !== 'INPUT' || target.disabled) return;
  event.preventDefault();
  onSetupNext();
});
$('#setup-body').addEventListener('input', (event) => {
  clearFieldError(event.target);
  saveSetupFields();
  setupError('');
});
$('#setup-body').addEventListener('click', (event) => {
  const choice = event.target.closest('.choice[data-method]');
  if (choice) { chooseMethod(choice.dataset.method); return; }
  if (event.target.closest('[data-setup-browse]')) {
    openFolderDialog('server', event.target.closest('[data-setup-browse]'));
    return;
  }
  if (event.target.closest('#device-cancel')) { cancelDevice(); }
});

$('#setup-next').addEventListener('click', onSetupNext);
$('#setup-back').addEventListener('click', onSetupBack);

/* dialogs */
/* Chrome может не прислать close после Esc-закрытия, поэтому очистку и возврат
   фокуса выполняем явно для каждого пути закрытия; close — запасной. */
function focusSoon(element, allowOverride = null) {
  if (!element) return;
  let tries = 0;
  const apply = () => {
    if (!element.isConnected) return;
    const active = document.activeElement;
    /* Не отбираем фокус, если пользователь уже перешёл в другой контрол. */
    if (active && active !== element && active !== document.body && active !== allowOverride && tries > 0) return;
    element.focus({ preventScroll: true });
    if (document.activeElement !== element && tries < 12) { tries += 1; setTimeout(apply, 60); }
  };
  setTimeout(apply, 0);
}

function finishExitDialog() {
  const chip = $('#account-chip');
  if (!chip || !chip.isConnected || $('#account-wrap').hidden) return;
  focusSoon(chip);
}

function finishFolderDialog() {
  if (folder.finished) return;
  folder.finished = true;
  folderToken += 1;
  const chosen = folder.chosen;
  const target = folder.target;
  const opener = folder.opener;
  folder.opener = null;
  folder.chosen = false;
  folder.nodes = new Map();
  folder.expanded = new Set();
  folder.selected = [];
  /* После «Сохранить» фокус — на сводке источника, иначе возвращается на «Обзор». */
  const field = chosen
    ? (target === 'cloud' ? $('#source-summary') : (document.getElementById('f-dest') || $('#dest-path')))
    : null;
  const nextFocus = field || opener;
  if (nextFocus && nextFocus.isConnected) focusSoon(nextFocus, opener || null);
}

/* Модальный диалог сам удерживает фокус, но Chrome делает остановку на body
   при переходе через последний элемент — замыкаем Tab вручную. */
function keepDialogFocus(dialog, event) {
  if (event.key !== 'Tab') return;
  const focusables = Array.from(dialog.querySelectorAll('button:not([disabled]), a[href], input:not([disabled]), select, textarea, [tabindex]:not([tabindex="-1"])'))
    .filter((element) => element.getClientRects().length > 0 || element === document.activeElement);
  if (!focusables.length) return;
  const first = focusables[0];
  const last = focusables[focusables.length - 1];
  const active = document.activeElement;
  if (event.shiftKey && (active === first || !dialog.contains(active))) {
    event.preventDefault();
    last.focus({ preventScroll: true });
  } else if (!event.shiftKey && (active === last || !dialog.contains(active))) {
    event.preventDefault();
    first.focus({ preventScroll: true });
  }
}

function closeDialog(dialog) {
  if (!dialog || !dialog.open) return;
  dialog.close();
  if (dialog.id === 'folder-dialog') finishFolderDialog();
  else if (dialog.id === 'exit-dialog') finishExitDialog();
}

for (const button of $$('[data-dialog-close]')) {
  button.addEventListener('click', () => {
    closeDialog(button.closest('dialog'));
  });
}

$('#exit-dialog')?.addEventListener('keydown', (event) => keepDialogFocus(event.currentTarget, event));
$('#exit-dialog')?.addEventListener('close', () => { finishExitDialog(); });
$('#exit-dialog')?.addEventListener('cancel', (event) => { event.preventDefault(); closeDialog(event.currentTarget); });

$('#folder-dialog')?.addEventListener('keydown', (event) => keepDialogFocus(event.currentTarget, event));
$('#folder-dialog')?.addEventListener('close', () => { finishFolderDialog(); });
$('#folder-dialog')?.addEventListener('cancel', (event) => { event.preventDefault(); closeDialog(event.currentTarget); });
$('#folder-choose')?.addEventListener('click', chooseFolder);

$('#folder-crumbs')?.addEventListener('click', (event) => {
  if (event.target.closest('[data-folder-up]')) {
    const root = folderRootPath();
    if (folder.path !== root) folderNavigate(folderSegments().length > 1 ? folderPathForIndex(folderSegments().length - 2) : root);
    return;
  }
  const crumb = event.target.closest('[data-folder-path]');
  if (!crumb) return;
  folderNavigate(crumb.dataset.folderPath || folderRootPath());
});

$('#folder-body')?.addEventListener('click', (event) => {
  if (folder.target === 'cloud') {
    if (event.target.closest('[data-folder-retry]')) { loadCloudNode(''); return; }
    const retry = event.target.closest('[data-retry-node]');
    if (retry) { loadCloudNode(retry.dataset.retryNode || ''); return; }
    const expand = event.target.closest('[data-expand]');
    if (expand) { toggleCloudNode(expand.dataset.expand || ''); return; }
    return;
  }
  if (event.target.closest('[data-folder-retry]')) { loadFolder(folder.path, true); return; }
  const row = event.target.closest('[data-folder-path]');
  if (!row) return;
  folderNavigate(row.dataset.folderPath);
});

$('#folder-body')?.addEventListener('change', (event) => {
  if (!event.target.matches('.folder-check')) return;
  toggleCloudSelection(event.target.dataset.path || '', event.target.checked);
});

$('#folder-chips')?.addEventListener('click', (event) => {
  const remove = event.target.closest('[data-remove]');
  if (!remove) return;
  const path = remove.dataset.remove || '';
  toggleCloudSelection(path, false);
  const next = $('#folder-chips [data-remove]') || $('#folder-body .folder-check');
  if (next && next.isConnected) next.focus({ preventScroll: true });
});

$('#exit-confirm')?.addEventListener('click', confirmExit);
$('#offline-retry')?.addEventListener('click', () => { bootAgain(); });

window.addEventListener('hashchange', () => {
  closeMenu(false);
  route();
});

/* --- boot ----------------------------------------------------------------- */

(async function boot() {
  try {
    await loadData();
    hideBoot();
    route();
    startPolling();
  } catch {
    showOffline();
  }
})();
