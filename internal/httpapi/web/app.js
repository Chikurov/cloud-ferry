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
  failed: ['Не удалось', 'status-pill--failed'],
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
  eye: '<path d="M2.8 12S6.3 6.4 12 6.4 21.2 12 21.2 12 17.7 17.6 12 17.6 2.8 12 2.8 12Z"/><circle cx="12" cy="12" r="2.6"/>',
  eyeOff: '<path d="m4 4 16 16"/><path d="M9.9 6.7A10.7 10.7 0 0 1 12 6.4c5.7 0 9.2 5.6 9.2 5.6a15.6 15.6 0 0 1-3.2 3.4M6.5 8.5A15 15 0 0 0 2.8 12s3.5 5.6 9.2 5.6c1.2 0 2.3-.3 3.3-.7"/>',
  code: '<path d="m9 8-4 4 4 4m6-8 4 4-4 4"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  folder: '<path d="M4 7a2 2 0 0 1 2-2h3.6l2 2H18a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V7Z"/>',
  storage: '<path d="M4 8c0-1.66 3.58-3 8-3s8 1.34 8 3-3.58 3-8 3-8-1.34-8-3Z"/><path d="M4 8v8c0 1.66 3.58 3 8 3s8-1.34 8-3V8"/>',
  chevron: '<path d="m6 9 6 6 6-6"/>',
  chevronUp: '<path d="m6 15 6-6 6 6"/>',
  copy: '<rect x="9" y="9" width="10" height="10" rx="2"/><path d="M15 9V6.8A1.8 1.8 0 0 0 13.2 5H6.8A1.8 1.8 0 0 0 5 6.8v6.4A1.8 1.8 0 0 0 6.8 15H9"/>',
};

function icon(name, extraClass = '') {
  const path = ICON_PATHS[name] || '';
  return `<svg class="icon${extraClass ? ` ${extraClass}` : ''}" aria-hidden="true" focusable="false" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">${path}</svg>`;
}

/* --- state ---------------------------------------------------------------- */

let state = { accounts: [], jobs: [], downloads: '/downloads', roots: [] };
/* Логин аккаунта, который знает UI: backend может не отдавать login у старых
   подключений, поэтому запоминаем его после подключения или probe. */
const accountLogins = new Map();
let system = { roots: [] };
let wizard = {
  step: 0,
  method: 'webdav',
  dest: '/downloads',
  accountID: '',
  login: '',
  fields: {},
  methodTouched: false,
};
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
let setupBusy = false;
let validateHintTimer = null;
/* Вход через Яндекс (device flow): сервер сам обменивает код подтверждения
   на токен и создаёт аккаунт. UI показывает код, ждёт подтверждение и
   переходит к выбору папки, не создавая аккаунт повторно. */
let yandexAvailable = null; /* последний ответ GET /api/oauth/yandex/status */
let yandexAuth = {
  available: null,      /* null — способ ещё проверяем, false — не показывать */
  status: 'idle',       /* idle | starting | waiting | error | expired | done */
  flowId: '',
  userCode: '',
  verificationUrl: 'https://oauth.yandex.ru/device',
  expiresAt: 0,
  remaining: 0,
  message: '',
  retryLabel: 'Получить код',
  hasCode: false,
  statusSeq: 0,
  pollSeq: 0,
  pollInFlight: false,
  pollTimer: null,
  tickTimer: null,
};
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
  createOpen: false,
  createBusy: false,
  createUnavailable: false,
  /* Размеры папок текущего уровня: path → {bytes, files} | {error:true}.
     Пока ответа нет, у папки показывается «…». */
  sizes: new Map(),
  sizePending: new Set(),
  /* Превью файлов: открытая панель (null — закрыта) и её кэш на время
     диалога. Повторное раскрытие берёт данные из кэша, без запроса. */
  preview: null,
  files: new Map(),
  filesInflight: new Map(),
};
/* Создание папок появляется вместе с POST /api/fs/dirs. Если сервер этой
   ручки не знает (404/405), кнопку убираем до перезагрузки страницы. */
let folderCreateUnavailable = false;
/* POST /api/cloud/sizes мог появиться позже фронтенда: на 404 просто больше
   не запрашиваем размеры и не показываем их — без ошибок и заглушек. */
let cloudSizesUnavailable = false;
/* GET /api/accounts/{id}/files появился позже пикера: если сервер ответил
   404/405, кнопки «Показать файлы» убираем и оставляем прежний пикер. */
let cloudFilesUnavailable = false;
let folderToken = 0;

/* Selected cloud folders (paths relative to the cloud root, sorted). Only
   meaningful when flowSourceAll is false. */
const MAX_SOURCE_FOLDERS = 20;
let cloudSelection = [];
let cloudSelectionAccount = '';

/* П2 «Скачивание»: один путь из трёх шагов и экрана итога.
   flowSourceAll — «Весь диск» включён; flowDest — подтверждённая папка на
   сервере; flowFree — свободное место, прочитанное в шаге 2; preflight —
   результат «Узнать объём». Выбор хранится здесь, а не в DOM, поэтому он
   переживает ошибки и переходы между шагами. */
let flowStep = 1;
let flowSourceAll = true;
/* Переключатель «Показать уже скачанные» переживает переходы между шагами,
   но сбрасывается при новом входе в путь. */
let flowShowExisting = false;
let flowDest = '';
let flowFree = null;
let flowTotal = null;
let flowBusy = false;
let flowOpener = null;
/* После успешного запуска фокус уходит в «Сейчас», а не на кнопку входа:
   close-событие диалога не должно перебить этот переход. */
let flowFocusNow = false;
let flowPreflight = { key: '', status: 'idle', data: null, error: null };

/* П2, шаг «Что скачать»: какие папки уже лежат в назначении. Ответы
   POST /api/jobs/existing кэшируются по паре (destPath, path), поэтому
   раскрытие и повторная отрисовка уровня не повторяют запросы. Кэш живёт
   до смены назначения/источника, ручка необязательная: на 404/405 фильтр
   тихо выключается, а 400/403/500 показываются строкой с «Повторить». */
const EXISTING_BATCH = 20;
let existingUnavailable = false;
let existingNoticeShown = false;
let existingCache = new Map();
let existingCacheScope = '';

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

const FILE_COUNT_FORMAT = new Intl.NumberFormat('ru-RU');

function fileWord(count) {
  const mod10 = count % 10;
  const mod100 = count % 100;
  if (mod10 === 1 && mod100 !== 11) return 'файл';
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return 'файла';
  return 'файлов';
}

/* «12 483 файла»: число с разрядами и правильное склонение. */
function formatFiles(count) {
  const value = Math.max(0, Number(count) || 0);
  return `${FILE_COUNT_FORMAT.format(value)}\u00A0${fileWord(value)}`;
}

/* --- helpers -------------------------------------------------------------- */

/* Таймаут на каждый запрос: зависший fetch иначе навсегда оставляет кнопку
   в busy, а опрос — в одном незавершённом тике. Значения — по реальному
   времени серверной операции: лёгкие чтения быстрые, probe/validate идут в
   Яндекс и ждут до 90 с. */
const API_TIMEOUT = {
  read: 15000,
  folders: 30000,
  mutate: 60000,
  external: 90000,
  /* POST /api/preflight перебирает дерево в облаке: сервер отводит ему до
     10 минут, поэтому таймаут клиента не должен обрывать подсчёт раньше. */
  preflight: 600000,
};
const API_TIMEOUT_DEFAULT = 30000;

function abortWithTimeout(timeout) {
  if (typeof AbortSignal !== 'undefined' && typeof AbortSignal.timeout === 'function') {
    return { signal: AbortSignal.timeout(timeout), cleanup: () => {} };
  }
  /* Фолбэк для браузеров без AbortSignal.timeout. */
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeout);
  return { signal: controller.signal, cleanup: () => clearTimeout(timer) };
}

function apiTimeoutError() {
  return Object.assign(new Error('Сервер не ответил. Попробуйте ещё раз.'), {
    kind: 'timeout', status: 0,
  });
}

function apiNetworkError(cause) {
  return Object.assign(new Error('Сервер не отвечает. Проверьте связь и повторите.'), {
    kind: 'network', status: 0, cause,
  });
}

async function api(path, options = {}) {
  const { timeout = API_TIMEOUT_DEFAULT, signal: callerSignal, ...rest } = options || {};
  const timed = abortWithTimeout(Number(timeout) > 0 ? timeout : API_TIMEOUT_DEFAULT);
  const signal = callerSignal && typeof AbortSignal !== 'undefined' && typeof AbortSignal.any === 'function'
    ? AbortSignal.any([callerSignal, timed.signal])
    : timed.signal;
  let response;
  let text = '';
  try {
    response = await fetch(path, {
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      ...rest,
      signal,
    });
    text = await response.text();
  } catch (cause) {
    if (callerSignal && callerSignal.aborted) {
      throw Object.assign(new Error('Запрос отменён.'), { kind: 'aborted', status: 0 });
    }
    if (cause && (cause.name === 'TimeoutError' || cause.name === 'AbortError')) throw apiTimeoutError();
    throw apiNetworkError(cause);
  } finally {
    timed.cleanup();
  }
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = { error: text }; }
  if (!response.ok) {
    const serverText = data && (data.error || data.message);
    const error = new Error(typeof serverText === 'string' && serverText ? serverText : `HTTP ${response.status}`);
    error.status = response.status;
    error.data = data;
    error.field = String((data && (data.field || data.param)) || '');
    throw error;
  }
  return data;
}

/* Один вежливый live-region для статусов: пилюли и шаги не объявляют себя сами.
   Это единственный канал голосовых уведомлений — у тостов нет live-роли,
   поэтому событие не объявляется дважды. */
function dialogOpen() {
  return !!document.querySelector('dialog[open]');
}

/* #live и #toasts лежат вне <dialog>. Пока открыт модальный диалог, остальная
   страница inert, поэтому сообщения копим и отдаём после закрытия. */
let deferredAnnouncements = [];
const MAX_DEFERRED_ANNOUNCEMENTS = 2;

function announce(text) {
  const value = String(text || '').trim();
  if (!value) return;
  if (dialogOpen()) {
    deferredAnnouncements.push(value);
    if (deferredAnnouncements.length > MAX_DEFERRED_ANNOUNCEMENTS) deferredAnnouncements.shift();
    return;
  }
  const live = $('#live');
  if (!live) return;
  live.textContent = value;
}

function flushDeferredAnnouncements() {
  if (!deferredAnnouncements.length || dialogOpen()) return;
  const live = $('#live');
  const text = deferredAnnouncements.join('. ');
  deferredAnnouncements = [];
  if (live) live.textContent = text;
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
  /* «1 ч 0 мин» не показываем: минуты только когда они есть. */
  if (h > 0) return m > 0 ? `${h}\u00A0ч ${m}\u00A0мин` : `${h}\u00A0ч`;
  return m > 0 ? `${m}\u00A0мин` : `${Math.max(1, Math.round(seconds))}\u00A0с`;
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

/* Логин — из ответа API, иначе из того, что UI запомнил при подключении/probe.
   Пустая строка значит «неизвестно»: чип остаётся нейтральным. */
function accountLogin(account) {
  if (!account) return '';
  const known = accountLogins.get(account.id) || '';
  const raw = account.login || account.user || account.username || known || '';
  return String(raw).trim();
}

function accountLabel(account) {
  if (!account) return '';
  return accountLogin(account) || account.name || 'Яндекс Диск';
}

/* Понятный текст ошибки: коды важнее технического тела ответа. HTML от
   gateway, длинные тела и «Failed to fetch» в интерфейс не попадают. */
const ERROR_TEXT = {
  network: 'Сервер не отвечает. Проверьте связь и повторите.',
  timeout: 'Сервер не ответил. Попробуйте ещё раз.',
  auth: 'Нужно войти заново. Откройте подключение и повторите вход.',
  conflict: 'Действие сейчас невозможно. Завершите текущую операцию и повторите.',
  rateLimit: 'Слишком много запросов, повторите позже.',
  server: 'Сервис недоступен. Повторите позже.',
  notFound: 'Не найдено. Возможно, данные уже изменились.',
  generic: 'Не удалось выполнить запрос. Попробуйте ещё раз.',
};
const ERROR_TEXT_MAX = 240;

function clearServerText(value) {
  let text = String(value == null ? '' : value);
  if (!text) return '';
  if (/<[^>]*>/.test(text)) {
    /* HTML-тело (часто страница gateway) превращаем в плоский текст. */
    text = text
      .replace(/<script[\s\S]*?<\/script>/gi, ' ')
      .replace(/<style[\s\S]*?<\/style>/gi, ' ')
      .replace(/<[^>]*>/g, ' ');
  }
  text = text
    .replace(/&(?:amp|lt|gt|quot|#39|#x27|nbsp);/gi, ' ')
    .replace(/[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F]/g, '')
    .replace(/\s+/g, ' ')
    .trim();
  if (!text) return '';
  if (/^(failed to fetch|load failed|networkerror|typeerror|fetch failed)$/i.test(text)) return '';
  if (text.length > ERROR_TEXT_MAX) text = `${text.slice(0, ERROR_TEXT_MAX).trimEnd()}…`;
  return text;
}

function friendlyError(error) {
  if (!error) return ERROR_TEXT.generic;
  if (typeof error === 'string') return clearServerText(error) || ERROR_TEXT.generic;
  if (error.kind === 'network') return ERROR_TEXT.network;
  if (error.kind === 'timeout') return ERROR_TEXT.timeout;
  const status = Number(error.status) || 0;
  const message = clearServerText(error.message);
  const genericStatus = !message || /^HTTP \d+$/.test(message);
  if (status === 401 || status === 403) return ERROR_TEXT.auth;
  if (status === 429) return ERROR_TEXT.rateLimit;
  if (status >= 500) return ERROR_TEXT.server;
  if (status === 409) return genericStatus ? ERROR_TEXT.conflict : message;
  if (status === 404) return genericStatus ? ERROR_TEXT.notFound : message;
  if (status === 0) return message || ERROR_TEXT.network;
  return genericStatus ? ERROR_TEXT.generic : message;
}

/* busy не через disabled: disabled у сфокусированной кнопки уводит фокус на
   body и после ошибки его никто не возвращает. aria-disabled + guards в
   обработчиках сохраняют фокус, а CSS рисует неактивное состояние. */
function setBusy(button, busy) {
  if (!button || !button.setAttribute) return;
  button.setAttribute('aria-busy', busy ? 'true' : 'false');
  if (busy) button.setAttribute('aria-disabled', 'true');
  else button.removeAttribute('aria-disabled');
}

function isBusyControl(button) {
  return !!button && !!button.getAttribute && button.getAttribute('aria-disabled') === 'true';
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

/* У поля может быть собственный aria-describedby (подсказка формата). Ошибку
   добавляем к нему, а при очистке возвращаем исходный набор, не удаляя его. */
function setDescribedBy(element, extraID = '') {
  if (element.dataset.baseDescribedby === undefined) {
    element.dataset.baseDescribedby = element.getAttribute('aria-describedby') || '';
  }
  const ids = [element.dataset.baseDescribedby, extraID].filter(Boolean).join(' ').trim();
  if (ids) element.setAttribute('aria-describedby', ids);
  else element.removeAttribute('aria-describedby');
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
    setDescribedBy(element, message ? node.id : '');
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
  setDescribedBy(element, '');
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
    token: 'f-token',
    destPath: 'f-dest', dest: 'f-dest', path: 'f-dest',
  };
  const id = map[field];
  const target = id ? document.getElementById(id) : null;
  return target && scope.contains(target) ? target : null;
}

/* --- toasts --------------------------------------------------------------- */

const TOAST_ICONS = { ok: 'check', warn: 'warning', error: 'warning', info: 'cloud' };

/* Тост не объявляет себя сам (нет live-роли): голосовой канал один — announce.
   Пока открыт модальный диалог, уведомления ждут в очереди вне inert-области. */
const deferredToasts = [];
const MAX_DEFERRED_TOASTS = 8;

function queueToast(options) {
  const id = options.id || '';
  if (id) {
    const index = deferredToasts.findIndex((item) => item.id === id);
    if (index >= 0) deferredToasts.splice(index, 1);
  }
  deferredToasts.push(options);
  while (deferredToasts.length > MAX_DEFERRED_TOASTS) deferredToasts.shift();
}

function flushDeferredToasts() {
  if (!deferredToasts.length || dialogOpen()) return;
  const items = deferredToasts.splice(0);
  for (const options of items) showToast(options);
}

function writeToastMessage(item, text) {
  let element = item.querySelector('.toast-message');
  if (!element) {
    element = document.createElement('span');
    element.className = 'toast-message';
    item.querySelector('.toast-content').append(element);
  }
  element.textContent = text;
}

/* Обновление текста уже показанного тоста (например, ошибка действия) нужно
   озвучить отдельно: live-роли у тоста нет. */
function setToastMessage(item, text) {
  writeToastMessage(item, text);
  const title = item.querySelector('.toast-title');
  announce([title ? title.textContent : '', text].filter(Boolean).join('. '));
}

/* Новый тост не вытесняет тот, в котором сейчас фокус. */
function trimToasts(host) {
  const active = document.activeElement;
  while (host.children.length > 4) {
    const victim = Array.from(host.children).find((child) => !(active && child.contains(active)));
    if (!victim) break;
    victim.remove();
  }
}

/* Успех скрывается сам через 6 с; пауза на hover и focus. Остальные ждут закрытия. */
const TOAST_TIMEOUT = 6000;

function showToast(options = {}) {
  const { type = 'info', title, message = '', action = null, id = '', persistent } = options || {};
  const host = $('#toasts');
  if (!host) return null;
  if (id) {
    const existing = host.querySelector(`[data-toast-id="${cssEscape(id)}"]`);
    if (existing) return existing;
  }
  if (dialogOpen()) { queueToast({ ...options }); return null; }
  const keep = persistent !== undefined ? persistent : type !== 'ok';
  const item = document.createElement('div');
  item.className = `toast toast--${type}`;
  if (id) item.dataset.toastId = id;
  item.innerHTML = `
    <span class="toast-icon" aria-hidden="true">${icon(TOAST_ICONS[type] || 'cloud')}</span>
    <div class="toast-content">
      <span class="toast-title"></span>
      ${message ? '<span class="toast-message"></span>' : ''}
      ${action ? `<div class="toast-actions"><button class="btn ${action.variant === 'danger' ? 'btn--danger' : 'btn--secondary'} btn--sm" type="button">${escapeHtml(action.label)}</button></div>` : ''}
    </div>
    <button class="toast-close" type="button" aria-label="Скрыть уведомление">${icon('close')}</button>`;
  item.querySelector('.toast-title').textContent = title;
  if (message) writeToastMessage(item, message);

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
      if (isBusyControl(actionButton)) return;
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
  trimToasts(host);
  announce([title, message].filter(Boolean).join('. '));
  return item;
}

function closeToast(id) {
  if (!id) return;
  const index = deferredToasts.findIndex((options) => options.id === id);
  if (index >= 0) deferredToasts.splice(index, 1);
  const item = $(`#toasts [data-toast-id="${cssEscape(id)}"]`);
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
    /* Тост сам объявляется через #live — второй канал не нужен. */
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

/* Опрос — один запрос в полёте и следующий тик только после его завершения.
   Иначе на медленной сети два тика идут параллельно и старый ответ
   перезаписывает новый. */
let pollTimer = null;
let pollInFlight = false;
let pollSeq = 0;
let pollFailures = 0;
const POLL_INTERVAL = 2500;
const POLL_MAX_BACKOFF = 30000;

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

function stopPolling() {
  if (pollTimer) {
    clearTimeout(pollTimer);
    pollTimer = null;
  }
}

function pollDelay() {
  if (!pollFailures) return POLL_INTERVAL;
  /* Экспоненциальный backoff, но не чаще раза в 30 с на плохой сети. */
  return Math.min(POLL_MAX_BACKOFF, POLL_INTERVAL * (2 ** Math.min(pollFailures, 4)));
}

function scheduleTick(delay = pollDelay()) {
  if (pollTimer) clearTimeout(pollTimer);
  pollTimer = setTimeout(() => { tick(); }, delay);
}

function startPolling() {
  stopPolling();
  pollFailures = 0;
  if (document.hidden || pollInFlight) return;
  /* Первый тик — по обычному интервалу: данные только что загружены стартом. */
  scheduleTick();
}

async function bootAgain() {
  const offline = $('#offline-view');
  if (offline) offline.hidden = true;
  showBoot();
  try {
    await loadData();
    hideBoot();
    route();
  } catch {
    showOffline();
  } finally {
    /* Даже после неудачи первого запроса опрос сам восстановит связь. */
    startPolling();
  }
}

/* --- data ----------------------------------------------------------------- */

/* Каждый ответ получает номер: если за время запроса состояние уже обновил
   более новый запрос, устаревший ответ не перезаписывает его. */
let loadSeq = 0;

async function loadData() {
  const seq = ++loadSeq;
  const [snapshot, sys] = await Promise.all([
    api('/api/state', { timeout: API_TIMEOUT.read }),
    api('/api/system', { timeout: API_TIMEOUT.read }).catch(() => null),
  ]);
  if (seq !== loadSeq) return false;
  state = snapshot || { accounts: [], jobs: [], downloads: '/downloads' };
  for (const account of state.accounts || []) {
    const login = accountLogin(account);
    if (login) accountLogins.set(account.id, login);
  }
  if (sys) system = sys;
  syncJobNotifications();
  setOffline(false);
  return true;
}

async function tick() {
  pollTimer = null;
  if (document.hidden) return;
  if (pollInFlight) { scheduleTick(); return; }
  pollInFlight = true;
  const seq = ++pollSeq;
  try {
    const fresh = await loadData();
    if (seq !== pollSeq || !fresh) return;
    pollFailures = 0;
    if ($('#app-view').hidden && $('#setup-view').hidden) { route(); return; }
    render();
  } catch {
    if (seq !== pollSeq) return;
    pollFailures += 1;
    setOffline(true);
  } finally {
    pollInFlight = false;
    if (seq === pollSeq && !document.hidden) scheduleTick();
  }
}

/* На скрытой вкладке не опрашиваем сервер; при возврате — сразу и без backoff. */
document.addEventListener('visibilitychange', () => {
  if (document.hidden) { stopPolling(); return; }
  pollFailures = 0;
  if (pollInFlight) return;
  scheduleTick(0);
});

/* После успешной мутации перечитываем состояние. Сбой обновления не отменяет
   уже подтверждённый ответом API результат операции — это отдельное
   предупреждение, а статус операции не меняется. */
async function refreshAfterAction() {
  try {
    await loadData();
    render();
    return true;
  } catch (error) {
    showToast({
      type: 'warn', id: 'refresh-failed',
      title: 'Не удалось обновить данные',
      message: friendlyError(error),
      action: {
        label: 'Обновить',
        onClick: async () => { if (await refreshAfterAction()) closeToast('refresh-failed'); },
      },
    });
    return false;
  }
}

/* --- routing -------------------------------------------------------------- */

function route() {
  let hash = location.hash || '#/';
  if (hash !== '#/' && hash !== '#/setup') {
    history.replaceState(null, '', '#/');
    hash = '#/';
  }
  /* Смена hash закрывает модальные диалоги: иначе они висят поверх другого
     экрана и держат focus trap. DELETE аккаунта не прерываем. */
  closeDialogsForRoute();
  hideBoot();
  const offline = $('#offline-view');
  if (offline) offline.hidden = true;
  if (hash === '#/setup') { showSetup(); return; }
  /* Уходим из мастера: незавершённый вход через Яндекс отменяем на сервере,
     чтобы не держать слот и не продолжать опрос на скрытом экране. */
  stopYandexFlow({ cancel: true });
  yandexAuth.statusSeq += 1;
  clearWizardSecrets();
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
  /* Повторный вход в мастер не должен наследовать чужой незавершённый поток. */
  stopYandexFlow({ cancel: true });
  resetYandexState();
  yandexAuth.available = yandexAvailable;
  /* Если сервер уже сообщал о доступности Яндекс-входа, выбираем его сразу:
     иначе способ появится через мгновение после ответа статуса. */
  const yandexDefault = yandexAuth.available === true;
  wizard = {
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
  homeMode = '';
  renderSetup();
  refreshYandexAvailability();
}

/* --- setup wizard --------------------------------------------------------- */

/* Способы входа. «Войти через Яндекс» — основной, когда сервер настроен:
   пароль приложения и токен остаются запасными и видны ниже. */
const SETUP_METHODS = {
  yandex: 'Войти через Яндекс',
  webdav: 'Логин и пароль приложения',
  'yandex-token': 'Вход по токену',
};

const YANDEX_DEVICE_URL = 'https://oauth.yandex.ru/device';
/* Требование макета: проверяем подтверждение каждые 5 секунд. */
const YANDEX_POLL_MS = 5000;
const YANDEX_ACCOUNT_NAME = 'Яндекс Диск';

/* Команда обмена кода на токен: показывается в инструкции и копируется как есть. */
const TOKEN_COMMAND = [
  'curl -s https://oauth.yandex.ru/token \\',
  '  -d grant_type=authorization_code \\',
  '  -d code=КОД_ИЗ_АДРЕСА \\',
  '  -d client_id=ID_ПРИЛОЖЕНИЯ \\',
  '  -d client_secret=СЕКРЕТ_ПРИЛОЖЕНИЯ',
].join('\n');

function setupFieldValue(name) {
  return escapeHtml(wizard.fields[name] || '');
}

/* Сегменты-переключатели: все доступные способы видны сразу, выбор не теряет
   ввод. Яндекс-вход — первым и во всю ширину, остальные два — под ним. */
function availableMethods() {
  return Object.keys(SETUP_METHODS).filter((method) => method !== 'yandex' || yandexAuth.available === true);
}

function methodSwitch() {
  const options = availableMethods().map((method) => {
    const active = wizard.method === method;
    const primary = method === 'yandex' ? ' method-option--primary' : '';
    return `<button class="method-option${primary}" type="button" role="radio" id="method-${method}" data-method="${method}" aria-checked="${active ? 'true' : 'false'}" tabindex="${active ? '0' : '-1'}">${escapeHtml(SETUP_METHODS[method])}</button>`;
  }).join('');
  return `<div class="method-switch" role="radiogroup" aria-label="Способ входа">${options}</div>`;
}

function loginFields() {
  return `
    <form class="setup-form" id="setup-form" novalidate autocomplete="on">
      <div class="control">
        <label class="control-label" for="f-user">Логин</label>
        <input id="f-user" name="username" type="text" autocomplete="username" inputmode="text" spellcheck="false" autocapitalize="none" autocorrect="off" placeholder="Логин на Яндексе" value="${setupFieldValue('user')}">
      </div>
      <div class="control">
        <label class="control-label" for="f-pass">Пароль приложения</label>
        <div class="field-with-action">
          <input id="f-pass" name="password" type="password" autocomplete="current-password" inputmode="text" spellcheck="false" autocapitalize="none" autocorrect="off" placeholder="Пароль из Яндекс ID" value="${setupFieldValue('pass')}">
          <button class="field-action" type="button" id="f-pass-toggle" aria-label="Показать пароль" aria-pressed="false" title="Показать пароль">${icon('eye')}</button>
        </div>
      </div>
      <p class="note" id="webdav-note">Нужен тариф <strong>Яндекс 360</strong>. Пароль приложения создаётся в <a href="https://id.yandex.ru/security/app-passwords" target="_blank" rel="noreferrer">Яндекс ID → Пароли приложений</a> и начинает действовать через 2–3 часа.</p>
      <button class="sr-only" type="submit" tabindex="-1" aria-hidden="true">Подключиться</button>
    </form>`;
}

function tokenFields() {
  const open = !!wizard.tokenHelp;
  return `
    <form class="setup-form" id="setup-form" novalidate autocomplete="off">
      <div class="control control--token">
        <div class="control-head">
          <label class="control-label" for="f-token">Токен</label>
          <button class="token-help-toggle" type="button" id="token-help-toggle" aria-expanded="${open ? 'true' : 'false'}" aria-controls="token-help">${icon('code')}Как получить токен?</button>
        </div>
        <textarea id="f-token" name="token" rows="3" spellcheck="false" autocapitalize="none" autocorrect="off" autocomplete="off" aria-describedby="token-required" placeholder="Вставьте токен или JSON из ответа">${setupFieldValue('token')}</textarea>
      </div>
      <p class="note" id="token-required">Нужен <strong>refresh-токен</strong>: обычный access-токен истекает через час, и подключение перестанет работать.</p>
      <div class="token-help" id="token-help"${open ? '' : ' hidden'}>
        <ol class="token-steps">
          <li>Создайте приложение на <a href="https://oauth.yandex.ru/client/new" target="_blank" rel="noreferrer">oauth.yandex.ru</a> и отметьте доступ <code>cloud_api:disk.read</code>.</li>
          <li>Откройте <code class="token-url">https://oauth.yandex.ru/authorize?response_type=code&amp;client_id=ВАШ_ID</code> — после разрешения браузер вернёт код в адресной строке.</li>
          <li>Обменяйте код на токен командой:
            <div class="token-command">
              <code id="token-command">${escapeHtml(TOKEN_COMMAND)}</code>
              <button class="btn btn--secondary btn--sm" type="button" id="token-copy">Скопировать</button>
            </div>
          </li>
          <li>Скопируйте JSON из ответа и вставьте его в поле выше.</li>
        </ol>
      </div>
      <button class="sr-only" type="submit" tabindex="-1" aria-hidden="true">Подключиться</button>
    </form>`;
}

/* --- вход через Яндекс: код подтверждения -------------------------------- */

function formatUserCode(value) {
  const code = String(value || '').trim().toUpperCase();
  if (!code) return '';
  if (code.includes('-')) return code;
  if (/^[A-Z0-9]{8}$/.test(code)) return `${code.slice(0, 4)}-${code.slice(4)}`;
  return code;
}

function formatCountdown(seconds) {
  const total = Math.max(0, Math.ceil(Number(seconds) || 0));
  const minutes = Math.floor(total / 60);
  return `${String(minutes).padStart(2, '0')}:${String(total % 60).padStart(2, '0')}`;
}

/* Ссылку подтверждения сервер отдаёт сам; в href пускаем только http(s). */
function safeHttpUrl(value, fallback) {
  const raw = String(value || '').trim();
  if (/^https?:\/\/[^\s]+$/i.test(raw)) return raw;
  return fallback;
}

function yandexIdleFields() {
  return `
    <div class="yandex-intro" id="yandex-intro">
      <p class="note" id="yandex-note">Код подтверждения действует 10 минут. Пароль приложения не нужен — подтвердить вход можно с телефона.</p>
      <button class="btn btn--primary" type="button" id="yandex-start">Получить код</button>
    </div>`;
}

function yandexWaitingFields() {
  const code = formatUserCode(yandexAuth.userCode);
  const url = safeHttpUrl(yandexAuth.verificationUrl, YANDEX_DEVICE_URL);
  return `
    <div class="yandex-panel" id="yandex-panel">
      <p class="control-label" id="yandex-code-label">Код для входа</p>
      <p class="yandex-code" id="yandex-code" tabindex="-1" aria-labelledby="yandex-code-label">${escapeHtml(code)}</p>
      <p class="yandex-hint">Откройте <a href="${escapeHtml(url)}" target="_blank" rel="noreferrer">${escapeHtml(url)}</a> и введите код.</p>
      <p class="yandex-timer" id="yandex-timer">Код действует <span id="yandex-timer-value">${formatCountdown(yandexAuth.remaining)}</span></p>
      <p class="yandex-state" id="yandex-state" role="status" aria-live="polite"><span class="yandex-pulse" aria-hidden="true"></span><span id="yandex-state-text"></span></p>
      <button class="btn btn--secondary" type="button" id="yandex-cancel">Отменить</button>
    </div>`;
}

function yandexFailureFields() {
  const expired = yandexAuth.status === 'expired';
  const title = expired ? 'Код истёк' : (yandexAuth.message || 'Не удалось войти через Яндекс. Попробуйте ещё раз.');
  const retry = yandexAuth.retryLabel || (yandexAuth.hasCode ? 'Получить новый код' : 'Получить код');
  return `
    <div class="yandex-panel yandex-panel--error" id="yandex-panel">
      <p class="yandex-state yandex-state--error" id="yandex-state" role="status" aria-live="polite">${icon('warning')}<span id="yandex-state-text"></span></p>
      <p class="note">Другие способы тоже доступны — выберите их выше.</p>
      <button class="btn btn--primary" type="button" id="yandex-retry">${escapeHtml(retry)}</button>
    </div>`;
}

function yandexFields() {
  if (yandexAuth.status === 'waiting') return yandexWaitingFields();
  if (yandexAuth.status === 'error' || yandexAuth.status === 'expired') return yandexFailureFields();
  return yandexIdleFields();
}

/* Текст собственного live-региона способа: он создан пустым, поэтому любое
   последующее изменение гарантированно озвучивается. */
function yandexLiveText() {
  if (wizard.step !== 0 || wizard.method !== 'yandex') return '';
  if (yandexAuth.status === 'waiting') return 'Ждём подтверждение…';
  if (yandexAuth.status === 'expired') return 'Код истёк';
  if (yandexAuth.status === 'error') return yandexAuth.message || 'Не удалось войти через Яндекс. Попробуйте ещё раз.';
  return '';
}

function syncYandexLive() {
  const node = document.getElementById('yandex-state-text');
  if (!node) return;
  const text = yandexLiveText();
  if (text) node.textContent = text;
}

function setYandexLive(text) {
  const node = document.getElementById('yandex-state-text');
  if (node && text) node.textContent = text;
}

function focusYandexRetry() {
  const retry = document.getElementById('yandex-retry');
  if (retry) retry.focus({ preventScroll: true });
}

function methodFields() {
  if (wizard.method === 'yandex') return yandexFields();
  return wizard.method === 'yandex-token' ? tokenFields() : loginFields();
}

const STEP_TITLES = ['Подключение', 'Папка на сервере', 'Готово'];

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
  $('#setup-step-text').textContent = `Шаг ${wizard.step + 1} из 3`;
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
    announce(`Шаг ${wizard.step + 1} из 3: ${STEP_TITLES[wizard.step]}`);
  }
  /* Live-регион способа заполняем после вставки разметки: пустой узел,
     созданный вместе с панелью, не озвучивается, а изменение — да. */
  syncYandexLive();
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
  const map = { 'f-user': 'user', 'f-pass': 'pass', 'f-token': 'token' };
  for (const [id, key] of Object.entries(map)) {
    const element = document.getElementById(id);
    if (element) wizard.fields[key] = element.value;
  }
  const dest = document.getElementById('f-dest');
  if (dest) wizard.dest = dest.value;
}

/* Пароль и токен не держим в памяти дольше, чем нужно. После успешного
   создания аккаунта они уже не нужны, а после отмены — тем более. */
function clearWizardSecrets() {
  delete wizard.fields.pass;
  delete wizard.fields.token;
  const pass = document.getElementById('f-pass');
  if (pass) pass.value = '';
  const token = document.getElementById('f-token');
  if (token) token.value = '';
}

function resetWizardFields() {
  wizard.fields = {};
  const pass = document.getElementById('f-pass');
  if (pass) pass.value = '';
  const token = document.getElementById('f-token');
  if (token) token.value = '';
}

function chooseMethod(method) {
  if (!method || !availableMethods().includes(method)) return;
  saveSetupFields();
  wizard.methodTouched = true;
  if (method !== wizard.method) {
    /* Уход со способа «Яндекс» отменяет незавершённый поток на сервере. */
    if (wizard.method === 'yandex') {
      stopYandexFlow({ cancel: true });
      resetYandexState();
    }
    wizard.method = method;
    renderSetup({ focusTitle: false });
    announce(SETUP_METHODS[method]);
  }
  /* Фокус остаётся на выбранном сегменте: Tab ведёт дальше в форму. */
  const option = document.getElementById(`method-${wizard.method}`);
  if (option) option.focus({ preventScroll: true });
}

async function enforceSingleAccount(keepID) {
  const stale = state.accounts.filter((account) => account.id !== keepID);
  if (!stale.length) return;
  let failed = 0;
  for (const account of stale) {
    try { await api(`/api/accounts/${encodeURIComponent(account.id)}`, { method: 'DELETE', timeout: API_TIMEOUT.mutate }); }
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
  wizard.accountID = account.id;
  const enteredLogin = wizard.method === 'webdav' ? String(wizard.fields.user || '').trim() : '';
  const login = accountLogin(account) || enteredLogin;
  wizard.login = login;
  if (login) accountLogins.set(account.id, login);
  try { await loadData(); } catch { /* the wizard stays usable */ }
  await enforceSingleAccount(account.id);
  wizard.step = 1;
  renderSetup();
  const title = login ? `Подключено как ${login}` : 'Яндекс Диск подключён';
  showToast({ type: 'ok', title, message: login ? '' : 'Можно выбирать папку для скачивания.' });
  clearWizardSecrets();
  lastCloudAttempt = 0;
  ensureCloudUsage(true);
}

/* --- проверка доступа до создания аккаунта -------------------------------- */

/* POST /api/accounts/validate подтверждает доступ, и только после этого
   создаётся аккаунт: при ошибке шаг 1 остаётся как был, а в сценарии «Войти
   заново» старый рабочий аккаунт не удаляется до успеха. */
const VALIDATE_HINT_DELAY = 2400;
const VALIDATE_HINT_TEXT = 'Проверяем доступ к Яндекс Диску…';
const VALIDATE_FIELD_TARGET = {
  user: '#f-user', login: '#f-user', url: '#f-user',
  password: '#f-pass', pass: '#f-pass', token: '#f-token',
};

function setupStatus(message) {
  const node = $('#setup-status');
  if (node) node.textContent = message || '';
}

/* Медленную проверку показываем один раз, чтобы интерфейс не мигал. */
function startValidateHint() {
  stopValidateHint();
  validateHintTimer = setTimeout(() => {
    validateHintTimer = null;
    if (setupBusy) setupStatus(VALIDATE_HINT_TEXT);
  }, VALIDATE_HINT_DELAY);
}

function stopValidateHint() {
  if (validateHintTimer) {
    clearTimeout(validateHintTimer);
    validateHintTimer = null;
  }
  setupStatus('');
}

/* Пока идёт проверка и создание, поля и переход назад недоступны. */
function setSetupFieldsDisabled(disabled) {
  const scope = $('#setup-body');
  if (scope) {
    for (const element of scope.querySelectorAll('input, textarea, button')) element.disabled = !!disabled;
  }
  const back = $('#setup-back');
  if (back) back.disabled = !!disabled;
}

/* Ответ проверки → понятная ошибка у поля. Технические детали (rclone,
   ответы провайдера, HTTP-коды) пользователю не показываем. */
function validationFailure(error, method) {
  const data = (error && error.data) || {};
  const status = Number(error && error.status) || 0;
  const code = String(data.code || '');
  const tokenMode = method === 'yandex-token';
  if (code === 'invalid_request') {
    if (data.field === 'token' || tokenMode) {
      return { field: 'token', message: 'Токен не подошёл или истёк. Проверьте, что скопировали его целиком.' };
    }
    if (!data.field) {
      return { message: 'Не удалось проверить данные — обновите страницу и попробуйте ещё раз.' };
    }
    if (data.field === 'password' || data.field === 'pass') {
      return { field: 'password', message: 'Проверьте пароль приложения: сервер не принял данные.' };
    }
    return { field: 'user', message: 'Проверьте логин: сервер не принял данные.' };
  }
  if (code === 'auth_failed') {
    /* 401 от WebDAV: пароль приложения мог ещё не активироваться. */
    if (data.retryAfterHint === true) {
      return { field: 'password', message: 'Пароль приложения ещё не активировался — подождите 2–3 часа.' };
    }
    if (tokenMode) return { field: 'token', message: 'Токен не подошёл или истёк. Проверьте, что скопировали его целиком.' };
    return { field: 'password', message: 'Неверный логин или пароль приложения. Проверьте данные из Яндекс ID.' };
  }
  if (code === 'webdav_unavailable') {
    return { message: 'WebDAV недоступен — нужен Яндекс 360.' };
  }
  if (status === 0 || status >= 500 || code === 'upstream_error' || code === 'timeout') {
    return { message: 'Сервер не ответил — попробуйте ещё раз.' };
  }
  return tokenMode
    ? { field: 'token', message: 'Токен не подошёл или истёк. Проверьте, что скопировали его целиком.' }
    : { message: 'Не удалось проверить доступ к Яндекс Диску. Попробуйте ещё раз.' };
}

async function validateAccount(body, method) {
  try {
    await api('/api/accounts/validate', { method: 'POST', timeout: API_TIMEOUT.external, body: JSON.stringify(body) });
    return { ok: true };
  } catch (error) {
    /* TODO: временный фолбэк для старого сервера без POST /api/accounts/validate.
       Пока ручки нет (404/405), создаём аккаунт как раньше — экран не ломаем.
       Когда образ с проверкой станет обязательным, эту ветку удалить. */
    if (error && (error.status === 404 || error.status === 405)) return { ok: true, legacy: true };
    return { ok: false, ...validationFailure(error, method) };
  }
}

/* Ошибка без поля — над футером, ошибка поля — под контролом. */
function showConnectError(failure) {
  const target = failure && failure.field ? $(VALIDATE_FIELD_TARGET[failure.field] || '') : null;
  if (target && failure.message) {
    setFieldError(target, failure.message);
    return;
  }
  setupError((failure && failure.message) || 'Не удалось проверить доступ к Яндекс Диску. Попробуйте ещё раз.');
}

async function connectCloud() {
  const scope = $('#setup-body');
  clearFieldErrors(scope);
  setupError('');
  let body;
  if (wizard.method === 'webdav') {
    const user = (wizard.fields.user || '').trim();
    const password = wizard.fields.pass || '';
    if (!user) { setFieldError($('#f-user'), 'Укажите логин.'); return; }
    if (!password) { setFieldError($('#f-pass'), 'Укажите пароль приложения.'); return; }
    body = { name: 'Яндекс Диск', provider: 'webdav', url: 'https://webdav.yandex.ru', user, password };
  } else {
    const token = (wizard.fields.token || '').trim();
    if (!token) {
      setFieldError($('#f-token'), 'Вставьте токен.');
      return;
    }
    // Сервер принимает и обычный токен, и полный JSON из rclone.
    if (token.startsWith('{')) {
      let parsedToken;
      try { parsedToken = JSON.parse(token); } catch {
        setFieldError($('#f-token'), 'Не удалось разобрать JSON. Вставьте токен строкой или целиком JSON.');
        return;
      }
      if (!parsedToken || typeof parsedToken !== 'object' || Array.isArray(parsedToken) || !parsedToken.access_token) {
        setFieldError($('#f-token'), 'В JSON нет поля access_token.');
        return;
      }
    } else if (token.length < 20 || /\s/.test(token)) {
      setFieldError($('#f-token'), 'Токен выглядит неполным — скопируйте его целиком, без пробелов.');
      return;
    }
    body = { name: 'Яндекс Диск', provider: 'yandex-token', token };
  }
  /* Та же схема для пароля и токена: сначала проверка, потом создание. */
  const checked = await validateAccount(body, wizard.method);
  if (!checked.ok) { showConnectError(checked); return; }
  const account = await api('/api/accounts', { method: 'POST', timeout: API_TIMEOUT.external, body: JSON.stringify(body) });
  await finishConnect(account);
}

/* Один путь подключения: форма шага 1 (submit или кнопка) вызывает runConnect. */
async function runConnect() {
  const next = $('#setup-next');
  /* Кнопка не disabled, а aria-disabled: фокус не теряется, повторный
     Enter/клик отсекаем здесь. */
  if (setupBusy || isBusyControl(next)) return;
  setupError('');
  saveSetupFields();
  const scope = $('#setup-body');
  setBusy(next, true);
  setupBusy = true;
  setSetupFieldsDisabled(true);
  startValidateHint();
  try {
    await connectCloud();
  } catch (error) {
    const target = serverFieldTarget(scope, error);
    if (target) setFieldError(target, friendlyError(error));
    else setupError(friendlyError(error));
  } finally {
    stopValidateHint();
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

async function onSetupNext() {
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

/* --- вход через Яндекс: получение кода и опрос ---------------------------- */

function clearYandexTimers() {
  if (yandexAuth.tickTimer) {
    clearInterval(yandexAuth.tickTimer);
    yandexAuth.tickTimer = null;
  }
  if (yandexAuth.pollTimer) {
    clearTimeout(yandexAuth.pollTimer);
    yandexAuth.pollTimer = null;
  }
}

/* Сброс состояния способа без обращения к серверу. Доступность способа
   (yandexAuth.available) при этом сохраняется: её решает статус сервера. */
function resetYandexState() {
  clearYandexTimers();
  yandexAuth.status = 'idle';
  yandexAuth.flowId = '';
  yandexAuth.userCode = '';
  yandexAuth.verificationUrl = YANDEX_DEVICE_URL;
  yandexAuth.expiresAt = 0;
  yandexAuth.remaining = 0;
  yandexAuth.message = '';
  yandexAuth.retryLabel = 'Получить код';
  yandexAuth.hasCode = false;
  yandexAuth.pollInFlight = false;
  yandexAuth.pollSeq += 1;
}

/* Отмена потока: останавливаем таймеры и, если поток был активен, удаляем
   его на сервере. При закрытии вкладки запрос уходит keepalive — ответа
   страница уже не ждёт. */
function stopYandexFlow({ cancel = false, keepalive = false } = {}) {
  const flowId = yandexAuth.flowId;
  const active = yandexAuth.status === 'waiting' || yandexAuth.status === 'starting';
  const finished = yandexAuth.status === 'done';
  clearYandexTimers();
  yandexAuth.pollSeq += 1;
  yandexAuth.pollInFlight = false;
  yandexAuth.flowId = '';
  if (!finished) yandexAuth.status = 'idle';
  if (cancel && active && flowId) deleteYandexFlow(flowId, keepalive);
}

function deleteYandexFlow(flowId, keepalive = false) {
  if (!flowId) return;
  const path = `/api/oauth/yandex/device/${encodeURIComponent(flowId)}`;
  try {
    if (keepalive) {
      fetch(path, { method: 'DELETE', keepalive: true }).catch(() => {});
      return;
    }
    api(path, { method: 'DELETE', timeout: 8000 }).catch(() => {});
  } catch {
    /* Отмена — вспомогательный запрос: сбой сети не должен ничего ломать. */
  }
}

/* Проверка на старте мастера: показывать ли способ «Войти через Яндекс». */
async function refreshYandexAvailability() {
  const seq = ++yandexAuth.statusSeq;
  let available = false;
  try {
    const data = await api('/api/oauth/yandex/status', { timeout: API_TIMEOUT.read });
    available = !!(data && data.available === true);
  } catch {
    /* Старый сервер или нет связи: способ просто не показываем. */
    available = false;
  }
  if (seq !== yandexAuth.statusSeq) return;
  yandexAvailable = available;
  yandexAuth.available = available;
  if ($('#setup-view').hidden || wizard.step !== 0) return;
  const preferred = available ? 'yandex' : 'webdav';
  const touched = wizard.methodTouched || hasWizardInput();
  if (!touched && wizard.method !== preferred) {
    /* Ничего ещё не выбирали: основной способ — Яндекс, он выбран сразу. */
    wizard.method = preferred;
    saveSetupFields();
    renderSetup({ focusTitle: false });
    const option = document.getElementById(`method-${preferred}`);
    if (option) option.focus({ preventScroll: true });
    return;
  }
  if (touched) {
    /* Пользователь уже выбрал способ: сегмент добавляем или убираем, но
       выбор не меняем. Ввод и фокус сохраняем. */
    saveSetupFields();
    const focus = captureSetupFocus();
    renderSetup({ focusTitle: false });
    restoreSetupFocus(focus);
  }
}

function hasWizardInput() {
  const fields = wizard.fields || {};
  return Boolean(String(fields.user || '').trim() || fields.pass || fields.token);
}

function captureSetupFocus() {
  const active = document.activeElement;
  if (!active || !active.id || !active.closest || !active.closest('#setup-body')) return null;
  return { id: active.id, start: active.selectionStart, end: active.selectionEnd };
}

function restoreSetupFocus(snapshot) {
  if (!snapshot || !snapshot.id) return;
  const element = document.getElementById(snapshot.id);
  if (!element) return;
  element.focus({ preventScroll: true });
  if (typeof snapshot.start === 'number') {
    try { element.setSelectionRange(snapshot.start, snapshot.end); } catch { /* не поле ввода */ }
  }
}

function yandexStartFailureMessage(error) {
  const status = Number(error && error.status) || 0;
  if (status === 429) return 'Слишком много попыток, подождите минуту.';
  if (status === 400) return 'На сервере не настроены ключи приложения. Выберите пароль приложения или токен.';
  if (status === 502) return 'Яндекс не выдал код. Подождите минуту и попробуйте ещё раз.';
  return friendlyError(error) || 'Не удалось получить код. Попробуйте ещё раз.';
}

async function startYandexDevice() {
  if (setupBusy || yandexAuth.status === 'starting' || yandexAuth.status === 'waiting') return;
  const trigger = document.getElementById('yandex-start') || document.getElementById('yandex-retry');
  yandexAuth.status = 'starting';
  yandexAuth.message = '';
  setupError('');
  setupStatus('Получаем код…');
  if (trigger) setBusy(trigger, true);
  let data = null;
  let failure = null;
  try {
    data = await api('/api/oauth/yandex/device/start', {
      method: 'POST',
      timeout: API_TIMEOUT.external,
      body: JSON.stringify({ name: YANDEX_ACCOUNT_NAME }),
    });
  } catch (error) {
    failure = error;
  }
  setupStatus('');
  if (trigger && trigger.isConnected) setBusy(trigger, false);
  /* Пока шёл запрос, мастер могли закрыть или сменить способ. */
  if ($('#setup-view').hidden || wizard.step !== 0 || wizard.method !== 'yandex' || yandexAuth.status !== 'starting') {
    if (data && data.flowId) deleteYandexFlow(String(data.flowId));
    return;
  }
  const retryLabel = yandexAuth.hasCode ? 'Получить новый код' : 'Получить код';
  if (failure) {
    failYandex(yandexStartFailureMessage(failure), { retryLabel });
    return;
  }
  if (!data || !data.flowId || !data.userCode) {
    failYandex('Сервер вернул неполные данные. Попробуйте ещё раз.', { retryLabel });
    return;
  }
  const lifetime = Number(data.expiresIn) > 0 ? Math.min(600, Math.max(1, Math.floor(Number(data.expiresIn)))) : 600;
  yandexAuth.status = 'waiting';
  yandexAuth.flowId = String(data.flowId);
  yandexAuth.userCode = String(data.userCode);
  yandexAuth.verificationUrl = safeHttpUrl(data.verificationUrl, YANDEX_DEVICE_URL);
  yandexAuth.expiresAt = Date.now() + lifetime * 1000;
  yandexAuth.remaining = lifetime;
  yandexAuth.hasCode = true;
  yandexAuth.retryLabel = 'Получить новый код';
  renderSetup({ focusTitle: false });
  startYandexTimers();
  /* Фокус — на код: он главный элемент экрана и читается целиком. */
  const code = document.getElementById('yandex-code');
  if (code) code.focus({ preventScroll: true });
}

function startYandexTimers() {
  clearYandexTimers();
  updateYandexTimer();
  yandexAuth.tickTimer = setInterval(updateYandexTimer, 1000);
  scheduleYandexPoll(YANDEX_POLL_MS);
}

function updateYandexTimer() {
  if (yandexAuth.status !== 'waiting') return;
  const remaining = Math.max(0, Math.ceil((yandexAuth.expiresAt - Date.now()) / 1000));
  yandexAuth.remaining = remaining;
  const value = document.getElementById('yandex-timer-value');
  if (value) value.textContent = formatCountdown(remaining);
  if (remaining <= 0) expireYandexCode();
}

function scheduleYandexPoll(delay = YANDEX_POLL_MS) {
  if (yandexAuth.pollTimer) clearTimeout(yandexAuth.pollTimer);
  yandexAuth.pollTimer = setTimeout(pollYandexDevice, delay);
}

async function pollYandexDevice() {
  yandexAuth.pollTimer = null;
  if (yandexAuth.status !== 'waiting' || !yandexAuth.flowId || yandexAuth.pollInFlight) return;
  const flowId = yandexAuth.flowId;
  const seq = ++yandexAuth.pollSeq;
  yandexAuth.pollInFlight = true;
  let data = null;
  let error = null;
  try {
    data = await api(`/api/oauth/yandex/device/${encodeURIComponent(flowId)}`, { timeout: API_TIMEOUT.read });
  } catch (caught) {
    error = caught;
  } finally {
    yandexAuth.pollInFlight = false;
  }
  /* Ответ устарел: поток отменили, истёк или уже завершился. */
  if (seq !== yandexAuth.pollSeq || yandexAuth.status !== 'waiting' || yandexAuth.flowId !== flowId) return;
  if (error) {
    const status = Number(error.status) || 0;
    if (status === 404) {
      failYandex('Код истёк', { expired: true, hasCode: true, retryLabel: 'Получить новый код' });
      return;
    }
    if (status === 429) {
      failYandex('Слишком много попыток, подождите минуту.', { hasCode: true, retryLabel: 'Получить новый код' });
      return;
    }
    if (status === 0 || status >= 500) {
      /* Сеть моргнула: поток на сервере жив, просто продолжаем ждать. */
      setYandexLive('Нет связи. Повторяем…');
      scheduleYandexPoll(YANDEX_POLL_MS);
      return;
    }
    failYandex(friendlyError(error), { hasCode: true, retryLabel: 'Получить новый код' });
    return;
  }
  const status = String((data && data.status) || '');
  if (status === 'done') {
    await completeYandexFlow(data && data.account);
    return;
  }
  if (status === 'error') {
    const message = clearServerText(data && data.message) || 'Яндекс не подтвердил вход. Попробуйте ещё раз.';
    failYandex(message, { hasCode: true, retryLabel: 'Получить новый код' });
    return;
  }
  setYandexLive('Ждём подтверждение…');
  scheduleYandexPoll(YANDEX_POLL_MS);
}

function expireYandexCode() {
  if (yandexAuth.status !== 'waiting') return;
  const flowId = yandexAuth.flowId;
  if (flowId) deleteYandexFlow(flowId);
  failYandex('Код истёк', { expired: true, hasCode: true, retryLabel: 'Получить новый код' });
}

/* Ошибка или истечение: останавливаем таймеры, показываем сообщение и
   возвращаем фокус на кнопку повторного получения кода. */
function failYandex(message, options = {}) {
  clearYandexTimers();
  yandexAuth.pollSeq += 1;
  yandexAuth.pollInFlight = false;
  yandexAuth.flowId = '';
  yandexAuth.status = options.expired ? 'expired' : 'error';
  yandexAuth.message = message || '';
  if (options.hasCode !== undefined) yandexAuth.hasCode = options.hasCode;
  yandexAuth.retryLabel = options.retryLabel || (yandexAuth.hasCode ? 'Получить новый код' : 'Получить код');
  if ($('#setup-view').hidden || wizard.step !== 0 || wizard.method !== 'yandex') return;
  renderSetup({ focusTitle: false });
  focusYandexRetry();
}

/* Подтверждение получено: аккаунт уже создан на сервере, поэтому второй
   POST /api/accounts не отправляем — переходим к выбору папки. */
async function completeYandexFlow(account) {
  if (yandexAuth.status !== 'waiting') return;
  yandexAuth.status = 'done';
  yandexAuth.flowId = '';
  yandexAuth.pollSeq += 1;
  clearYandexTimers();
  setYandexLive('Вход подтверждён. Переходим к выбору папки…');
  let target = account && account.id ? account : null;
  if (!target) {
    /* Ответ без аккаунта: подключение уже создано, просто перечитываем. */
    try { await loadData(); } catch { /* покажем сообщение ниже */ }
    target = currentAccount();
  }
  if (!target || !target.id) {
    showToast({
      type: 'warn', id: 'yandex-account',
      title: 'Вход подтверждён',
      message: 'Не удалось прочитать подключение. Вернитесь на главный экран и откройте мастер снова.',
    });
    location.hash = '#/';
    return;
  }
  try {
    await finishConnect(target);
  } catch {
    showToast({
      type: 'warn', id: 'yandex-followup',
      title: 'Вход подтверждён',
      message: 'Экран не обновился. Вернитесь на главный экран и обновите страницу.',
    });
    location.hash = '#/';
  }
}

function cancelYandexAttempt() {
  if (yandexAuth.status !== 'waiting') return;
  stopYandexFlow({ cancel: true });
  resetYandexState();
  renderSetup({ focusTitle: false });
  const start = document.getElementById('yandex-start');
  if (start) start.focus({ preventScroll: true });
  announce('Вход через Яндекс отменён');
}

/* Показ/скрытие пароля: type переключается на месте, autocomplete сохраняется. */
function togglePassword() {
  const input = document.getElementById('f-pass');
  const button = document.getElementById('f-pass-toggle');
  if (!input || !button) return;
  const reveal = input.type === 'password';
  input.type = reveal ? 'text' : 'password';
  button.setAttribute('aria-pressed', reveal ? 'true' : 'false');
  button.setAttribute('aria-label', reveal ? 'Скрыть пароль' : 'Показать пароль');
  button.title = reveal ? 'Скрыть пароль' : 'Показать пароль';
  button.innerHTML = icon(reveal ? 'eyeOff' : 'eye');
  input.focus({ preventScroll: true });
}

/* Инструкция «Как получить токен?» раскрывается на месте, без ухода со страницы. */
function toggleTokenHelp() {
  const help = document.getElementById('token-help');
  const button = document.getElementById('token-help-toggle');
  if (!help || !button) return;
  const open = help.hidden;
  help.hidden = !open;
  wizard.tokenHelp = open;
  button.setAttribute('aria-expanded', open ? 'true' : 'false');
  if (open) announce('Инструкция, как получить токен');
}

async function copyTokenCommand(trigger) {
  if (isBusyControl(trigger)) return;
  setBusy(trigger, true);
  try {
    await copyText(TOKEN_COMMAND);
    showToast({ type: 'ok', title: 'Команда скопирована', message: 'Выполните её в терминале и вставьте JSON из ответа.' });
  } catch {
    showToast({
      type: 'error', title: 'Не удалось скопировать',
      message: 'Выделите команду в инструкции и скопируйте вручную.',
    });
  } finally {
    if (trigger && trigger.isConnected) setBusy(trigger, false);
  }
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
  const label = accountLabel(account);
  const name = $('#account-chip-name');
  if (name) name.textContent = label;
  const chip = $('#account-chip');
  if (chip) chip.title = accountLogin(account) ? `Аккаунт: ${label}` : label;
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
    const data = await api(`/api/accounts/${encodeURIComponent(account.id)}/probe`, {
      method: 'POST', timeout: API_TIMEOUT.external,
    });
    const about = data.about || {};
    if (data.knownUser && data.login) {
      const probed = String(data.login).trim();
      if (probed) accountLogins.set(account.id, probed);
    }
    if (Number(about.total) > 0 || Number(about.used) > 0) {
      cloudUsage = {
        used: Number(about.used) || 0,
        total: Number(about.total) || 0,
        checkedAt: Date.now(),
        accountId: account.id,
      };
    }
    lastCloudAttempt = Date.now();
    if (data.warning) {
      showToast({ type: 'warn', title: 'Подключение работает', message: data.warning });
    } else {
      showToast({ type: 'ok', title: 'Подключение работает', message: usageText(about) });
    }
    /* Проверка уже подтверждена ответом — сбой обновления данных не должен
       выглядеть как «не удалось проверить подключение». */
    await refreshAfterAction();
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
  /* Пока идёт DELETE, диалог не закрывается ни Esc, ни «Остаться»: операция
     уже отправлена и отменять её нельзя. */
  if (dialog) {
    dialog.setAttribute('aria-busy', 'true');
    setDialogCancelBlocked(dialog, true);
  }
  const button = $('#exit-confirm');
  setBusy(button, true);
  try {
    let failed = null;
    try {
      for (const job of activeJobs()) {
        suppressStatus(job.id, ['interrupted', 'canceled']);
        try { await api(`/api/jobs/${encodeURIComponent(job.id)}/stop`, { method: 'POST', timeout: API_TIMEOUT.mutate }); }
        catch { /* the account delete below will report the real problem */ }
      }
      await api(`/api/accounts/${encodeURIComponent(account.id)}`, { method: 'DELETE', timeout: API_TIMEOUT.mutate });
    } catch (error) {
      failed = error;
    }
    if (failed) {
      /* Ошибка открытого диалога — внутри диалога, не тостом под top layer. */
      if (error && dialog && dialog.open) {
        error.hidden = false;
        error.textContent = friendlyError(failed);
      } else {
        showToast({ type: 'error', title: 'Не удалось выйти из Яндекс Диска', message: friendlyError(failed) });
      }
      return;
    }
    /* DELETE подтверждён сервером: локальное состояние обновляем сразу, чтобы
       интерфейс не показывал чужой аккаунт, даже если полное обновление
       данных затем не удастся. */
    cloudUsage = null;
    lastCloudAttempt = 0;
    state.accounts = (state.accounts || []).filter((item) => item.id !== account.id);
    state.jobs = (state.jobs || []).filter((job) => job.accountId !== account.id);
    if (dialog && dialog.open) dialog.close();
    render();
    showToast({ type: 'ok', title: 'Вы вышли из Яндекс Диска', message: 'Подключить снова можно в любой момент.' });
    const plus = $('.plus-tile');
    if (plus) plus.focus({ preventScroll: true });
    await refreshAfterAction();
  } finally {
    exitBusy = false;
    if (button && button.isConnected) setBusy(button, false);
    if (dialog) {
      dialog.removeAttribute('aria-busy');
      setDialogCancelBlocked(dialog, false);
    }
  }
}

/* --- storage informer ----------------------------------------------------- */

function serverRoot() {
  const roots = Array.isArray(system.roots) ? system.roots : [];
  const dest = (flowDest || preferredDest || state.downloads || '/downloads').trim();
  const match = roots.find((root) => {
    if (!root || !root.path) return false;
    const base = String(root.path).replace(/\/+$/, '');
    return dest === base || dest.startsWith(`${base}/`);
  });
  return match || roots[0] || null;
}

/* Сводка /api/system относится к конкретному аккаунту: данные другого
   подключения или помеченные stale не выдаём за актуальные. */
function systemCloudMatchesAccount(account, cloud = system && system.cloud) {
  if (!cloud) return false;
  if (!account) return false;
  if (!cloud.accountId) return true;
  return cloud.accountId === account.id;
}

/* Cloud usage comes from GET /api/system (background cache, no per-render
   network); the probe is only a fallback for when that cache has no numbers. */
function systemCloudUsage() {
  const cloud = system && system.cloud;
  const account = currentAccount();
  if (!cloud || cloud.status !== 'ok' || !systemCloudMatchesAccount(account, cloud)) return null;
  const total = cloud.total == null ? null : Number(cloud.total);
  const used = cloud.used == null ? null : Number(cloud.used);
  if (total == null && used == null) return null;
  return {
    used: Number.isFinite(used) ? used : 0,
    total: Number.isFinite(total) ? total : 0,
    stale: !!cloud.stale,
  };
}

/* cloudUsage живёт для одного аккаунта: при смене подключения и при
   needs-auth сбрасываем, чтобы не показать чужие цифры. */
function syncCloudUsageAccount() {
  const account = currentAccount();
  if (!cloudUsage) return;
  if (!account || cloudUsage.accountId !== account.id) cloudUsage = null;
}

function ensureCloudUsage(force = false) {
  const account = currentAccount();
  if (!account) return;
  const systemCloud = system && system.cloud;
  const systemMatches = systemCloudMatchesAccount(account, systemCloud);
  if (!force) {
    const cached = systemCloudUsage();
    if (cached && (cached.total > 0 || cached.used > 0)) {
      /* Устаревший снимок показываем как последнее известное значение,
         но не считаем его свежим и не блокируем проверку. */
      cloudUsage = {
        used: cached.used, total: cached.total,
        checkedAt: cached.stale ? 0 : Date.now(),
        stale: cached.stale,
        accountId: account.id,
        fromSystem: true,
      };
      if (!cached.stale) return;
      if (Date.now() - lastCloudAttempt < 60000) return;
    }
    if (systemMatches && systemCloud && systemCloud.status === 'needs-auth') return;
  }
  if (cloudBusy) return;
  if (account.status === 'needs-auth') return;
  if (!force && Date.now() - lastCloudAttempt < 60000) return;
  if (!force && cloudUsage && cloudUsage.checkedAt && Date.now() - cloudUsage.checkedAt < 60000) return;
  cloudBusy = true;
  lastCloudAttempt = Date.now();
  api(`/api/accounts/${encodeURIComponent(account.id)}/probe`, { method: 'POST', timeout: API_TIMEOUT.external })
    .then((data) => {
      const about = data && data.about;
      if (about && currentAccount() && currentAccount().id === account.id) {
        cloudUsage = {
          used: Number(about.used) || 0,
          total: Number(about.total) || 0,
          checkedAt: Date.now(),
          stale: false,
          accountId: account.id,
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

/* Папка, выбранная в мастере, становится начальным назначением шага 2:
   homeMarkup при повторном входе не пересобирается. */
function applyPreferredDest() {
  if (!preferredDest || flowDest) return;
  flowDest = displayPath(preferredDest);
  renderStorage();
}

/* Пара «138 из 214 ГБ» для плитки: число крупно, остальное — мелкой строкой.
   Единица выбирается по большему значению, чтобы пара читалась целиком. */
function usageValuePair(used, total) {
  const units = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ'];
  let unit = 0;
  let base = Math.max(Number(total) || 0, Number(used) || 0);
  while (base >= 1024 && unit < units.length - 1) { base /= 1024; unit += 1; }
  const format = (value) => {
    const number = (Number(value) || 0) / (1024 ** unit);
    return number.toFixed(unit === 0 ? 0 : 1).replace('.', ',').replace(/,0$/, '');
  };
  const totalValue = Math.max(0, Number(total) || 0);
  return {
    used: format(used),
    rest: totalValue > 0 ? `из ${format(totalValue)}\u00A0${units[unit]}` : units[unit],
  };
}

/* Значение плитки: крупное «138» и «из 214 ГБ» справа; подпись — свободно. */
function setStorageValue(valueNode, noteNode, used, total, options = {}) {
  if (!valueNode) return;
  const safeUsed = Math.max(0, Number(used) || 0);
  const safeTotal = Math.max(0, Number(total) || 0);
  const pair = usageValuePair(safeUsed, safeTotal);
  if (safeTotal > 0) {
    valueNode.innerHTML = `<strong>${pair.used}</strong> <span class="storage-tile-total">${pair.rest}</span>`;
    valueNode.title = `Занято ${humanBytes(safeUsed)} из ${humanBytes(safeTotal)}${options.stale ? ' · данные могли устареть' : ''}`;
  } else {
    valueNode.innerHTML = `<strong>${pair.used}\u00A0${pair.rest}</strong>`;
    valueNode.title = `Занято ${humanBytes(safeUsed)}`;
  }
  if (!noteNode) return;
  const free = options.free != null
    ? Number(options.free)
    : (safeTotal > 0 ? safeTotal - safeUsed : null);
  const note = Number.isFinite(free) && free >= 0 ? `свободно ${humanBytes(free)}` : '';
  noteNode.textContent = note;
  noteNode.hidden = !note;
  noteNode.title = note ? `${note.charAt(0).toUpperCase()}${note.slice(1)}` : '';
}

function renderStorage() {
  const cloudValue = $('#cloud-usage');
  const cloudBar = $('#cloud-bar');
  const cloudNote = $('#cloud-note');
  const serverValue = $('#server-usage');
  const serverBar = $('#server-bar');
  const serverNote = $('#server-note');
  if (!cloudValue || !serverValue) return;

  const account = currentAccount();
  const systemCloud = system && system.cloud;
  const systemMatches = systemCloudMatchesAccount(account, systemCloud);
  const needsAuth = !!account
    && (account.status === 'needs-auth' || (systemMatches && systemCloud.status === 'needs-auth'));
  if (needsAuth) {
    cloudUsage = null;
  }
  if (cloudUsage && (!account || cloudUsage.accountId !== account.id)) cloudUsage = null;
  if (needsAuth) {
    cloudValue.textContent = 'Нужен вход';
    cloudValue.title = '';
    if (cloudNote) { cloudNote.textContent = ''; cloudNote.hidden = true; }
    if (cloudBar) cloudBar.style.width = '0%';
  } else if (cloudUsage && (cloudUsage.total > 0 || cloudUsage.used > 0)) {
    setStorageValue(cloudValue, cloudNote, cloudUsage.used, cloudUsage.total, { stale: cloudUsage.stale });
    if (cloudBar) {
      const percent = cloudUsage.total > 0 ? Math.min(100, Math.round((cloudUsage.used / cloudUsage.total) * 100)) : 0;
      cloudBar.style.width = `${percent}%`;
    }
  } else {
    cloudValue.textContent = '—';
    cloudValue.title = '';
    if (cloudNote) { cloudNote.textContent = ''; cloudNote.hidden = true; }
    if (cloudBar) cloudBar.style.width = '0%';
  }

  const root = serverRoot();
  if (root && Number.isFinite(Number(root.free))) {
    const free = Number(root.free);
    const total = Number(root.total);
    const knownTotal = Number.isFinite(total) && total > 0;
    const used = knownTotal ? Math.max(0, total - free) : 0;
    setStorageValue(serverValue, serverNote, used, knownTotal ? total : 0, { free });
    if (serverBar) {
      const percent = knownTotal ? Math.min(100, Math.round((used / total) * 100)) : 0;
      serverBar.style.width = `${percent}%`;
    }
  } else {
    serverValue.textContent = '—';
    serverValue.title = '';
    if (serverNote) { serverNote.textContent = ''; serverNote.hidden = true; }
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

/* Источник выбранного пути: «Весь Диск» или «3 папки». Пустой список папок
   без включённого «Весь диск» трактуем как весь диск — так же, как API. */
function flowSourceText() {
  if (flowSourceAll || !cloudSelection.length) return 'Весь Диск';
  return pluralFolders(cloudSelection.length);
}

function flowSourcePaths() {
  return flowSourceAll ? [] : cloudSelection.slice();
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
    flowSourceAll = true;
    flowShowExisting = false;
    cloudSelectionAccount = account ? account.id : '';
    flowPreflight = { key: '', status: 'idle', data: null, error: null };
    resetExistingCache();
  }
}

/* --- home: cells ---------------------------------------------------------- */

/* Задания другого аккаунта не показываем и не останавливаем «за текущий»:
   после смены подключения старые задания могут ещё лежать в состоянии. */
function accountJobs() {
  const account = currentAccount();
  if (!account) return [];
  return state.jobs.filter((job) => !job.accountId || job.accountId === account.id);
}

function activeJobs() {
  return accountJobs()
    .filter((job) => ACTIVE_STATUSES.includes(job.status))
    .sort((a, b) => {
      const order = { running: 0, verifying: 0, queued: 1 };
      return (order[a.status] ?? 2) - (order[b.status] ?? 2);
    });
}

function latestJob() {
  const jobs = accountJobs();
  return jobs.length ? jobs[0] : null;
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
  /* Пока rclone не знает общий объём, процент 0 % выглядит зависанием:
     честно говорим, что объём ещё считается. */
  if (job.status === 'running' && !(Number(stats.totalBytes) > 0)) parts.push('Считаем объём…');
  if (Number(stats.speed) > 0) parts.push(`${humanBytes(stats.speed)}/с`);
  if (Number(stats.eta) > 0) parts.push(`≈ ${humanEta(stats.eta)}`);
  if (!parts.length) parts.push('Идёт скачивание');
  return parts.join(' · ');
}

/* Время задания: elapsedTime от rclone, иначе разница отметок. */
function jobElapsedSeconds(job) {
  const stats = job.stats || {};
  const elapsed = Number(stats.elapsedTime) || 0;
  if (elapsed > 0) return elapsed;
  const started = new Date(job.startedAt || job.createdAt || 0).getTime();
  const finished = new Date(job.finishedAt || Date.now()).getTime();
  if (!(started > 0) || !(finished > started)) return 0;
  return Math.round((finished - started) / 1000);
}

/* Итог «Готово»: файлов, объём, время. Пустые значения не выдумываем. */
function jobDoneFacts(job) {
  const stats = job.stats || {};
  const files = Math.max(0, Number(stats.transfers) || Number(stats.totalTransfers) || 0);
  const bytes = Math.max(0, Number(stats.totalBytes) || Number(stats.bytes) || 0);
  const seconds = jobElapsedSeconds(job);
  const parts = [];
  if (files > 0) parts.push(formatFiles(files));
  if (bytes > 0) parts.push(humanBytes(bytes));
  if (seconds > 0 && job.finishedAt) parts.push(humanEta(seconds));
  return parts.join(' · ');
}

/* Неудачные файлы видны в «Сейчас»: не только копирование в буфер.
   Список ограничен по высоте и прокручивается внутри, длинные пути
   переносятся. */
function failureListMarkup(failures, failureCount) {
  if (!failureCount) return '';
  const items = failures.slice(0, 200);
  const list = items.length
    ? `<ul class="now-failure-list">${items.map((path) => `<li title="${escapeHtml(displayPath(path))}">${escapeHtml(displayPath(path))}</li>`).join('')}</ul>`
    : '<p class="now-failure-more">Список файлов недоступен.</p>';
  const more = failureCount > items.length
    ? `<p class="now-failure-more">Показаны первые ${items.length} из ${failureCount}.</p>`
    : '';
  return `
    <div class="now-failures">
      <p class="now-failures-text">Не скачались: ${failureCount}</p>
      ${list}
      ${more}
    </div>`;
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

    <section class="cell cell--start" aria-labelledby="start-title">
      <div class="cell-head">
        ${icon('transfer')}
        <h2 class="cell-title" id="start-title">Скачать</h2>
      </div>
      <div class="cell-body start-body">
        <p class="start-note">Из Яндекс Диска на сервер Umbrel: куда сохранить, что скачать и проверка.</p>
        <div class="start-row">
          <button class="btn btn--primary btn--start" type="button" id="flow-start" data-action="start-flow">${icon('transfer')}<span>Скачать</span></button>
          <span class="start-hint">Файлы в облаке не удаляются</span>
        </div>
      </div>
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
            <span class="storage-tile-value" id="cloud-usage">—</span>
          </div>
          <span class="meter-bar" aria-hidden="true"><i id="cloud-bar"></i></span>
          <span class="storage-tile-note" id="cloud-note" hidden></span>
        </div>
        <div class="storage-tile">
          <div class="storage-tile-head">
            <span class="storage-tile-icon" aria-hidden="true">${icon('storage')}</span>
            <span class="storage-tile-label">Сервер Umbrel</span>
            <span class="storage-tile-value" id="server-usage">—</span>
          </div>
          <span class="meter-bar" aria-hidden="true"><i id="server-bar"></i></span>
          <span class="storage-tile-note" id="server-note" hidden></span>
        </div>
      </div>
    </section>`;
}

function emptyMarkup() {
  return `
    <section class="plus-cell">
      <button class="plus-tile" type="button" data-action="connect" aria-label="Подключить диск">
        <span class="plus-icon" aria-hidden="true">${icon('plus')}</span>
        <span class="plus-title">Подключить диск</span>
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
    syncCloudUsageAccount();
    renderNow();
    ensureCloudUsage();
    renderStorage();
    if (focusStartAfterRender) {
      focusStartAfterRender = false;
      applyPreferredDest();
      const button = $('#flow-start');
      if (button) button.focus({ preventScroll: true });
    }
  }
}

function nowKey(mode, job, activeCount, account) {
  return [mode, job ? job.id : '', job ? job.status : '', activeCount, account ? account.status : ''].join('|');
}

function nowHead(right = '') {
  /* tabindex="-1" делает заголовок стабильным якорем фокуса: при смене
     режима кнопка, на которой был фокус, может исчезнуть. */
  return `
    <div class="cell-head">
      ${icon('clock')}
      <h2 class="cell-title" id="now-title" tabindex="-1">Сейчас</h2>
      <span class="now-status">${right}</span>
    </div>`;
}

function nowMarkup(mode, job, activeCount, account) {
  if (mode === 'active') {
    const percent = percentOf(job);
    const stats = job.stats || {};
    const unknownTotal = job.status === 'running' && !(Number(stats.totalBytes) > 0);
    const [label, pill] = statusOf(job);
    const live = (job.status === 'running' || job.status === 'verifying') && !unknownTotal;
    const counter = activeCount > 1 ? ` · ${activeCount} в работе` : '';
    /* Очередь тоже можно остановить; проверку целостности API прервать не даёт. */
    const canPause = job.status === 'running' || job.status === 'queued';
    const pause = canPause
      ? `<button class="btn btn--secondary" type="button" data-action="stop" data-id="${escapeHtml(job.id)}" data-focus-key="stop">${icon('pause')}Приостановить</button>`
      : '';
    const percentText = unknownTotal ? 'Считаем…' : `${percent}\u00A0%`;
    return `
      ${nowHead(`<span class="status-pill ${pill}">${escapeHtml(label)}</span>`)}
      <div class="cell-body now-body">
        <div class="now-top-row">
          <span class="now-percent${unknownTotal ? ' now-percent--unknown' : ''}" id="now-percent">${percentText}</span>
          ${pause}
        </div>
        <div class="progress now-progress${live ? ' progress--live' : ''}${unknownTotal ? ' progress--indeterminate' : ''}" id="now-progress" role="progressbar" aria-label="Прогресс скачивания" aria-valuemin="0" aria-valuemax="100"${unknownTotal ? '' : ` aria-valuenow="${percent}"`} aria-valuetext="${unknownTotal ? 'Объём ещё считается' : `${percent}\u00A0%`}"><i id="now-bar" style="width:${percent}%"></i></div>
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
    const pillLabel = failed ? 'Не удалось' : 'Пауза';
    const title = failed ? (job.errorTitle || 'Скачивание не завершено') : 'Скачивание приостановлено';
    const note = failed
      ? (job.errorAdvice || 'Проверьте место на сервере и повторите.')
      : 'Продолжите — скачанные файлы не будут скачаны снова.';
    const failureCount = Math.max(0, Number(job.failureCount) || 0);
    const failures = Array.isArray(job.failures) ? job.failures.filter(Boolean) : [];
    const partial = failed ? failureListMarkup(failures, failureCount) : '';
    const copy = failed && failureCount > 0
      ? `<button class="btn btn--secondary btn--sm" type="button" data-action="copy-failures" data-id="${escapeHtml(job.id)}" data-focus-key="copy-failures">${icon('copy')}Скопировать список</button>`
      : '';
    return `
      ${nowHead(`<span class="status-pill ${pill}">${pillLabel}</span>`)}
      <div class="cell-body">
        <div class="now-row">
          <div class="now-text">
            <p class="now-title">${escapeHtml(title)}</p>
            <p class="now-note">${escapeHtml(note)}</p>
          </div>
        </div>
        ${partial}
        <div class="now-actions">
          <button class="btn btn--primary" type="button" data-action="resume" data-id="${escapeHtml(job.id)}" data-focus-key="resume">${icon('resume')}${failed ? 'Повторить' : 'Продолжить'}</button>
          ${copy}
        </div>
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
    const facts = jobDoneFacts(job);
    return `
      ${nowHead('<span class="status-pill status-pill--ok">Готово</span>')}
      <div class="cell-body now-done">
        ${facts ? `<p class="now-summary" id="now-summary">${escapeHtml(facts)}</p>` : ''}
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

  return `
    ${nowHead()}
    <div class="cell-body">
      <div class="now-text now-text--idle">
        <p class="now-title">Скачиваний пока нет</p>
        <p class="now-note">Начните с кнопки «Скачать»: три шага и проверка перед запуском.</p>
      </div>
    </div>`;
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
  const focusWasInside = !!(focused && focused !== cell && cell.contains(focused));
  const focusKey = (focusWasInside && focused.dataset ? focused.dataset.focusKey || '' : '') || pendingFocusKey;
  pendingFocusKey = '';
  cell.dataset.key = key;
  cell.dataset.mode = mode;
  cell.innerHTML = nowMarkup(mode, job, active.length, account);
  const anchor = () => {
    const title = cell.querySelector('#now-title');
    if (title) title.focus({ preventScroll: true });
  };
  if (focusKey) {
    /* После «Приостановить» логично вести фокус на «Продолжить», но если
       такого действия в новом режиме нет — на заголовок региона, а не body. */
    const fallbackKey = focusKey === 'stop' ? 'resume' : '';
    const target = cell.querySelector(`[data-focus-key="${cssEscape(focusKey)}"]`)
      || (fallbackKey ? cell.querySelector(`[data-focus-key="${cssEscape(fallbackKey)}"]`) : null);
    if (target) target.focus({ preventScroll: true });
    else anchor();
  } else if (focusWasInside) {
    /* running → done/error: кнопки исчезли, но пользователь не должен
       оказаться на body. */
    anchor();
  }
}

function updateNowValues(job) {
  const percent = percentOf(job);
  const stats = job.stats || {};
  const unknownTotal = job.status === 'running' && !(Number(stats.totalBytes) > 0);
  const percentElement = $('#now-percent');
  if (percentElement) {
    percentElement.textContent = unknownTotal ? 'Считаем…' : `${percent}\u00A0%`;
    percentElement.classList.toggle('now-percent--unknown', unknownTotal);
  }
  const bar = $('#now-bar');
  if (bar) bar.style.width = `${percent}%`;
  const progress = $('#now-progress');
  if (progress) {
    progress.classList.toggle('progress--indeterminate', unknownTotal);
    progress.classList.toggle('progress--live', !unknownTotal);
    if (unknownTotal) {
      progress.removeAttribute('aria-valuenow');
      progress.setAttribute('aria-valuetext', 'Объём ещё считается');
    } else {
      progress.setAttribute('aria-valuenow', String(percent));
      progress.setAttribute('aria-valuetext', `${percent}\u00A0%`);
    }
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
    /* Тост объявляет себя через #live — второго announce не нужно. */
    showToast({ type: 'ok', title: 'Скачивание завершено', message: `${jobSourceLabel(job)} · ${displayPath(job.dest)}` });
    return;
  }
  if (job.status === 'failed') {
    showToast({
      type: 'error', id: `job-${job.id}`,
      title: job.errorTitle || 'Скачивание не завершено',
      message: `${jobSourceLabel(job)} · ${displayPath(job.dest)}. ${job.errorAdvice || 'Проверьте место на сервере и повторите.'}`,
      action: { label: 'Повторить', onClick: () => resumeJob(job.id, null) },
    });
    return;
  }
  if (job.status === 'interrupted' || job.status === 'canceled') {
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
  let data = null;
  let failed = null;
  try {
    data = await api(`/api/jobs/${encodeURIComponent(id)}/stop`, { method: 'POST', timeout: API_TIMEOUT.mutate });
  } catch (error) {
    failed = error;
  } finally {
    jobActionBusy.delete(busyKey);
    if (trigger && trigger.isConnected) setBusy(trigger, false);
  }
  if (failed) {
    const skip = suppressed.get(id);
    if (skip) skip.clear();
    throw failed;
  }
  /* Ответ 200 — операция подтверждена. Ниже только обновление UI: его сбой
     не превращается в «не удалось приостановить». */
  const job = state.jobs.find((item) => item.id === id);
  pendingFocusKey = trigger && trigger.dataset ? (trigger.dataset.focusKey || '') : '';
  try {
    showToast({
      type: data.warning ? 'warn' : 'ok',
      title: data.warning ? 'Скачивание приостановлено не сразу' : 'Скачивание приостановлено',
      message: data.warning || (job ? `${jobSourceLabel(job)} · ${displayPath(job.dest)}` : ''),
    });
    await refreshAfterAction();
  } finally {
    pendingFocusKey = '';
  }
}

async function resumeJob(id, trigger) {
  const busyKey = `resume:${id}`;
  if (jobActionBusy.has(busyKey)) return;
  jobActionBusy.add(busyKey);
  setBusy(trigger, true);
  let failed = null;
  try {
    await api(`/api/jobs/${encodeURIComponent(id)}/resume`, { method: 'POST', timeout: API_TIMEOUT.mutate });
  } catch (error) {
    failed = error;
  } finally {
    jobActionBusy.delete(busyKey);
    if (trigger && trigger.isConnected) setBusy(trigger, false);
  }
  if (failed) throw failed;
  const job = state.jobs.find((item) => item.id === id);
  pendingFocusKey = trigger && trigger.dataset ? (trigger.dataset.focusKey || '') : '';
  try {
    showToast({
      type: 'ok', title: 'Скачивание продолжено',
      message: job
        ? `${jobSourceLabel(job)} · ${displayPath(job.dest)}. Скачанные файлы не будут скачаны снова.`
        : 'Скачанные файлы не будут скачаны снова.',
    });
    await refreshAfterAction();
  } finally {
    pendingFocusKey = '';
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
  if (!job || isBusyControl(trigger)) return;
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

/* --- П2: три шага и проверка ---------------------------------------------- */

function openFlowStart(trigger) {
  const account = currentAccount();
  if (!account) return;
  if (account.status === 'needs-auth') {
    showToast({
      type: 'warn', title: 'Нужно войти заново',
      message: 'Доступ к облаку истёк — войдите и повторите скачивание.',
      action: { label: 'Войти заново', onClick: () => { location.hash = '#/setup'; } },
    });
    return;
  }
  flowOpener = trigger instanceof HTMLElement ? trigger : $('#flow-start');
  flowStep = 1;
  /* Новый вход в путь — новый источник: старые ответы фильтра не храним. */
  flowShowExisting = false;
  resetExistingCache();
  openFolderDialog('server', flowOpener, 'flow-dest');
}

function flowReviewKey() {
  return `${flowSourceAll ? 'all' : 'folders'}:${cloudSelection.join('\n')}|${flowDest}`;
}

function clearReviewError() {
  const node = $('#review-error');
  if (!node) return;
  node.hidden = true;
  node.textContent = '';
}

/* Ошибка запуска — внутри диалога, видимая и озвученная (role="alert"). */
function reviewError(message) {
  const node = $('#review-error');
  if (!node) return;
  node.textContent = message || '';
  node.hidden = !message;
  if (message) node.focus({ preventScroll: true });
}

function reviewRow(label, value) {
  return `<div class="review-row">
    <dt>${label}</dt>
    <dd>${value}</dd>
  </div>`;
}

function reviewEstimateMarkup(free) {
  const state = flowPreflight;
  if (state.status === 'loading') {
    return '<p class="review-sub" aria-busy="true">Считаем файлы в облаке… Это может занять время.</p>';
  }
  if (state.status === 'error') {
    return '<p class="review-sub">Объём узнать не удалось. Выбор сохранился — попробуйте ещё раз.</p>';
  }
  if (state.status !== 'ready') {
    return '<p class="review-sub">Не посчитан. Нажмите «Узнать объём» — покажем файлы, объём и сравнение со свободным местом.</p>';
  }
  const data = state.data || {};
  const files = Math.max(0, Number(data.files) || 0);
  const bytes = Math.max(0, Number(data.bytes) || 0);
  const dataFree = Number(data.free) > 0 ? Number(data.free) : free;
  let fit = '';
  if (bytes <= 0) {
    fit = '<p class="review-sub">Файлов не найдено — возможно, выбранные папки пусты.</p>';
  } else if (data.fits === false) {
    fit = `<p class="review-warning" role="alert">Не поместится: нужно ${humanBytes(bytes)}, свободно ${humanBytes(dataFree)}. Освободите место или выберите другую папку.</p>`;
  } else if (Number(dataFree) > 0) {
    fit = `<p class="review-ok">Поместится: свободно ${humanBytes(dataFree)}.</p>`;
  } else {
    fit = '<p class="review-sub">Свободное место на сервере неизвестно — объём посчитан, но запас проверить нельзя.</p>';
  }
  return `<p class="review-value">${formatFiles(files)} · ${humanBytes(bytes)}</p>${fit}`;
}

function renderReview() {
  const body = $('#review-body');
  if (!body) return;
  const preflightFree = flowPreflight.status === 'ready' && Number(flowPreflight.data && flowPreflight.data.free) > 0
    ? Number(flowPreflight.data.free)
    : flowFree;
  const free = Number.isFinite(Number(preflightFree)) ? Number(preflightFree) : null;
  const paths = flowSourceAll ? [] : cloudSelection;
  const source = `<span class="review-value">${escapeHtml(flowSourceText())}</span>`
    + (paths.length
      ? `<span class="review-paths">${paths.map((path) => `<span class="review-path-chip" title="${escapeHtml(displayCloudPath(path))}">${escapeHtml(displayCloudPath(path))}</span>`).join('')}</span>`
      : '<span class="review-sub">Все папки и файлы</span>');
  body.innerHTML = `
    <dl class="review-list">
      ${reviewRow('Источник', source)}
      ${reviewRow('Куда', `<span class="review-path" title="${escapeHtml(flowDest)}">${escapeHtml(flowDest)}</span>`)}
      ${reviewRow('Свободно', free != null ? `<span class="review-value">${humanBytes(free)}</span>` : '<span class="review-sub">неизвестно</span>')}
    </dl>
    <div class="review-estimate-box">
      <p class="review-label">Объём</p>
      ${reviewEstimateMarkup(free)}
    </div>
    <p class="review-note">Cloud Ferry только добавляет файлы на сервер — в облаке ничего не удаляется.</p>`;
  const estimate = $('#review-estimate');
  if (estimate && !isBusyControl(estimate)) {
    estimate.textContent = flowPreflight.status === 'idle' ? 'Узнать объём' : 'Обновить объём';
  }
  const error = $('#review-error');
  if (error) {
    const failed = flowPreflight.status === 'error';
    error.hidden = !failed;
    error.textContent = failed ? friendlyError(flowPreflight.error) : '';
  }
}

function openReview() {
  const dialog = $('#review-dialog');
  if (!dialog) return;
  const key = flowReviewKey();
  if (flowPreflight.key !== key) {
    flowPreflight = { key, status: 'idle', data: null, error: null };
  }
  flowStep = 3;
  clearReviewError();
  renderReview();
  if (!dialog.open) dialog.showModal();
  const title = $('#review-title');
  if (title) title.focus({ preventScroll: true });
}

async function reviewEstimate(button) {
  if (flowBusy || flowPreflight.status === 'loading') return;
  const account = currentAccount();
  if (!account) return;
  const key = flowReviewKey();
  flowPreflight = { key, status: 'loading', data: flowPreflight.data, error: null };
  setBusy(button, true);
  const dialog = $('#review-dialog');
  if (dialog) dialog.setAttribute('aria-busy', 'true');
  renderReview();
  let data = null;
  let failure = null;
  try {
    data = await api('/api/preflight', {
      method: 'POST',
      timeout: API_TIMEOUT.preflight,
      body: JSON.stringify({ accountId: account.id, sourcePaths: flowSourcePaths(), destPath: flowDest }),
    });
  } catch (error) {
    failure = error;
  }
  if (button && button.isConnected) setBusy(button, false);
  if (dialog) dialog.removeAttribute('aria-busy');
  /* Пока шёл подсчёт, выбор могли изменить (назад → другая папка):
     устаревший результат не должен подменить актуальный. */
  if (flowPreflight.key !== key) return;
  flowPreflight = failure
    ? { key, status: 'error', data: null, error: failure }
    : { key, status: 'ready', data: data || {}, error: null };
  renderReview();
  if (!dialog || !dialog.open) return;
  if (failure) announce('Не удалось узнать объём. Выбор сохранён.');
  else {
    const bytes = Number((data || {}).bytes) || 0;
    announce(`Объём: ${formatFiles((data || {}).files)} · ${humanBytes(bytes)}`);
  }
}

function finishReviewDialog() {
  resumeAfterDialog();
  if (flowFocusNow) { flowFocusNow = false; return; }
  if (flowBusy) return;
  /* Закрыли проверку — возвращаем фокус на кнопку входа в путь. */
  const target = flowOpener && flowOpener.isConnected && flowOpener.getClientRects().length
    ? flowOpener
    : $('#flow-start');
  if (target && target.isConnected && target.getClientRects().length) focusSoon(target);
}

async function startTransfer(button) {
  if (flowBusy) return;
  const dialog = $('#review-dialog');
  const account = currentAccount();
  if (!account) {
    if (dialog && dialog.open) dialog.close();
    location.hash = '#/setup';
    return;
  }
  clearReviewError();
  if (account.status === 'needs-auth') {
    reviewError('Доступ к облаку истёк. Войдите заново и повторите скачивание.');
    return;
  }
  if (!flowDest) {
    reviewError('Выберите папку на сервере и вернитесь к проверке.');
    return;
  }
  const sourcePaths = flowSourcePaths();
  const destPath = flowDest;
  flowBusy = true;
  setBusy(button, true);
  if (dialog) {
    dialog.setAttribute('aria-busy', 'true');
    setDialogCancelBlocked(dialog, true);
  }
  const back = $('#review-back');
  const estimate = $('#review-estimate');
  if (back) back.disabled = true;
  if (estimate) estimate.disabled = true;
  let failure = null;
  try {
    await api('/api/jobs', {
      method: 'POST',
      timeout: API_TIMEOUT.mutate,
      body: JSON.stringify({
        accountId: account.id,
        sourcePaths,
        destPath,
        /* Интерфейс всегда только добавляет и обновляет файлы: mode=copy. */
        mode: 'copy',
      }),
    });
  } catch (error) {
    failure = error;
  } finally {
    flowBusy = false;
    if (button && button.isConnected) setBusy(button, false);
    if (dialog) {
      dialog.removeAttribute('aria-busy');
      setDialogCancelBlocked(dialog, false);
    }
    if (back) back.disabled = false;
    if (estimate) estimate.disabled = false;
  }
  if (failure) {
    /* 201 не пришло — выбор и диалог остаются на месте, ошибка озвучивается. */
    reviewError(`Не удалось начать скачивание. ${friendlyError(failure)}`);
    return;
  }
  /* 201 = скачивание действительно создано. Обновление данных ниже —
     отдельный шаг: его сбой не отменяет запуск. */
  flowFocusNow = true;
  flowSourceAll = true;
  cloudSelection = [];
  cloudSelectionAccount = account.id;
  flowShowExisting = false;
  flowDest = '';
  flowFree = null;
  flowTotal = null;
  flowStep = 1;
  flowPreflight = { key: '', status: 'idle', data: null, error: null };
  resetExistingCache();
  folder.opener = null;
  if (dialog && dialog.open) dialog.close();
  showToast({
    type: 'ok', title: 'Скачивание началось',
    message: `${sourceLabel(sourcePaths)} → ${displayPath(destPath)}`,
  });
  await refreshAfterAction();
  const title = $('#now-title');
  if (title) title.focus({ preventScroll: true });
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

function folderContext() { return folder.context || 'setup'; }
function folderIsFlow() {
  return folder.context === 'flow-source' || folder.context === 'flow-dest';
}

function updateFolderChrome() {
  const cloud = folder.target === 'cloud';
  const sourceStep = folder.context === 'flow-source';
  const destStep = folder.context === 'flow-dest';
  const title = $('#folder-title');
  const note = $('#folder-note');
  const kicker = $('#folder-kicker');
  let titleText = cloud ? 'Выберите папки' : 'Папка на сервере';
  let noteText = cloud ? 'Отметьте папки, которые нужно скачать.' : '';
  if (sourceStep) {
    titleText = 'Что скачать';
    noteText = 'Отметьте папки или включите «Весь диск».';
  } else if (destStep) {
    titleText = 'Куда сохранить';
    noteText = 'Выберите одну папку на сервере — сюда лягут файлы.';
  }
  if (title) title.textContent = titleText;
  if (note) {
    note.hidden = !noteText;
    note.textContent = noteText;
  }
  if (kicker) {
    kicker.hidden = !folderIsFlow();
    kicker.textContent = destStep ? 'Шаг 1 из 3' : (sourceStep ? 'Шаг 2 из 3' : '');
  }
  const dialog = $('#folder-dialog');
  if (dialog) dialog.dataset.step = destStep ? '1' : (sourceStep ? '2' : '');
  const sourceBar = $('#flow-source-bar');
  if (sourceBar) sourceBar.hidden = !sourceStep;
  /* Крошки-строка с текущим путём нужны только пикеру сервера. */
  const crumbs = $('#folder-crumbs');
  if (crumbs) crumbs.hidden = cloud;
  const choose = $('#folder-choose');
  if (choose) choose.textContent = folderIsFlow() ? 'Далее' : (cloud ? 'Сохранить' : 'Выбрать эту папку');
  /* «Назад» есть только на шаге источника: он возвращает к назначению. */
  const back = $('#folder-back');
  if (back) back.hidden = !sourceStep;
  renderFolderFooter();
}

/* Один пикер обслуживает оба шага: в облаке — мультивыбор с чекбоксами,
   на сервере — одна папка. Контекст задаёт заголовок, кнопки и футер. */
function showFolderStep(target, context, opener) {
  const active = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const setupDest = document.getElementById('f-dest');
  const initialDest = ((flowDest || '').trim()
    || (setupDest && setupDest.value.trim())
    || preferredDest
    || state.downloads
    || '/downloads');
  /* Смена папки/шага закрывает текущее превью и отменяет его запрос. */
  cancelCloudFiles();
  folder = {
    target,
    context,
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
    opener: opener || active || flowOpener,
    selected: target === 'cloud' ? cloudSelection.slice() : [],
    all: target === 'cloud' ? !!flowSourceAll : false,
    showExisting: target === 'cloud' ? !!flowShowExisting : false,
    expanded: new Set(),
    nodes: new Map(),
    limitError: '',
    finished: false,
    createOpen: false,
    createBusy: false,
    createUnavailable: folderCreateUnavailable,
    sizes: new Map(),
    sizePending: new Set(),
    /* Фильтр уже скачанных: по умолчанию выключен. Уже существующие папки
       видны, но недоступны; переключатель разрешает их перекачать. */
    existingPending: new Set(),
    existingFailed: new Set(),
    existingError: '',
    preview: null,
    files: new Map(),
    filesInflight: new Map(),
  };
  folderToken += 1;
  /* Возврат на источник с готовым кэшем: уже скачанные папки сразу выпадают
     из выбора, счётчик не показывает недоступные. */
  if (context === 'flow-source') pruneExistingSelection();
  updateFolderChrome();
  renderFolderDialog();
  const dialog = $('#folder-dialog');
  if (dialog && !dialog.open) dialog.showModal();
  const title = $('#folder-title');
  if (title) title.focus({ preventScroll: true });
  if (target === 'cloud') loadCloudNode('');
  else loadFolder(folder.path, false);
}

function openFolderDialog(target, opener, context = 'setup') {
  const account = currentAccount();
  if (target === 'cloud' && !account) return;
  if (target === 'cloud' && account && account.status === 'needs-auth') {
    showToast({
      type: 'warn', title: 'Нужно войти заново',
      message: 'Доступ к облаку истёк — выберите папки после входа.',
      action: { label: 'Войти заново', onClick: () => { location.hash = '#/setup'; } },
    });
    return;
  }
  if (context === 'flow-source' && opener) flowOpener = opener;
  showFolderStep(target, context, opener);
}

function renderFolderCrumbs() {
  const crumbs = $('#folder-crumbs');
  if (!crumbs) return;
  if (folder.target === 'cloud') {
    crumbs.innerHTML = '';
    return;
  }
  /* Пикер сервера: текущий путь — в шапке диалога, рядом кнопка «наверх». */
  const root = folderRootPath();
  const atRoot = folder.path === root || folder.parent == null;
  const current = displayPath(folder.path || root);
  const free = Number.isFinite(Number(folder.free)) ? Number(folder.free) : null;
  const freeText = free != null ? `Свободно ${humanBytes(free)}` : 'Свободно: —';
  crumbs.innerHTML = `
    <div class="folder-location">
      <button class="btn btn--secondary btn--sm folder-up" type="button" data-folder-up aria-label="На уровень выше"${atRoot ? ' disabled' : ''}>${icon('chevronUp')}<span>На уровень выше</span></button>
      <span class="folder-path" title="${escapeHtml(current)}">${escapeHtml(current)}</span>
      <span class="folder-free" id="folder-free" title="${escapeHtml(free != null ? `Свободно на сервере: ${humanBytes(free)}` : 'Свободное место на сервере неизвестно')}">${escapeHtml(freeText)}</span>
    </div>`;
}

/* Шаг 2: «Весь диск», «Показать уже скачанные» и счётчик выбора.
   Переключатели и счётчик живут вне тела списка, поэтому их обновление не
   сбрасывает фокус с чекбоксов. */
function renderFlowSourceBar() {
  if (folder.context !== 'flow-source') return;
  const toggle = $('#folder-all');
  if (toggle) toggle.checked = !!folder.all;
  /* В режиме «Весь диск» фильтр не применяется: скачаем всё, поэтому
     переключатель фильтра и его ошибки не нужны. */
  const showRow = $('#folder-show-existing-row');
  if (showRow) showRow.hidden = folder.all || existingUnavailable;
  const showToggle = $('#folder-show-existing');
  if (showToggle) showToggle.checked = !!folder.showExisting;
  const showNote = $('#folder-show-existing-note');
  if (showNote) {
    showNote.textContent = folder.showExisting
      ? 'Уже скачанные папки можно выбрать — они будут перекачаны.'
      : 'Уже скачанные папки видны, но недоступны.';
  }
  const unavailableNote = $('#folder-existing-note');
  if (unavailableNote) {
    unavailableNote.hidden = !existingUnavailable || folder.all;
    unavailableNote.textContent = 'Фильтр уже скачанных недоступен на этом сервере — выбирайте любые папки.';
  }
  const errorBox = $('#folder-existing-error');
  const errorText = $('#folder-existing-error-text');
  const retry = $('#folder-existing-retry');
  const errorVisible = !!folder.existingError && !existingUnavailable && !folder.all;
  if (errorBox) errorBox.hidden = !errorVisible;
  if (errorText) errorText.textContent = errorVisible ? folder.existingError : '';
  if (retry) retry.disabled = !errorVisible || folder.existingPending.size > 0;
  const count = $('#folder-count');
  if (count) {
    count.textContent = folder.all
      ? 'Скачаем весь Диск'
      : `Выбрано: ${folder.selected.length} из ${MAX_SOURCE_FOLDERS}`;
  }
  const chips = $('#folder-chips');
  if (!chips) return;
  if (folder.all || !folder.selected.length) {
    chips.innerHTML = '';
    chips.hidden = true;
    return;
  }
  chips.hidden = false;
  chips.innerHTML = folder.selected.map((path) => {
    const label = displayCloudPath(path);
    return `<li class="chip">
      <span class="chip-label" title="${escapeHtml(label)}">${escapeHtml(label)}</span>
      <button class="chip-remove" type="button" data-remove-path="${escapeHtml(path)}" aria-label="Убрать папку ${escapeHtml(label)}">${icon('close')}</button>
    </li>`;
  }).join('');
}

/* --- что уже скачано в назначение ----------------------------------------- */

/* Ответы живут в одном кэше на пару (destPath, path). Аккаунт входит в
   область кэша: после переподключения старые ответы не должны применяться. */
function existingScopeKey() {
  const account = currentAccount();
  return account ? `${account.id}\u0000${flowDest}` : '';
}

function syncExistingCache() {
  const scope = existingScopeKey();
  if (scope !== existingCacheScope) {
    existingCache.clear();
    existingCacheScope = scope;
  }
  return scope;
}

/* Смена источника/аккаунта или новый вход в путь: старые ответы неактуальны. */
function resetExistingCache() {
  existingCache.clear();
  existingCacheScope = '';
  if (!folder) return;
  if (folder.existingPending) folder.existingPending.clear();
  if (folder.existingFailed) folder.existingFailed.clear();
  folder.existingError = '';
}

function existingKey(dest, path) {
  return `${dest}\u0000${path}`;
}

function existingEntry(path) {
  if (existingUnavailable || folder.all || folder.context !== 'flow-source' || !flowDest || !path) return null;
  if (!syncExistingCache()) return null;
  return existingCache.get(existingKey(flowDest, path)) || null;
}

function existingIsDownloaded(entry) {
  return !!entry && entry.exists && entry.entries > 0;
}

function existingItemWord(count) {
  const mod10 = count % 10;
  const mod100 = count % 100;
  if (mod10 === 1 && mod100 !== 11) return 'элемент';
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return 'элемента';
  return 'элементов';
}

function existingItemsText(count) {
  const value = Math.max(0, Number(count) || 0);
  return `${FILE_COUNT_FORMAT.format(value)}\u00A0${existingItemWord(value)}`;
}

/* Папка, которая уже есть и не пуста, выпадает из выбора, пока пользователь
   явно не разрешил перекачать её переключателем. */
function pruneExistingSelection() {
  if (!folder.selected.length) return;
  const blocked = new Set(folder.selected.filter((path) => (
    !folder.showExisting && existingIsDownloaded(existingEntry(path))
  )));
  if (!blocked.size) return;
  folder.selected = folder.selected.filter((path) => !blocked.has(path));
  folder.limitError = '';
}

/* Проверяем видимый уровень одной пачкой (сервер принимает до 20 путей).
   Ошибка одного уровня не блокирует выбор: строки просто остаются обычными,
   а в диалоге появляется ошибка с «Повторить». */
async function requestExisting(paths, { retry = false } = {}) {
  if (existingUnavailable || folder.all || folder.context !== 'flow-source') return;
  const account = currentAccount();
  const dest = flowDest;
  if (!account || !dest) return;
  if (!syncExistingCache()) return;
  const unique = Array.from(new Set((Array.isArray(paths) ? paths : [])
    .map((path) => cloudApiPath(path))
    .filter(Boolean)));
  const pending = unique.filter((path) => (
    !folder.existingPending.has(path)
    && !existingCache.has(existingKey(dest, path))
    && (retry || !folder.existingFailed.has(path))
  ));
  if (!pending.length) return;
  if (retry) {
    folder.existingError = '';
    renderFolderDialog();
  }
  const token = folderToken;
  for (const path of pending) folder.existingPending.add(path);
  const chunks = [];
  for (let index = 0; index < pending.length; index += EXISTING_BATCH) {
    chunks.push(pending.slice(index, index + EXISTING_BATCH));
  }
  try {
    for (let index = 0; index < chunks.length; index += 1) {
      const chunk = chunks[index];
      try {
        const data = await api('/api/jobs/existing', {
          method: 'POST',
          timeout: API_TIMEOUT.folders,
          body: JSON.stringify({ accountId: account.id, destPath: dest, sourcePaths: chunk }),
        });
        if (token !== folderToken) return;
        const items = Array.isArray(data && data.items) ? data.items : [];
        for (const item of items) {
          if (!item || !item.path) continue;
          const path = cloudApiPath(item.path);
          if (!path) continue;
          existingCache.set(existingKey(dest, path), {
            exists: !!item.exists,
            entries: Math.max(0, Number(item.entries) || 0),
          });
          folder.existingFailed.delete(path);
        }
        /* Пути, о которых сервер промолчал, проверим ещё раз при повторе. */
        const answered = new Set(items.map((item) => cloudApiPath(item && item.path)).filter(Boolean));
        for (const path of chunk) {
          if (!answered.has(path)) folder.existingFailed.add(path);
        }
      } catch (error) {
        if (token !== folderToken) return;
        if (error && (error.status === 404 || error.status === 405)) {
          /* Ручки нет (старый сервер): фильтр выключаем тихо, все папки
             снова выбираемы. Тост показываем один раз за сессию страницы. */
          existingUnavailable = true;
          existingCache.clear();
          folder.showExisting = false;
          flowShowExisting = false;
          folder.existingFailed.clear();
          folder.existingError = '';
          if (!existingNoticeShown) {
            existingNoticeShown = true;
            showToast({
              type: 'info', id: 'existing-unavailable',
              title: 'Фильтр уже скачанных недоступен',
              message: 'На этом сервере проверка не поддерживается — можно выбирать любые папки.',
            });
          }
          return;
        }
        /* 400/403/500: выбор не блокируем. Помечаем весь остаток уровня,
           чтобы «Повторить» запросил его заново, и показываем inline-ошибку. */
        for (const rest of chunks.slice(index)) {
          for (const path of rest) folder.existingFailed.add(path);
        }
        folder.existingError = `Не удалось проверить, что уже скачано. ${friendlyError(error)}`;
        break;
      }
    }
  } finally {
    for (const path of pending) folder.existingPending.delete(path);
    if (token === folderToken) {
      if (!folder.existingFailed.size) folder.existingError = '';
      pruneExistingSelection();
      renderFolderDialog();
    }
  }
}

function retryExisting() {
  if (existingUnavailable || !folder.existingFailed.size) return;
  requestExisting(Array.from(folder.existingFailed), { retry: true });
}

/* «Весь диск» выключили: проверяем уже загруженные уровни, не дожидаясь
   повторного раскрытия узлов. По-прежнему одна пачка на уровень. */
function requestExistingForLoadedLevels() {
  if (existingUnavailable || folder.all || folder.context !== 'flow-source') return;
  for (const node of folder.nodes.values()) {
    if (!node || !node.loaded || !Array.isArray(node.dirs) || !node.dirs.length) continue;
    requestExisting(node.dirs.map((entry) => entry.path));
  }
}

function existingTagMarkup(path, entry) {
  if (!entry || !entry.exists) return '';
  const id = `folder-tag-${encodeURIComponent(path)}`;
  const span = (modifier, text) => `<span class="folder-tag folder-tag--${modifier}" id="${escapeHtml(id)}">${text}</span>`;
  if (!existingIsDownloaded(entry)) return span('empty', 'Пустая папка уже есть');
  if (folder.showExisting) return span('redo', 'Будет перекачано');
  return span('done', 'Уже в папке');
}

function folderFocusSelector(element) {
  if (!element || !element.matches) return '';
  if (element.matches('.folder-node-main')) return `.folder-node-main[data-row-path="${cssEscape(element.dataset.rowPath || '')}"]`;
  if (element.matches('.folder-check')) return `.folder-check[data-path="${cssEscape(element.dataset.path || '')}"]`;
  if (element.matches('.folder-expand')) return `.folder-expand[data-expand="${cssEscape(element.dataset.expand || '')}"]`;
  /* Превью перерисовывается целиком: фокус возвращаем на тихую кнопку строки,
     а не теряем на body, — «Скрыть» и «Повторить» живут только в открытой
     панели и после перерисовки исчезают. */
  if (element.matches('.folder-files-toggle')) return `.folder-files-toggle[data-preview="${cssEscape(element.dataset.preview || '')}"]`;
  if (element.matches('[data-preview-close]')) return `.folder-files-toggle[data-preview="${cssEscape(element.dataset.previewClose || '')}"]`;
  if (element.matches('[data-preview-retry]')) return `.folder-files-toggle[data-preview="${cssEscape(element.dataset.previewRetry || '')}"]`;
  if (element.matches('[data-retry-node]')) return `[data-retry-node="${cssEscape(element.dataset.retryNode || '')}"]`;
  if (element.matches('[data-folder-retry]')) return '[data-folder-retry]';
  if (element.matches('.folder-row')) return `.folder-row[data-folder-path="${cssEscape(element.dataset.folderPath || '')}"]`;
  return '';
}

function renderFolderDialog() {
  renderFolderCrumbs();
  renderFlowSourceBar();
  const body = $('#folder-body');
  if (body) {
    const focused = document.activeElement;
    const focusSelector = focused && body.contains(focused) ? folderFocusSelector(focused) : '';
    if (folder.target === 'cloud') renderCloudBody(body);
    else renderServerBody(body);
    if (focusSelector) {
      let target = body.querySelector(focusSelector);
      /* Чекбокс стал недоступен после ответа фильтра: фокус переходит на
         строку с текстовой причиной, а не теряется на body. */
      if (target && target.disabled && target.matches('.folder-check')) {
        target = target.closest('.folder-node-main');
      }
      if (target && !target.disabled) target.focus({ preventScroll: true });
      else {
        const fallback = body.querySelector('.folder-check:not([disabled]), .folder-node-main, .folder-row, [data-folder-retry]');
        if (fallback) fallback.focus({ preventScroll: true });
      }
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
    /* Сообщение сервера важнее generic: «папка не найдена» или «нужен вход»
       объясняют причину, а не только факт сбоя. */
    const hint = folder.context === 'flow-dest'
      ? 'Проверьте, что сервер Umbrel доступен, и повторите.'
      : 'Выбор папок недоступен — введите путь вручную.';
    body.innerHTML = `
      <div class="folder-state folder-state--error">
        <p>${escapeHtml(friendlyError(folder.error))}</p>
        <p class="folder-detail">${hint}</p>
        <button class="btn btn--secondary btn--sm" type="button" data-folder-retry>Повторить</button>
      </div>`;
    return;
  }
  if (!folder.entries.length) {
    body.innerHTML = '<p class="folder-state">Здесь нет подпапок.</p>';
    return;
  }
  /* Кнопки-строки без role="list"/"listitem": иначе роль перекрывает
     семантику кнопки и скринридер не сообщает «кнопка». */
  body.innerHTML = `<div class="folder-list">${folder.entries.map((entry) => {
    /* В путь идёт исходное имя, в интерфейс — нормализованное для отображения. */
    const rawName = String(entry.name || shortPath(entry.path) || 'Папка');
    const path = normFolderEntryPath(entry.path, rawName);
    return `<button class="folder-row" type="button" data-folder-path="${escapeHtml(path)}">
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
        <p>${escapeHtml(friendlyError(root.error))}</p>
        ${notFound ? '<p class="folder-detail">Похоже, облако отключено. Проверьте подключение и повторите.</p>' : ''}
        <button class="btn btn--secondary btn--sm" type="button" data-folder-retry>Повторить</button>
      </div>`;
    return;
  }
  if (!root.dirs.length) {
    body.innerHTML = '<p class="folder-state">Здесь нет подпапок.</p>';
    return;
  }
  /* Настоящие вложенные списки вместо плоского role="list": иерархия папок
     доступна скринридеру без отдельного ARIA-дерева. */
  let html = '';
  if (folder.all) {
    html += '<p class="folder-all-note">Скачаем весь Диск — отмечать папки не нужно. Выключите «Весь диск», чтобы выбрать отдельные папки.</p>';
  }
  html += `<ul class="folder-tree">${root.dirs.map((entry) => cloudNodeMarkup(entry.path, 0)).join('')}</ul>`;
  const truncated = Array.from(folder.nodes.values()).some((node) => node && node.truncated);
  if (truncated) html += `<p class="folder-truncated">Показаны первые ${pluralFolders(500)}.</p>`;
  body.innerHTML = html;
  for (const checkbox of body.querySelectorAll('.folder-check')) {
    const path = checkbox.dataset.path || '';
    checkbox.indeterminate = !folder.all
      && !checkbox.checked
      && !checkbox.disabled
      && folder.selected.some((selected) => selected.startsWith(path + '/'));
  }
}

function cloudNodeMarkup(path, depth) {
  const name = displayPath(shortPath(path) || path);
  const all = !!folder.all;
  const selected = !all && folder.selected.includes(path);
  const covered = !all && !selected && folder.selected.some((ancestor) => path.startsWith(ancestor + '/'));
  const expanded = folder.expanded.has(path);
  const previewOpen = !!folder.preview && folder.preview.path === path;
  const node = folder.nodes.get(path);
  const canExpand = !node || !node.loaded || (node.dirs && node.dirs.length > 0);
  const expand = canExpand || expanded
    ? `<button class="folder-expand" type="button" data-expand="${escapeHtml(path)}" aria-expanded="${expanded}" aria-label="${expanded ? 'Скрыть' : 'Показать'} папки внутри «${escapeHtml(name)}»">${icon('chevron')}</button>`
    : '';
  /* «Показать файлы» — отдельная тихая кнопка строки. Она стоит рядом с
     чекбоксом и шевроном, не входит в label и не переключает выбор папки. */
  const previewButton = !cloudFilesAvailable()
    ? ''
    : `<button class="folder-files-toggle" type="button" data-preview="${escapeHtml(path)}" aria-expanded="${previewOpen}" aria-label="Показать файлы в папке «${escapeHtml(name)}»"${previewOpen ? ` aria-controls="${escapeHtml(previewPanelId(path))}"` : ''} title="Файлы в папке «${escapeHtml(name)}»">${icon('eye')}</button>`;
  const children = expanded ? cloudNodeChildren(path, depth) : '';
  const preview = previewOpen ? cloudFilesMarkup(path) : '';
  const sizeText = cloudSizesUnavailable ? '' : folderSizeText(path);
  const meta = sizeText ? `<span class="folder-row-meta">${escapeHtml(sizeText)}</span>` : '';
  /* Уже скачанная папка видна всегда, но по умолчанию недоступна. Пустая
     уже существующая папка выбирается: её содержимое всё равно новое. */
  const existing = existingEntry(path);
  const downloaded = existingIsDownloaded(existing);
  const emptyExisting = !!existing && existing.exists && !downloaded;
  const blockedExisting = downloaded && !folder.showExisting;
  const disabled = all || covered || blockedExisting;
  const coveredTag = covered
    ? `<span class="folder-tag folder-tag--covered" id="folder-tag-${escapeHtml(encodeURIComponent(path))}">Входит в выбранную выше</span>`
    : '';
  const tag = existingTagMarkup(path, existing) || coveredTag;
  const describedBy = tag
    ? ` aria-describedby="folder-tag-${escapeHtml(encodeURIComponent(path))}"`
    : '';
  let label = all
    ? `Папка «${escapeHtml(name)}»: выбран весь Диск`
    : `Скачать папку «${escapeHtml(name)}»`;
  if (blockedExisting) {
    label += ` — уже есть в назначении, ${existingItemsText(existing.entries)}; включите «Показать уже скачанные», чтобы перекачать`;
  } else if (emptyExisting) {
    label += ' — пустая папка уже есть';
  } else if (downloaded && folder.showExisting) {
    label += ' — будет перекачано';
  } else if (covered) {
    label += ' — входит в выбранную выше папку';
  }
  const title = all
    ? 'Выбран весь Диск'
    : blockedExisting
      ? `Уже в папке: ${existingItemsText(existing.entries)}. Включите «Показать уже скачанные», чтобы перекачать.`
      : emptyExisting
        ? 'Папка уже есть в назначении, но пуста — можно выбрать.'
        : downloaded
          ? `Будет перекачано: ${existingItemsText(existing.entries)} уже есть в папке.`
          : covered
            ? 'Входит в выбранную выше папку'
            : '';
  return `<li class="folder-node" style="--depth:${depth}">
    <label class="folder-node-main${disabled ? ' is-disabled' : ''}"${title ? ` title="${escapeHtml(title)}"` : ''} tabindex="-1" data-row-path="${escapeHtml(path)}">
      <input class="folder-check" type="checkbox" data-path="${escapeHtml(path)}"${selected ? ' checked' : ''}${disabled ? ' disabled aria-disabled="true"' : ''}${describedBy} aria-label="${label}">
      <span class="folder-check-mark" aria-hidden="true"></span>
      <span class="folder-row-text">
        <span class="folder-row-name" title="${escapeHtml(displayPath(path))}">${escapeHtml(name)}</span>
        ${tag}
        ${meta}
      </span>
    </label>
    <div class="folder-row-actions">
      ${previewButton}
      ${expand}
    </div>
    ${preview}
    ${children}
  </li>`;
}

/* Дочерние папки — вложенный список внутри своего <li>: настоящая
   иерархия для скринридера и Tab-порядка. */
function cloudNodeChildren(path, depth) {
  const node = folder.nodes.get(path);
  const innerDepth = depth + 1;
  const wrap = (inner) => `<ul class="folder-children">${inner}</ul>`;
  if (!node || node.loading) {
    return wrap(`<li class="folder-node-state" style="--depth:${innerDepth}">Загружаем папки…</li>`);
  }
  if (node.error) {
    return wrap(`<li class="folder-node-state" style="--depth:${innerDepth}"><span>${escapeHtml(friendlyError(node.error))}</span><button class="btn btn--ghost btn--sm" type="button" data-retry-node="${escapeHtml(path)}">Повторить</button></li>`);
  }
  if (!node.dirs.length) {
    return wrap(`<li class="folder-node-state" style="--depth:${innerDepth}">Здесь нет подпапок.</li>`);
  }
  return wrap(node.dirs.map((entry) => cloudNodeMarkup(entry.path, innerDepth)).join(''));
}

function renderFolderFooter() {
  const cloud = folder.target === 'cloud';
  const limit = $('#folder-limit');
  if (limit) {
    limit.hidden = !folder.limitError;
    limit.textContent = folder.limitError || '';
  }
  const choose = $('#folder-choose');
  if (choose) {
    /* «Далее» на шаге 1 доступна и без выбора: по нажатию объясняем, чего
       не хватает, и не оставляем пользователя с неактивной кнопкой. */
    choose.disabled = cloud ? (folder.context === 'flow-source' ? folderBusy() : false) : (folder.loading || !!folder.error);
  }
  updateFolderCreateUI();
}

/* --- размеры папок в облаке ---------------------------------------------- */

/* Показываем «12 483 файла · 4,1 ГБ» рядом с названием. Пока ответа нет —
   «…»; при ошибке ручки или пустом ответе просто не показываем размер. */
function folderSizeText(path) {
  if (cloudSizesUnavailable) return '';
  const entry = folder.sizes && folder.sizes.get(path);
  if (!entry) return '\u2026';
  if (entry.error) return '';
  return `${formatFiles(entry.files)} · ${humanBytes(entry.bytes)}`;
}

/* Размеры видимого уровня запрашиваются пачками: contract POST /api/cloud/sizes
   {accountId, paths} → {entries:[{path,bytes,files,error}]}, не больше 20 путей
   за один запрос (иначе сервер ответит 400). */
const CLOUD_SIZES_BATCH = 20;

async function requestCloudSizes(paths) {
  if (cloudSizesUnavailable) return;
  const account = currentAccount();
  if (!account) return;
  const pending = (Array.isArray(paths) ? paths : []).filter((path) => (
    path && !folder.sizes.has(path) && !folder.sizePending.has(path)
  ));
  if (!pending.length) return;
  const token = folderToken;
  for (const path of pending) folder.sizePending.add(path);
  const chunks = [];
  for (let index = 0; index < pending.length; index += CLOUD_SIZES_BATCH) {
    chunks.push(pending.slice(index, index + CLOUD_SIZES_BATCH));
  }
  try {
    for (const chunk of chunks) {
      try {
        const data = await api('/api/cloud/sizes', {
          method: 'POST',
          timeout: API_TIMEOUT.folders,
          body: JSON.stringify({ accountId: account.id, paths: chunk }),
        });
        if (token !== folderToken) return;
        const entries = Array.isArray(data && data.entries) ? data.entries : [];
        for (const entry of entries) {
          if (!entry || !entry.path) continue;
          const path = cloudApiPath(entry.path);
          if (!path) continue;
          if (entry.error) folder.sizes.set(path, { error: true });
          else folder.sizes.set(path, { bytes: Number(entry.bytes) || 0, files: Number(entry.files) || 0 });
        }
      } catch (error) {
        if (token !== folderToken) return;
        if (error && (error.status === 404 || error.status === 405)) {
          /* Ручки нет — размеры не показываем вовсе, интерфейс продолжает работать. */
          cloudSizesUnavailable = true;
          break;
        }
        /* Ошибка одной пачки не ломает список: просто не показываем эти размеры. */
        for (const path of chunk) folder.sizes.set(path, { error: true });
      }
    }
  } finally {
    for (const path of pending) folder.sizePending.delete(path);
    if (token === folderToken) renderFolderDialog();
  }
}

/* --- файлы внутри папки (превью в дереве) -------------------------------- */

/* GET /api/accounts/{id}/files?path=<relative>&limit=50 →
   {path, files:[{name,size,modified}], truncated, count}. Ручка необязательная:
   на 404/405 превью просто исчезает, пикер работает как раньше. */
const CLOUD_FILES_LIMIT = 50;

function cloudFilesAvailable() {
  return !cloudFilesUnavailable;
}

function previewPanelId(path) {
  return `folder-files-${encodeURIComponent(path)}`;
}

function previewFileSize(value) {
  const size = Number(value);
  if (!Number.isFinite(size) || size < 0) return '—';
  return humanBytes(size);
}

/* Файл — одна строка «имя · размер · дата»: имя обрезается с title, размер
   моноширинный и табличный, дата — тот же формат, что и у заданий. */
function cloudFileMarkup(file) {
  return `<li class="folder-file">
    <span class="folder-file-name" title="${escapeHtml(file.name)}">${escapeHtml(displayPath(file.name))}</span>
    <span class="folder-file-size" title="${escapeHtml(previewFileSize(file.size))}">${escapeHtml(previewFileSize(file.size))}</span>
    <span class="folder-file-date">${escapeHtml(formatWhen(file.modified))}</span>
  </li>`;
}

function cloudFilesMarkup(path) {
  const entry = folder.preview;
  if (!entry || entry.path !== path) return '';
  const name = displayPath(shortPath(path) || path);
  const title = `Файлы в «${name}»`;
  let body = '';
  if (entry.status === 'loading') {
    body = `<div class="folder-files-body" role="status">
      <p class="folder-files-status">Загружаем файлы…</p>
      <div class="folder-files-skeleton" aria-hidden="true"><span></span><span></span><span></span></div>
    </div>`;
  } else if (entry.status === 'error') {
    body = `<div class="folder-files-body">
      <p class="folder-files-status">${escapeHtml(friendlyError(entry.error))}</p>
      <button class="btn btn--secondary btn--sm" type="button" data-preview-retry="${escapeHtml(path)}">Повторить</button>
    </div>`;
  } else if (!entry.files.length) {
    body = '<div class="folder-files-body"><p class="folder-files-status">Папка пуста</p></div>';
  } else {
    body = `<ul class="folder-file-list" aria-label="${escapeHtml(title)}">${entry.files.map(cloudFileMarkup).join('')}</ul>`;
    if (entry.truncated) {
      const count = Number(entry.count) > 0 ? Number(entry.count) : entry.files.length;
      body += `<p class="folder-files-note">Показаны первые ${formatFiles(count)}</p>`;
    }
  }
  return `<div class="folder-files" id="${escapeHtml(previewPanelId(path))}">
    <div class="folder-files-head">
      <p class="folder-files-title" title="${escapeHtml(title)}">${escapeHtml(title)}</p>
      <button class="btn btn--quiet btn--sm folder-files-hide" type="button" data-preview-close="${escapeHtml(path)}">Скрыть</button>
    </div>
    ${body}
  </div>`;
}

function normalizeCloudFiles(data) {
  const files = [];
  for (const raw of Array.isArray(data && data.files) ? data.files : []) {
    if (!raw) continue;
    const name = String(raw.name || '').trim();
    if (!name) continue;
    files.push({ name, size: raw.size, modified: raw.modified || '' });
  }
  return files;
}

/* Отмена — на закрытие диалога и смену папки, а не на сворачивание превью:
   повторное раскрытие пользуется кэшем и не повторяет запрос. */
function cancelCloudFiles() {
  if (!folder || !folder.filesInflight) return;
  for (const controller of folder.filesInflight.values()) {
    try { controller.abort(); } catch { /* запрос уже завершён */ }
  }
  folder.filesInflight.clear();
}

async function requestCloudFiles(path) {
  if (!cloudFilesAvailable()) return;
  const account = currentAccount();
  const entry = folder.files.get(path);
  if (!account || !entry) return;
  const token = folderToken;
  const controller = typeof AbortController === 'function' ? new AbortController() : null;
  if (controller) folder.filesInflight.set(path, controller);
  const isCurrent = () => token === folderToken && folder.files.get(path) === entry
    && (!controller || folder.filesInflight.get(path) === controller);
  try {
    const data = await api(`/api/accounts/${encodeURIComponent(account.id)}/files?path=${encodeURIComponent(cloudApiPath(path))}&limit=${CLOUD_FILES_LIMIT}`, {
      timeout: API_TIMEOUT.folders,
      signal: controller ? controller.signal : undefined,
    });
    if (!isCurrent()) return;
    entry.files = normalizeCloudFiles(data);
    const count = Number(data && data.count);
    entry.count = Number.isFinite(count) && count > 0 ? count : entry.files.length;
    entry.truncated = !!(data && data.truncated);
    entry.error = null;
    entry.status = 'loaded';
  } catch (error) {
    if (!isCurrent()) return;
    if (error && (error.status === 404 || error.status === 405)) {
      /* Ручки нет — превью убираем из всего пикера до перезагрузки страницы. */
      cloudFilesUnavailable = true;
      cancelCloudFiles();
      folder.files.clear();
      folder.preview = null;
      renderFolderDialog();
      return;
    }
    entry.files = [];
    entry.count = 0;
    entry.truncated = false;
    entry.error = error;
    entry.status = 'error';
  } finally {
    if (controller && folder.filesInflight.get(path) === controller) folder.filesInflight.delete(path);
    if (token === folderToken && folder.preview && folder.preview.path === path) renderFolderDialog();
  }
}

function openFilesPreview(path) {
  const cached = folder.files.get(path);
  if (cached) {
    folder.preview = cached;
    renderFolderDialog();
    return;
  }
  const entry = {
    path,
    status: 'loading',
    files: [],
    truncated: false,
    count: 0,
    error: null,
  };
  folder.files.set(path, entry);
  folder.preview = entry;
  renderFolderDialog();
  requestCloudFiles(path);
}

function closeFilesPreview() {
  if (!folder.preview) return;
  folder.preview = null;
  renderFolderDialog();
}

function focusFilesToggle(path) {
  const target = document.querySelector(`.folder-files-toggle[data-preview="${cssEscape(path)}"]`);
  if (target) target.focus({ preventScroll: true });
}

function toggleFilesPreview(path) {
  if (!path || !cloudFilesAvailable()) return;
  if (folder.preview && folder.preview.path === path) {
    closeFilesPreview();
    focusFilesToggle(path);
    return;
  }
  openFilesPreview(path);
  /* Раскрытие/сворачивание не уводит фокус из строки: кнопка остаётся
     активной и для мыши, и для клавиатуры. */
  focusFilesToggle(path);
}

function retryFilesPreview(path) {
  const entry = folder.files.get(path);
  if (!entry || entry.status === 'loading') return;
  entry.status = 'loading';
  entry.error = null;
  if (folder.preview && folder.preview.path === path) renderFolderDialog();
  requestCloudFiles(path);
}

/* Создание папки: кнопка живёт в футере пикера, поле — инлайн-строкой.
   POST /api/fs/dirs появился позже первого релиза, поэтому 404/405 считаем
   «сервер не умеет» и прячем кнопку до перезагрузки страницы. */
function folderCreateAvailable() {
  return folder.target === 'server'
    && !folder.createUnavailable
    && !folder.error
    && folder.writable !== false;
}

function updateFolderCreateUI() {
  const trigger = $('#folder-create');
  const panel = $('#folder-create-panel');
  if (!trigger || !panel) return;
  const available = folderCreateAvailable();
  trigger.hidden = !available || folder.createOpen;
  if (!available && folder.createOpen && !folder.createBusy) folder.createOpen = false;
  panel.hidden = !(available && folder.createOpen);
  if (!available) return;
  trigger.disabled = folder.loading || folder.createBusy;
  if (!folder.createOpen) return;
  const input = document.getElementById('folder-new-name');
  const submit = $('#folder-create-submit');
  const cancel = $('#folder-create-cancel');
  if (input) input.disabled = folder.createBusy;
  if (submit) setBusy(submit, folder.createBusy);
  if (cancel) cancel.disabled = folder.createBusy;
}

function clearFolderCreateError() {
  const input = document.getElementById('folder-new-name');
  const node = document.getElementById('folder-create-error');
  if (input) {
    input.removeAttribute('aria-invalid');
    input.removeAttribute('aria-describedby');
    const control = input.closest('.control');
    if (control) control.classList.remove('has-error');
  }
  if (node) { node.hidden = true; node.textContent = ''; }
}

function setFolderCreateError(message) {
  const input = document.getElementById('folder-new-name');
  const node = document.getElementById('folder-create-error');
  if (input) {
    input.setAttribute('aria-invalid', 'true');
    const control = input.closest('.control');
    if (control) control.classList.add('has-error');
    if (node && message) input.setAttribute('aria-describedby', node.id);
    else input.removeAttribute('aria-describedby');
  }
  if (node) { node.textContent = message || ''; node.hidden = !message; }
}

/* Имя — только одна папка: без разделителей пути, «.»/«..» и без пустоты. */
function validateFolderName(value) {
  const name = String(value || '').trim();
  if (!name) return 'Введите имя папки.';
  if (name === '.' || name === '..') return 'Имя не может быть «.» или «..».';
  if (/[\\/]/.test(name)) return 'В имени не может быть «/» или «\\».';
  if (/[\u0000-\u001f]/.test(name)) return 'В имени есть недопустимые символы.';
  if (name.length > 255) return 'Имя длиннее 255 символов.';
  return '';
}

function joinServerPath(base, name) {
  const clean = String(base || '/').replace(/\/+$/, '');
  return clean ? `${clean}/${name}` : `/${name}`;
}

function openFolderCreate() {
  if (!folderCreateAvailable()) return;
  folder.createOpen = true;
  folder.limitError = '';
  clearFolderCreateError();
  const input = document.getElementById('folder-new-name');
  if (input) input.value = '';
  renderFolderDialog();
  const field = document.getElementById('folder-new-name');
  if (field) field.focus({ preventScroll: true });
}

function closeFolderCreate(restoreFocus = true) {
  if (folder.createBusy) return;
  folder.createOpen = false;
  clearFolderCreateError();
  renderFolderDialog();
  if (!restoreFocus) return;
  const trigger = $('#folder-create');
  if (trigger && !trigger.hidden) trigger.focus({ preventScroll: true });
  else focusFolderContent();
}

async function createFolder() {
  if (!folderCreateAvailable() || folder.createBusy) return;
  const input = document.getElementById('folder-new-name');
  const raw = input ? input.value : '';
  const invalid = validateFolderName(raw);
  if (invalid) { setFolderCreateError(invalid); if (input) input.focus({ preventScroll: true }); return; }
  const name = String(raw).trim();
  const target = joinServerPath(folder.path || folderRootPath(), name);
  clearFolderCreateError();
  folder.createBusy = true;
  renderFolderDialog();
  try {
    const data = await api('/api/fs/dirs', { method: 'POST', timeout: API_TIMEOUT.mutate, body: JSON.stringify({ path: target }) });
    const created = data && data.path ? String(data.path) : target;
    folder.createBusy = false;
    folder.createOpen = false;
    folder.limitError = '';
    announce(`Папка «${name}» создана`);
    /* Перечитываем список и открываем новую папку: она сразу выбрана. */
    await loadFolder(created, true);
  } catch (error) {
    folder.createBusy = false;
    if (error && (error.status === 404 || error.status === 405)) {
      folderCreateUnavailable = true;
      folder.createUnavailable = true;
      folder.createOpen = false;
      folder.limitError = 'Создание папок недоступно на этом сервере.';
      renderFolderDialog();
      return;
    }
    renderFolderDialog();
    setFolderCreateError(friendlyError(error));
    const field = document.getElementById('folder-new-name');
    if (field) field.focus({ preventScroll: true });
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
    const data = await api(`/api/accounts/${encodeURIComponent(account.id)}/folders?path=${encodeURIComponent(path)}`, { timeout: API_TIMEOUT.folders });
    if (token !== folderToken) return;
    const dirs = normalizeCloudDirs(data.dirs || data.entries, path);
    folder.nodes.set(path, {
      loading: false,
      loaded: true,
      error: null,
      dirs,
      truncated: !!data.truncated,
    });
    renderFolderDialog();
    /* Размеры — пачкой на весь видимый уровень, после отрисовки списка. */
    requestCloudSizes(dirs.map((entry) => entry.path));
    /* Проверка «уже есть» — тоже одна пачка на уровень. В режиме «Весь
       диск» она не нужна: качаем всё. */
    requestExisting(dirs.map((entry) => entry.path));
  } catch (error) {
    if (token !== folderToken) return;
    folder.nodes.set(path, { loading: false, loaded: false, error, dirs: [] });
    renderFolderDialog();
  }
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
    /* Уже скачанную папку нельзя выбрать, пока переключатель не разрешил
       перекачать её: защита от гонки, если ответ фильтра пришёл после клика. */
    if (!folder.showExisting && existingIsDownloaded(existingEntry(path))) {
      renderFolderDialog();
      return;
    }
    if (folder.selected.some((ancestor) => path.startsWith(ancestor + '/'))) {
      renderFolderDialog();
      return;
    }
    const rest = folder.selected.filter((selected) => !selected.startsWith(path + '/'));
    if (rest.length + 1 > MAX_SOURCE_FOLDERS) {
      folder.limitError = `Можно выбрать не больше ${pluralFolders(MAX_SOURCE_FOLDERS)}`;
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
    const data = await api(`/api/fs/folders?path=${encodeURIComponent(path)}`, { timeout: API_TIMEOUT.folders });
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

/* Шаг 1 → 2: фиксируем назначение и открываем источник. Папка сохранена в
   flowDest, поэтому «Назад» с источника вернёт её же, даже после ошибки. */
function flowToSource() {
  if (folder.loading || folder.error || !folder.path) return;
  flowDest = displayPath(folder.path);
  flowFree = Number.isFinite(Number(folder.free)) ? Number(folder.free) : null;
  flowTotal = Number.isFinite(Number(folder.total)) ? Number(folder.total) : null;
  flowStep = 2;
  folder.limitError = '';
  /* Кэш фильтра сам сбросится в syncExistingCache, если назначение другое. */
  showFolderStep('cloud', 'flow-source', flowOpener);
}

/* Шаг 2 → 1: сохраняем недособранный источник и возвращаем к назначению. */
function flowBackToDest() {
  if (folder.target !== 'cloud') return;
  const account = currentAccount();
  flowSourceAll = !!folder.all;
  cloudSelection = folder.all ? [] : folder.selected.slice().sort();
  cloudSelectionAccount = account ? account.id : '';
  folder.limitError = '';
  flowStep = 1;
  showFolderStep('server', 'flow-dest', flowOpener);
}

/* Шаг 2 → 3: подтверждаем источник и открываем проверку. */
function flowToReview() {
  if (folderBusy()) return;
  const account = currentAccount();
  if (!account) return;
  if (folder.all) {
    cloudSelection = [];
  } else if (!folder.selected.length) {
    folder.limitError = 'Выберите хотя бы одну папку или включите «Весь диск».';
    renderFolderFooter();
    const first = document.querySelector('#folder-body .folder-check:not([disabled])');
    if (first) first.focus({ preventScroll: true });
    return;
  } else {
    cloudSelection = folder.selected.slice().sort();
  }
  flowSourceAll = !!folder.all;
  cloudSelectionAccount = account.id;
  folder.limitError = '';
  flowPreflight = { key: '', status: 'idle', data: null, error: null };
  flowStep = 3;
  folder.chosen = true;
  /* Фокус после закрытия пикера отдаём заголовку «Проверка», а не кнопке
     входа: иначе close-событие перебьёт фокус нового шага. */
  folder.opener = null;
  const dialog = $('#folder-dialog');
  if (dialog && dialog.open) dialog.close();
  openReview();
}

function chooseFolder() {
  if (folder.context === 'flow-dest') { flowToSource(); return; }
  if (folder.context === 'flow-source') { flowToReview(); return; }
  if (folder.target === 'cloud') {
    if (folderBusy()) return;
    cloudSelection = folder.selected.slice().sort();
    cloudSelectionAccount = currentAccount() ? currentAccount().id : '';
    folder.chosen = true;
    const dialog = $('#folder-dialog');
    if (dialog && dialog.open) dialog.close();
    finishFolderDialog();
    return;
  }
  if (folder.loading || folder.error) return;
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
  /* aria-disabled не мешает повторному нажатию с клавиатуры — гасим здесь. */
  if (isBusyControl(trigger)) return;
  const action = trigger.dataset.action;
  const id = trigger.dataset.id || '';

  if (action === 'connect' || action === 'reconnect') {
    location.hash = '#/setup';
    return;
  }

  if (action === 'start-flow') {
    openFlowStart(trigger);
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
  const option = event.target.closest ? event.target.closest('.method-option') : null;
  if (option) {
    /* Стрелки/Home/End двигают выбор внутри радиогруппы способов входа. */
    const options = $$('#setup-body .method-option');
    const index = options.indexOf(option);
    let next = -1;
    if (event.key === 'ArrowRight' || event.key === 'ArrowDown') next = (index + 1) % options.length;
    else if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') next = (index - 1 + options.length) % options.length;
    else if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = options.length - 1;
    if (next >= 0) {
      event.preventDefault();
      chooseMethod(options[next].dataset.method);
    }
    return;
  }
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
$('#setup-body').addEventListener('submit', (event) => {
  if (!event.target.closest('#setup-form')) return;
  event.preventDefault();
  runConnect();
});
$('#setup-body').addEventListener('click', (event) => {
  const method = event.target.closest('[data-method]');
  if (method) { chooseMethod(method.dataset.method); return; }
  if (event.target.closest('#yandex-start, #yandex-retry')) { startYandexDevice(); return; }
  if (event.target.closest('#yandex-cancel')) { cancelYandexAttempt(); return; }
  const help = event.target.closest('#token-help-toggle');
  if (help) { toggleTokenHelp(); return; }
  const copy = event.target.closest('#token-copy');
  if (copy) { copyTokenCommand(copy); return; }
  if (event.target.closest('[data-setup-browse]')) {
    openFolderDialog('server', event.target.closest('[data-setup-browse]'));
    return;
  }
  if (event.target.closest('#f-pass-toggle')) { togglePassword(); }
});

$('#setup-next').addEventListener('click', onSetupNext);
$('#setup-back').addEventListener('click', onSetupBack);

/* Закрытие вкладки/уход со страницы: поток не должен висеть на сервере. */
window.addEventListener('pagehide', () => {
  stopYandexFlow({ cancel: true, keepalive: true });
});
/* Возврат из bfcache: поток уже отменён, поэтому показываем экран способа
   заново, а не устаревшую панель с кодом без таймеров. */
window.addEventListener('pageshow', (event) => {
  if (!event.persisted) return;
  if ($('#setup-view').hidden || wizard.step !== 0 || wizard.method !== 'yandex') return;
  resetYandexState();
  renderSetup({ focusTitle: false });
  refreshYandexAvailability();
});

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

/* После закрытия модального диалога отдаём накопленные уведомления и
   голосовые сообщения: до этого они были в inert-области. */
function resumeAfterDialog() {
  flushDeferredToasts();
  flushDeferredAnnouncements();
}

function finishExitDialog() {
  resumeAfterDialog();
  const chip = $('#account-chip');
  if (!chip || !chip.isConnected || $('#account-wrap').hidden) return;
  focusSoon(chip);
}

function finishFolderDialog() {
  /* close-событие приходит задачей: если диалог уже открыт заново (шаг 2 →
     проверка → назад), старую сессию не сбрасываем. */
  const dialog = $('#folder-dialog');
  if (dialog && dialog.open) return;
  resumeAfterDialog();
  if (folder.finished) return;
  folder.finished = true;
  /* Диалог закрыт — незавершённый запрос превью больше не нужен. */
  cancelCloudFiles();
  folderToken += 1;
  const chosen = folder.chosen;
  const target = folder.target;
  const opener = folder.opener;
  folder.opener = null;
  folder.chosen = false;
  folder.nodes = new Map();
  folder.expanded = new Set();
  folder.preview = null;
  folder.files = new Map();
  folder.filesInflight = new Map();
  folder.selected = [];
  folder.createOpen = false;
  folder.createBusy = false;
  clearFolderCreateError();
  /* В мастере после «Выбрать эту папку» фокус — на поле назначения. В шагах
     скачивания диалог либо переключается на следующий шаг, либо возвращает
     фокус на кнопку входа в путь. */
  const field = chosen && !folderIsFlow() && target === 'server'
    ? document.getElementById('f-dest')
    : null;
  const nextFocus = field || opener;
  if (nextFocus && nextFocus.isConnected && nextFocus.getClientRects().length) {
    focusSoon(nextFocus, opener || null);
  }
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

/* Пока идёт DELETE аккаунта, «Остаться» и Esc не должны закрывать диалог. */
function setDialogCancelBlocked(dialog, blocked) {
  if (!dialog) return;
  for (const button of dialog.querySelectorAll('[data-dialog-close]')) {
    if (blocked) button.setAttribute('aria-disabled', 'true');
    else button.removeAttribute('aria-disabled');
  }
}

function closeDialog(dialog) {
  if (!dialog || !dialog.open) return false;
  /* Операция уже отправлена — диалог закрывать нельзя, но и фокус не рвём. */
  if (dialog.id === 'exit-dialog' && exitBusy) return false;
  if (dialog.id === 'review-dialog' && flowBusy) return false;
  dialog.close();
  if (dialog.id === 'folder-dialog') finishFolderDialog();
  else if (dialog.id === 'exit-dialog') finishExitDialog();
  else if (dialog.id === 'review-dialog') finishReviewDialog();
  return true;
}

/* Смена hash закрывает открытые диалоги; исключение — DELETE аккаунта. */
function closeDialogsForRoute() {
  for (const dialog of $$('dialog[open]')) {
    if (dialog.id === 'exit-dialog' && exitBusy) continue;
    closeDialog(dialog);
  }
}

for (const button of $$('[data-dialog-close]')) {
  button.addEventListener('click', () => {
    closeDialog(button.closest('dialog'));
  });
}

$('#exit-dialog')?.addEventListener('keydown', (event) => keepDialogFocus(event.currentTarget, event));
$('#exit-dialog')?.addEventListener('close', () => { finishExitDialog(); });
$('#exit-dialog')?.addEventListener('cancel', (event) => {
  event.preventDefault();
  if (exitBusy) return; // Esc не прерывает уже отправленный DELETE
  closeDialog(event.currentTarget);
});

$('#folder-dialog')?.addEventListener('keydown', (event) => keepDialogFocus(event.currentTarget, event));
$('#folder-dialog')?.addEventListener('close', () => { finishFolderDialog(); });
$('#folder-dialog')?.addEventListener('cancel', (event) => { event.preventDefault(); closeDialog(event.currentTarget); });
$('#folder-choose')?.addEventListener('click', chooseFolder);
$('#folder-back')?.addEventListener('click', flowBackToDest);

/* «Весь диск»: дерево остаётся на месте, но чекбоксы недоступны — выбор
   папок не теряется и возвращается, когда переключатель выключают. */
$('#folder-all')?.addEventListener('change', (event) => {
  if (folder.context !== 'flow-source') return;
  folder.all = !!event.target.checked;
  folder.limitError = '';
  renderFolderDialog();
  /* Фильтр не применяется к «Весь диск», но при возврате к папкам уже
     загруженные уровни должны получить пометки без перезагрузки. */
  if (!folder.all) requestExistingForLoadedLevels();
});

/* «Показать уже скачанные»: выключен — папки видны, но недоступны; включён —
   их можно выбрать, и они помечаются «Будет перекачано». */
$('#folder-show-existing')?.addEventListener('change', (event) => {
  if (folder.context !== 'flow-source') return;
  folder.showExisting = !!event.target.checked;
  flowShowExisting = folder.showExisting;
  folder.limitError = '';
  if (!folder.showExisting) pruneExistingSelection();
  renderFolderDialog();
});

$('#folder-existing-retry')?.addEventListener('click', () => { retryExisting(); });

/* Чипы выбранных папок: снятие чипа = снятие чекбокса. */
$('#folder-chips')?.addEventListener('click', (event) => {
  const remove = event.target.closest('[data-remove-path]');
  if (!remove) return;
  const path = remove.dataset.removePath || '';
  toggleCloudSelection(path, false);
  const checkbox = document.querySelector(`.folder-check[data-path="${cssEscape(path)}"]`);
  const fallback = document.querySelector('#folder-body .folder-check')
    || document.getElementById('folder-all');
  if (checkbox) checkbox.focus({ preventScroll: true });
  else if (fallback) fallback.focus({ preventScroll: true });
});

$('#review-dialog')?.addEventListener('keydown', (event) => keepDialogFocus(event.currentTarget, event));
$('#review-dialog')?.addEventListener('close', () => { finishReviewDialog(); });
$('#review-dialog')?.addEventListener('cancel', (event) => {
  event.preventDefault();
  if (flowBusy) return; // запуск уже отправлен — не теряем результат
  closeDialog(event.currentTarget);
});
$('#review-estimate')?.addEventListener('click', () => reviewEstimate($('#review-estimate')));
$('#review-start')?.addEventListener('click', () => startTransfer($('#review-start')));
$('#review-back')?.addEventListener('click', () => {
  if (flowBusy) return;
  flowStep = 2;
  /* «Назад» ведёт на шаг источника, а не закрывает путь: close-событие
     проверки не должно вернуть фокус на кнопку входа. */
  flowFocusNow = true;
  const dialog = $('#review-dialog');
  if (dialog && dialog.open) dialog.close();
  showFolderStep('cloud', 'flow-source', flowOpener);
});

/* Создание папки на сервере. */
$('#folder-create')?.addEventListener('click', openFolderCreate);
$('#folder-create-submit')?.addEventListener('click', createFolder);
$('#folder-create-cancel')?.addEventListener('click', () => closeFolderCreate());
$('#folder-new-name')?.addEventListener('input', () => clearFolderCreateError());
$('#folder-new-name')?.addEventListener('keydown', (event) => {
  if (event.key === 'Enter') {
    event.preventDefault();
    event.stopPropagation();
    createFolder();
    return;
  }
  if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    closeFolderCreate();
  }
});

$('#folder-crumbs')?.addEventListener('click', (event) => {
  if (!event.target.closest('[data-folder-up]')) return;
  if (folder.target !== 'server' || folder.loading) return;
  const root = folderRootPath();
  if (folder.path === root) return;
  const fallback = folderSegments().length > 1 ? folderPathForIndex(folderSegments().length - 2) : root;
  folderNavigate(folder.parent != null ? String(folder.parent) : fallback);
});

$('#folder-body')?.addEventListener('click', (event) => {
  if (folder.target === 'cloud') {
    const filesToggle = event.target.closest('[data-preview]');
    if (filesToggle) { toggleFilesPreview(filesToggle.dataset.preview || ''); return; }
    const filesHide = event.target.closest('[data-preview-close]');
    if (filesHide) { closeFilesPreview(); return; }
    const filesRetry = event.target.closest('[data-preview-retry]');
    if (filesRetry) { retryFilesPreview(filesRetry.dataset.previewRetry || ''); return; }
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
  } catch {
    showOffline();
  } finally {
    /* Даже если первый запрос не прошёл, опрос должен сам восстановить
       связь — иначе «Повторить» остаётся единственным путём. */
    startPolling();
  }
})();
