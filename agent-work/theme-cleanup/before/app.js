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
  file: '<path d="M7 3.5h7l4.5 4.5V20a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V4.5a1 1 0 0 1 1-1Z"/><path d="M14 3.5V8.5h5"/>',
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
let dismissedJobs = new Set();
try {
  const saved = JSON.parse(localStorage.getItem('cloud-ferry.dismissed-jobs') || '[]');
  if (Array.isArray(saved)) dismissedJobs = new Set(saved.filter(id => typeof id === 'string'));
} catch { /* Хранилище браузера может быть недоступно. */ }

let lastStatuses = null;
let suppressed = new Map();
let offlineShown = false;
/* Возвращать ли фокус на чип после закрытия панели аккаунта: при переходе
   в мастер или в диалог выхода фокус ведёт уже другое действие. */
let accountFocusRestore = true;
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
/* Одна сессия диалога «Новое скачивание» (и пикера папки в мастере).
   Шаги переключают context, но накопленные узлы дерева, раскрытые ветки,
   выбор и прокрутка живут в этой же сессии: «Назад» возвращает состояние. */
let folder = {
  context: 'setup', /* setup | flow-dest | flow-source | flow-review */
  target: 'server', /* server | cloud */
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
  createError: '',
  /* Шаг 2: папки и отдельные файлы выбираются независимо. */
  all: false,
  showExisting: false,
  expanded: new Set(),
  nodes: new Map(),
  selected: [],
  selectedFiles: [],
  /* Сообщение о лимите/ошибке выбора живёт в футере над кнопками. */
  alert: '',
  treeScroll: 0,
  /* Панель файлов: path открытой папки и её кэш на время диалога. */
  filePanelPath: '',
  files: new Map(),
  filesInflight: new Map(),
  /* Размеры папок текущего уровня: path → {bytes, files} | {error:true}. */
  sizes: new Map(),
  sizePending: new Set(),
  /* Уже скачанные: по одному ответу на видимый уровень. */
  existingPending: new Set(),
  existingFailed: new Set(),
  existingError: '',
  existingDest: '',
  finished: false,
  chosen: false,
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

/* «Новое скачивание»: один диалог и один черновик на три шага.
   flowSourceAll — режим «Весь диск»; cloudSelection — выбранные корни папок
   (до 20), cloudFileSelection — отдельные файлы (до 100); flowDest —
   подтверждённая папка на сервере. Выбор хранится в черновике, а не в DOM,
   поэтому переживает ошибки, переходы и повторную отрисовку. */
const MAX_SOURCE_FOLDERS = 20;
const MAX_SOURCE_FILES = 100;
let flowStep = 1;
let excludedFolders = [];
let flowSourceAll = false;
let flowShowExisting = false;
let flowDest = '';
let flowFree = null;
let flowTotal = null;
let flowBusy = false;
let flowOpener = null;
/* 'dest' | 'source' — шаг открыт по «Изменить» из проверки и возвращает
   обратно, не пересобирая сценарий с первого шага. */
let flowEditing = '';
/* После успешного запуска фокус уходит в «Сейчас», а не на кнопку входа:
   close-событие диалога не должно перебить этот переход. */
let flowFocusNow = false;
let cloudSelection = [];
let cloudFileSelection = [];
let cloudSelectionAccount = '';
/* Оценка объёма: ключ = аккаунт + источник + назначение + режим. Повторные
   параллельные вызовы одного ключа дедуплицируются, устаревшие ответы
   отбрасываются по seq/ключу. */
let flowPreflight = {
  key: '', status: 'idle', data: null, error: null, seq: 0, controller: null,
  startedAt: 0,
};
/* Ключ идемпотентности одной попытки запуска. Он привязан к выбору
   (account/source/dest) и переживает потерю ответа и повторное нажатие
   «Скачать», чтобы сервер вернул уже созданную задачу, а не начал вторую.
   Новый ключ — при другом выборе, после успешного старта или сброса по 409. */
let flowLaunchToken = '';
let flowLaunchKey = '';

/* Уже скачанные: ответы POST /api/jobs/existing кэшируются по паре
   (destPath, path), поэтому раскрытие и повторная отрисовка уровня не
   повторяют запросы. Ручка необязательная: на 404/405 фильтр тихо
   выключается, а 400/403/500 показываются строкой с «Повторить». */
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
  const { timeout = API_TIMEOUT_DEFAULT, signal: callerSignal, meta = false, ...rest } = options || {};
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
    error.path = path;
    error.data = data;
    error.field = String((data && (data.field || data.param)) || '');
    throw error;
  }
  /* meta нужен запуску: 200 + Idempotent-Replay — это повтор, а не новый
     старт. Остальные вызовы по-прежнему получают только тело ответа. */
  if (meta) return { data, status: response.status, headers: response.headers };
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

/* 403 от папок на сервере — про путь, а не про вход в облако: сервер отвечает
   «в эту папку сохранять нельзя» или «папка ведёт за пределы доступной».
   Такое сообщение надо показать как есть, иначе интерфейс отправит человека
   заново входить в Диск. Остальные 401/403 — про доступ к облаку. */
function localFolderRequest(error) {
  const requestPath = String((error && error.path) || '');
  return requestPath.startsWith('/api/fs/') || requestPath.startsWith('/api/jobs/existing');
}

function friendlyError(error) {
  if (!error) return ERROR_TEXT.generic;
  if (typeof error === 'string') return clearServerText(error) || ERROR_TEXT.generic;
  if (error.kind === 'network') return ERROR_TEXT.network;
  if (error.kind === 'timeout') return ERROR_TEXT.timeout;
  const status = Number(error.status) || 0;
  const message = clearServerText(error.message);
  const genericStatus = !message || /^HTTP \d+$/.test(message);
  if ((status === 401 || status === 403) && localFolderRequest(error) && message) return message;
  if (status === 401 || status === 403) return ERROR_TEXT.auth;
  if (status === 429) return ERROR_TEXT.rateLimit;
  if (status >= 500) return ERROR_TEXT.server;
  if (status === 409) return genericStatus ? ERROR_TEXT.conflict : message;
  if (status === 404) return genericStatus ? ERROR_TEXT.notFound : message;
  if (status === 0) return message || ERROR_TEXT.network;
  return genericStatus ? ERROR_TEXT.generic : message;
}

/* Текст сервера для объяснения причины (без подмены generic-статусом). */
function serverMessage(error) {
  return clearServerText(error && error.message) || friendlyError(error);
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
  /* Пока открыт диалог флоу, тост показываем внутри него: #toasts снаружи
     inert и иначе ждал бы закрытия. */
  const flowHost = dialogOpen() ? $('#toasts-flow') : null;
  const host = flowHost || $('#toasts');
  if (!host) return null;
  if (id) {
    const existing = host.querySelector(`[data-toast-id="${cssEscape(id)}"]`);
    if (existing) return existing;
  }
  if (!flowHost && dialogOpen()) { queueToast({ ...options }); return null; }
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

let retryBusy = false;

async function retryConnection(trigger) {
  if (retryBusy) return;
  retryBusy = true;
  if (trigger && trigger.isConnected) setBusy(trigger, true);
  try {
    await loadData();
    if ($('#app-view').hidden && $('#setup-view').hidden) route();
    else render();
    startPolling();
  } catch {
    setOffline(true);
  } finally {
    retryBusy = false;
    if (trigger && trigger.isConnected) setBusy(trigger, false);
  }
}

/* Офлайн — полоса под шапкой, а не отдельный экран: данные в ячейках
   остаются последними известными, фокус никуда не переводится. */
function setOffline(value) {
  const dot = $('#offline-dot');
  if (dot) dot.hidden = !value;
  for (const bar of $$('[data-offline-bar]')) bar.hidden = !value;
  document.body.classList.toggle('is-offline', !!value);
  if (value && !offlineShown) offlineShown = true;
  if (!value && offlineShown) {
    offlineShown = false;
    announce('Связь с сервером восстановлена');
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

function hideBoot() {
  const boot = $('#boot-view');
  if (boot) { boot.hidden = true; boot.removeAttribute('aria-busy'); }
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
  clearWizardSecrets();
  focusStartAfterRender = true;
  location.hash = '#/';
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
  setYandexLive('Вход подтверждён.');
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

/* --- header: account panel ------------------------------------------------ */

function renderHeader() {
  const account = currentAccount();
  const wrap = $('#account-wrap');
  const dialog = $('#account-dialog');
  if (!account) {
    if (wrap) wrap.hidden = true;
    if (dialog && dialog.open) {
      accountFocusRestore = false;
      dialog.close();
    }
    return;
  }
  if (wrap) wrap.hidden = false;
  const chip = $('#account-chip');
  if (chip) {
    const access = accountAccess(account);
    chip.title = 'Настройки';
    chip.dataset.status = access.tone;
  }
}

/* Панель аккаунта — модальный диалог: браузер сам держит фокус внутри,
   Esc закрывает, а close-событие возвращает фокус на чип. */
function openAccountPanel() {
  const account = currentAccount();
  const dialog = $('#account-dialog');
  const chip = $('#account-chip');
  if (!account || !dialog || dialog.open) return;
  accountFocusRestore = true;
  setAccountProbeResult('', '');
  renderAccountCell();
  renderAccountPanel();
  dialog.showModal();
  if (chip) chip.setAttribute('aria-expanded', 'true');
  const title = $('#account-title');
  if (title) title.focus({ preventScroll: true });
  /* Место могло устареть: панель сама обновит цифры, когда ответит система. */
  ensureCloudUsage();
}

function closeAccountPanel(restoreFocus = true) {
  const dialog = $('#account-dialog');
  if (!dialog || !dialog.open) return false;
  accountFocusRestore = restoreFocus;
  dialog.close();
  return true;
}

function finishAccountDialog() {
  const chip = $('#account-chip');
  const dialog = $('#account-dialog');
  if (chip) chip.setAttribute('aria-expanded', 'false');
  resumeAfterDialog();
  const restore = accountFocusRestore;
  accountFocusRestore = true;
  if (!restore || !chip || !chip.isConnected) return;
  if ($('#account-wrap').hidden || $('#app-view').hidden) return;
  /* Поверх панели уже открыт диалог выхода — фокус вернёт его закрытие. */
  if (dialog && dialog.open) return;
  if ($$('dialog[open]').length) return;
  focusSoon(chip);
}

function usageText(about) {
  if (!about) return '';
  const total = Number(about.total) || 0;
  const used = Number(about.used) || 0;
  if (total > 0) return `Занято ${humanBytes(used)} из ${humanBytes(total)}`;
  if (used > 0) return `Занято ${humanBytes(used)}`;
  return '';
}

/* Проверка доступа: результат остаётся строкой внутри панели, без тоста.
   busy-состояние кнопки сохраняет фокус на месте. */
async function probeAccount(button) {
  const account = currentAccount();
  if (!account || probeBusy) return;
  probeBusy = true;
  lastCloudAttempt = Date.now();
  setBusy(button, true);
  setAccountProbeResult('busy', 'Проверяем доступ к Диску…');
  const dialog = $('#account-dialog');
  if (dialog) dialog.setAttribute('aria-busy', 'true');
  let data = null;
  let failure = null;
  try {
    data = await api(`/api/accounts/${encodeURIComponent(account.id)}/probe`, {
      method: 'POST', timeout: API_TIMEOUT.external,
    });
  } catch (error) {
    failure = error;
  }
  if (!failure) {
    if (data.knownUser && data.login) {
      const probed = String(data.login).trim();
      if (probed) accountLogins.set(account.id, probed);
    }
    const about = data.about || {};
    if (Number(about.total) > 0 || Number(about.used) > 0) {
      cloudUsage = {
        used: Number(about.used) || 0,
        total: Number(about.total) || 0,
        checkedAt: Date.now(),
        updatedAt: Date.now(),
        stale: false,
        accountId: account.id,
        fromSystem: false,
      };
    }
    lastCloudAttempt = Date.now();
  }
  /* Статус и время проверки приходят из account.checkedAt/status: перечитываем
     состояние, но сбой обновления не отменяет уже полученный ответ probe. */
  try { await loadData(); } catch { /* показываем последнее известное */ }
  renderAccountCell();
  renderAccountPanel();
  if (failure) {
    const fresh = currentAccount();
    const authExpired = !!fresh && fresh.id === account.id && fresh.status === 'needs-auth';
    setAccountProbeResult('error', authExpired
      ? 'Нужно войти заново: Яндекс Диск не принял сохранённый доступ.'
      : `Не удалось проверить доступ. ${friendlyError(failure)}`);
  } else {
    const note = usageText(data.about);
    setAccountProbeResult('ok', data.warning
      ? `Доступ есть. ${data.warning}`
      : `Доступ есть. ${note || 'Занятое место обновлено.'}`);
  }
  probeBusy = false;
  if (dialog) dialog.removeAttribute('aria-busy');
  if (button && button.isConnected) setBusy(button, false);
}

function openExitDialog() {
  closeAccountPanel(false);
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
    updatedAt: cloud.updatedAt || '',
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
        updatedAt: cached.updatedAt || (cached.stale ? 0 : Date.now()),
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
          updatedAt: Date.now(),
          stale: false,
          accountId: account.id,
          fromSystem: false,
        };
      }
    })
    .catch(() => { /* «—» in the informer, no toast for the silent check */ })
    .finally(() => {
      cloudBusy = false;
      renderAccount();
    });
}

/* Папка, выбранная в мастере, становится начальным назначением шага 2:
   homeMarkup при повторном входе не пересобирается. */
function applyPreferredDest() {
  if (!preferredDest || flowDest) return;
  flowDest = displayPath(preferredDest);
  renderAccount();
}

/* Пара «138 из 214 ГБ» для аккаунта: число и единица в одной строке. */
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

/* «138 из 214 ГБ» без слова «Занято»: подпись уже стоит рядом. */
function cloudUsageShort(usage) {
  if (!usage) return '';
  const used = Math.max(0, Number(usage.used) || 0);
  const total = Math.max(0, Number(usage.total) || 0);
  if (!(used > 0 || total > 0)) return '';
  const pair = usageValuePair(used, total);
  return `${pair.used}\u00A0${pair.rest}`;
}

/* Свежесть данных Диска: server tick или probe обновляет updatedAt.
   Пять минут — граница, после которой число помечается как последнее
   известное, даже если сервер не успел поставить stale. */
const CLOUD_STALE_MS = 5 * 60 * 1000;

const cloudStamp = (value) => {
  if (!value) return 0;
  const ms = typeof value === 'number' ? value : Date.parse(String(value));
  return Number.isFinite(ms) && ms > 0 ? ms : 0;
};

function cloudUsageStamp(usage) {
  if (!usage) return 0;
  return cloudStamp(usage.updatedAt) || Number(usage.checkedAt) || 0;
}

function cloudUsageIsStale(usage) {
  if (!usage) return false;
  if (usage.stale) return true;
  const stamp = cloudUsageStamp(usage);
  return !stamp || Date.now() - stamp > CLOUD_STALE_MS;
}

function cloudStaleNote(usage) {
  const stamp = cloudUsageStamp(usage);
  const when = stamp ? checkedAtText(new Date(stamp).toISOString()) : '';
  return when && when !== 'не проверялось' ? `последние данные, ${when}` : 'последние данные';
}

const ACCESS_STATES = {
  ok: { label: 'Доступно', tone: 'ok' },
  auth: { label: 'Нужен вход', tone: 'auth' },
  unknown: { label: 'Не проверялось', tone: 'unknown' },
};

/* «Пароль приложения» / «Токен» / «Вход через Яндекс» — то, чем реально
   подключали аккаунт. */
const ACCOUNT_METHODS = {
  webdav: 'Пароль приложения',
  'yandex-token': 'Токен',
  'yandex-oauth': 'Вход через Яндекс',
  yandex: 'Вход через Яндекс',
};

function accountMethodLabel(account) {
  if (!account) return '';
  return ACCOUNT_METHODS[String(account.provider || '')] || 'Подключение к облаку';
}

function accountAccess(account) {
  if (!account) return ACCESS_STATES.unknown;
  const systemCloud = system && system.cloud;
  const systemMatches = systemCloudMatchesAccount(account, systemCloud);
  if (account.status === 'needs-auth'
    || (systemMatches && systemCloud && systemCloud.status === 'needs-auth')) return ACCESS_STATES.auth;
  if (account.status === 'ok') return ACCESS_STATES.ok;
  return ACCESS_STATES.unknown;
}

function accessPill(access) {
  const modifier = access.tone === 'ok'
    ? ' status-pill--ok'
    : (access.tone === 'auth' ? ' status-pill--auth' : ' status-pill--unknown');
  return `<span class="status-pill${modifier}">${access.label}</span>`;
}

/* Время последней проверки: «сегодня, 14:32» или дата для старых. */
function checkedAtText(value) {
  if (!value) return 'не проверялось';
  const date = new Date(value);
  if (Number.isNaN(date.getTime()) || date.getFullYear() < 2000) return 'не проверялось';
  const now = new Date();
  const sameDay = date.getFullYear() === now.getFullYear()
    && date.getMonth() === now.getMonth()
    && date.getDate() === now.getDate();
  const time = `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`;
  return sameDay ? `сегодня, ${time}` : (formatWhen(value) || 'не проверялось');
}

/* Свободное место сервера — в панели аккаунта и в шаге назначения (там же,
   где выбирается папка), а не отдельной ячейкой на главном. */
function serverSpaceText(root) {
  if (!root || !Number.isFinite(Number(root.free))) return '';
  const free = Math.max(0, Number(root.free));
  const total = Math.max(0, Number(root.total) || 0);
  return total > 0 ? `${humanBytes(free)} из ${humanBytes(total)}` : humanBytes(free);
}

function destinationPath() {
  const root = serverRoot();
  return displayPath(flowDest || preferredDest || state.downloads || (root && root.path) || '/downloads');
}

/* Главный экран: компактная карточка-вход в панель. Место Диска и статус
   доступа видны сразу, серверное место живёт в шаге назначения и в панели. */
function renderAccountCell() {
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

/* Панель аккаунта: статичные узлы, поэтому busy и фокус не теряются при
   каждом обновлении состояния. */
function renderAccountPanel() {
  const login = $('#account-login');
  if (!login) return;
  const account = currentAccount();
  if (!account) return;
  const access = accountAccess(account);
  if (access.tone === 'auth') cloudUsage = null;

  login.textContent = accountLogin(account) || account.name || 'Яндекс Диск';
  const method = $('#account-method');
  if (method) method.textContent = accountMethodLabel(account);
  const status = $('#account-status');
  if (status) status.innerHTML = accessPill(access);
  const checked = $('#account-checked');
  if (checked) {
    checked.textContent = account.checkedAt
      ? `проверено ${checkedAtText(account.checkedAt)}`
      : 'не проверялось';
    checked.classList.toggle('is-stale', !account.checkedAt);
  }

  const usage = cloudUsage;
  const stale = cloudUsageIsStale(usage);
  const short = cloudUsageShort(usage);
  const value = $('#account-disk-value');
  if (value) {
    value.textContent = short || '—';
    value.classList.toggle('is-stale', stale);
    value.title = usage ? usageText(usage) : '';
  }
  const bar = $('#account-disk-bar');
  if (bar) {
    const percent = usage && Number(usage.total) > 0
      ? Math.min(100, Math.round((Number(usage.used) / Number(usage.total)) * 100))
      : 0;
    bar.style.width = `${percent}%`;
  }
  const note = $('#account-disk-note');
  if (note) {
    let text = '';
    if (access.tone === 'auth') text = 'Войдите заново, чтобы увидеть занятое место.';
    else if (stale && short) text = cloudStaleNote(usage);
    else if (!short) text = 'Нажмите «Проверить», чтобы узнать занятое место.';
    note.textContent = text;
    note.hidden = !text;
    note.classList.toggle('is-stale', !!text && stale && short);
  }

  const root = serverRoot();
  const serverValue = $('#account-server-value');
  if (serverValue) {
    serverValue.textContent = serverSpaceText(root) || '—';
    serverValue.title = root && root.path ? `Свободно на сервере: ${root.path}` : '';
  }
  const serverNote = $('#account-server-note');
  if (serverNote) {
    serverNote.textContent = root && Number.isFinite(Number(root.free))
      ? `Папка назначения: ${destinationPath()}`
      : 'Свободное место на сервере неизвестно.';
  }
}

function renderAccount() {
  renderAccountCell();
  renderAccountPanel();
}

function setAccountProbeResult(kind, message) {
  const node = $('#account-probe-result');
  if (!node) return;
  node.textContent = message || '';
  node.hidden = !message;
  node.className = `account-probe-result${kind ? ` account-probe-result--${kind}` : ''}`;
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

/* Отдельные файлы задания (появились вместе с выбором файлов на шаге 2). */
function jobSourceFiles(job) {
  return Array.isArray(job.sourceFiles)
    ? job.sourceFiles.map((value) => cloudApiPath(value)).filter(Boolean)
    : [];
}

function sourceLabel(paths) {
  if (!paths.length) return 'Весь Диск';
  if (paths.length === 1) return displayCloudPath(paths[0]);
  return pluralFolders(paths.length);
}

/* «2 папки и 3 файла» — одна подпись источника для проверки и итога. */
function selectionLabel(folders, files) {
  const folderCount = Array.isArray(folders) ? folders.length : 0;
  const fileCount = Array.isArray(files) ? files.length : 0;
  if (!folderCount && !fileCount) return 'Весь Диск';
  if (folderCount && fileCount) return `${pluralFolders(folderCount)} и ${formatFiles(fileCount)}`;
  if (folderCount) return pluralFolders(folderCount);
  return formatFiles(fileCount);
}

/* One readable line for «Сейчас» and toasts: "3 папки: A, B → /downloads". */
function jobSourceText(job) {
  const paths = jobSourcePaths(job);
  const files = jobSourceFiles(job);
  const dest = displayPath(job.dest);
  if (paths.length > 1) {
    const names = paths.map((value) => displayPath(value)).join(', ');
    return `${pluralFolders(paths.length)}: ${names} → ${dest}`;
  }
  if (paths.length === 0 && files.length) return `${formatFiles(files.length)} → ${dest}`;
  return `${sourceLabel(paths)} → ${dest}`;
}

/* Источник черновика: «Весь Диск», «2 папки и 3 файла» и т. п. */
function flowSourceText() {
  if (flowSourceAll) return wholeDiskLabel(excludedFolders);
  return selectionLabel(cloudSelection, cloudFileSelection);
}

function flowSourcePaths() {
  return flowSourceAll ? [] : cloudSelection.slice();
}

function flowSourceFiles() {
  return flowSourceAll ? [] : cloudFileSelection.slice();
}

/* Selection belongs to one account: a new/disconnected cloud starts clean. */
function syncSelectionAccount() {
  const account = currentAccount();
  if (!flowSourceAll && !cloudSelection.length && !cloudFileSelection.length) {
    cloudSelectionAccount = account ? account.id : '';
    return;
  }
  if (!account || account.id !== cloudSelectionAccount) {
    cloudSelection = [];
    cloudFileSelection = [];
    excludedFolders = [];
    flowSourceAll = false;
    flowShowExisting = false;
    cloudSelectionAccount = account ? account.id : '';
    flowPreflight = {
      key: '', status: 'idle', data: null, error: null, seq: 0, controller: null, startedAt: 0,
    };
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
  const paths = jobSourcePaths(job);
  const files = jobSourceFiles(job);
  return selectionLabel(paths, files);
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
  const stats = job.stats || {};
  if (stats.totalBytes > 0) return humanPair(stats.bytes, stats.totalBytes);
  return stats.bytes > 0 ? humanBytes(stats.bytes) : '';

}

function homeMarkup() {
  return `
    <section class="drive-row" aria-labelledby="account-cell-login">
      <span class="drive-symbol">${icon('cloud')}</span>
      <div class="drive-name"><h2 id="account-cell-login"></h2><small>Яндекс Диск</small></div>
      <button class="btn btn--primary" id="flow-start" type="button" data-action="start-flow">${icon('transfer')}Скачать</button>
      <details class="drive-menu"><summary aria-label="Действия с диском" title="Действия с диском">•••</summary>
        <div class="drive-pop"><button type="button" data-action="exit">Выйти из аккаунта</button></div>
      </details>
    </section>
    <div id="job-list" aria-label="Загрузки"></div>
    <section id="cell-now" class="cell cell--now" data-mode="idle" aria-labelledby="now-title"></section>`;
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
    renderAccount();
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

function renderJobList(jobs) {
  const list = $('#job-list');
  const stale = new Map(Array.from(list.children, row => [row.dataset.id, row]));
  for (const job of jobs) {
    let row = stale.get(job.id);
    stale.delete(job.id);
    if (!row) {
      row = document.createElement('article');
      row.className = 'cell cell--now job-row';
      row.dataset.id = job.id;
      row.innerHTML = `<div class="task-head">${icon('folder')}<h2></h2><button class="btn btn--secondary" type="button"></button></div>
        <div class="now-body"><progress max="100"></progress><div class="task-stats"><span></span><span></span></div><div class="job-error"></div></div>`;
      list.append(row);
    }
    const paths = [...jobSourcePaths(job), ...jobSourceFiles(job)];
    const title = paths.length === 1 ? shortPath(paths[0]) : jobSourceLabel(job);
    row.querySelector('h2').textContent = title;
    const button = row.querySelector('button');
    const paused = PAUSED_STATUSES.includes(job.status);
    const failed = job.status === 'failed';
    const done = job.status === 'done';
    const label = done ? 'Убрать из списка' : paused ? (failed ? 'Повторить' : 'Продолжить') : 'Приостановить';
    if (row.dataset.status !== job.status) {
      row.dataset.status = job.status;
      button.hidden = job.status === 'verifying';
      button.dataset.id = job.id;
      button.dataset.action = done ? 'dismiss-job' : paused ? 'resume' : 'stop';
      button.title = label;
      button.setAttribute('aria-label', label + ' · ' + title);
      button.innerHTML = icon(done ? 'close' : paused ? 'resume' : 'pause');
      const error = row.querySelector('.job-error');
      error.hidden = !failed;
      if (failed) error.innerHTML = escapeHtml(job.errorAdvice || job.errorTitle || 'Не удалось скачать') + failureListMarkup(job.failures || [], job.failureCount || 0) +
        (job.failureCount ? `<button class="btn btn--quiet" data-action="copy-failures" data-id="${escapeHtml(job.id)}">Скопировать список</button>` : '');
    }
    const progress = row.querySelector('progress');
    progress.setAttribute('aria-label', title);
    if (job.stats?.totalBytes > 0 || paused || done) progress.value = percentOf(job);
    else progress.removeAttribute('value');
    const facts = row.querySelectorAll('.task-stats span');
    facts[0].textContent = jobSizeText(job);
    facts[1].textContent = done ? 'Завершена' : paused ? statusOf(job)[0] : job.status === 'running' && !(job.stats?.bytes > 0) ? 'Начинается' : jobMeta(job);
  }
  for (const row of stale.values()) {
    const hadFocus = row.contains(document.activeElement);
    row.remove();
    if (hadFocus) {
      const target = list.querySelector('button:not([hidden])') || $('#flow-start');
      target?.focus({ preventScroll: true });
    }
  }
  list.hidden = !jobs.length;
}

function nowMarkup(mode, job, activeCount, account) {
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
  const visible = accountJobs().filter(item =>
    ACTIVE_STATUSES.includes(item.status) || PAUSED_STATUSES.includes(item.status) ||
    (item.status === 'done' && !dismissedJobs.has(item.id)));

  renderJobList(visible);
  if (visible.length && account?.status !== 'needs-auth') {
    cell.hidden = true;
    cell.dataset.key = '';
    return;
  } else if (account && account.status === 'needs-auth') {
    mode = 'auth';

  } else if (latest && !KNOWN_JOB_STATUSES.has(latest.status)) {
    mode = 'unknown';
  }
  cell.hidden = mode === 'idle';
  const key = nowKey(mode, job, active.length, account);
  if (cell.dataset.key === key) {

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

/* --- «Новое скачивание»: один диалог, три шага ---------------------------- */

/* Шаги диалога: короткий заголовок и номер для «N из 3». Название операции
   «Новое скачивание» показывается в шапке один раз. Контекст setup — тот же
   диалог в роли пикера папки в мастере, поэтому там номера нет. */
const FLOW_STEPS = {
  'flow-dest': { step: 1, title: 'Куда сохранить' },
  'flow-source': { step: 2, title: 'Что скачать' },
  'flow-review': { step: 3, title: 'Проверьте выбор' },
  setup: { step: 0, title: 'Папка на сервере' },
};
/* Сколько ждём оценку, прежде чем честно сказать «Оценка ещё идёт». */
const ESTIMATE_SLOW_MS = 20000;
let reviewWaitTimer = 0;
/* /files отдаёт максимум 200 файлов; count — размер ответа, не всего списка. */
const CLOUD_FILES_LIMIT = 200;
const CLOUD_SIZES_BATCH = 20;
/* «+N» на шаге 3 раскрывает остальные чипы выбора. */
let reviewShowAll = false;

/* Голосовые уведомления внутри диалога: #live снаружи модального окна inert,
   поэтому готовый объём и лимиты идут в live-region самого диалога. */
function flowAnnounce(text) {
  const node = $('#flow-live');
  const value = String(text || '').trim();
  if (!node || !value) return;
  if (node.textContent === value) {
    node.textContent = '';
    requestAnimationFrame(() => { node.textContent = value; });
    return;
  }
  node.textContent = value;
}

/* --- сессия диалога -------------------------------------------------------- */

function folderSession(context, opener) {
  return {
    context,
    target: context === 'flow-source' ? 'cloud' : 'server',
    path: '',
    parent: null,
    roots: [],
    entries: [],
    loaded: false,
    free: null,
    total: null,
    writable: true,
    loading: false,
    error: null,
    opener: opener || null,
    createOpen: false,
    createBusy: false,
    createUnavailable: folderCreateUnavailable,
    all: false,
    showExisting: false,
    excluded: [],
    confirmPath: '',
    confirmFocus: false,
    expanded: new Set(),
    nodes: new Map(),
    selected: [],
    selectedFiles: [],
    alert: '',
    status: '',
    keepVisiblePath: '',
    treeScroll: 0,
    filePanelPath: '',
    files: new Map(),
    filesInflight: new Map(),
    sizes: new Map(),
    sizePending: new Set(),
    existingPending: new Set(),
    existingFailed: new Set(),
    existingError: '',
    existingDest: '',
    finished: false,
    chosen: false,
  };
}

function initialDestinationPath() {
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

/* Сброс черновика — только после успешного старта и при смене аккаунта.
   Закрытие X черновик не очищает: повторный вход начинается с шага 1. */
function resetFlowDraft() {
  excludedFolders = [];
  flowStep = 1;
  flowSourceAll = false;
  flowShowExisting = false;
  flowDest = '';
  flowFree = null;
  flowTotal = null;
  flowBusy = false;
  flowEditing = '';
  flowLaunchToken = '';
  flowLaunchKey = '';
  reviewShowAll = false;
  clearTimeout(reviewWaitTimer);
  cloudSelection = [];
  cloudFileSelection = [];
  const account = currentAccount();
  cloudSelectionAccount = account ? account.id : '';
  flowPreflight = { key: '', status: 'idle', data: null, error: null, seq: 0, controller: null, startedAt: 0 };
  resetExistingCache();
  folder = folderSession('setup', null);
}

/* --- запуск флоу ----------------------------------------------------------- */

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
  /* Новый вход: фильтр «уже скачанные» выключен, ответы проверки сбрасываем,
     а назначение и выбор папок/файлов остаются в памяти страницы. */
  flowShowExisting = false;
  flowEditing = '';
  resetExistingCache();
  folder = folderSession('flow-dest', flowOpener);
  folder.path = flowDest || initialDestinationPath();
  folder.selected = cloudSelection.slice();
  folder.selectedFiles = cloudFileSelection.slice();
  folder.excluded = excludedFolders.slice();
  folder.all = !!flowSourceAll;
  const dialog = $('#folder-dialog');
  if (dialog && !dialog.open) dialog.showModal();
  flowDest = folder.path;
  showFlowStep('flow-source');
}

/* Пикер папки в мастере использует тот же диалог и тот же шаг назначения. */
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

/* --- переключение шагов ---------------------------------------------------- */

function showFlowStep(context) {
  folder.context = context;
  folder.target = context === 'flow-source' ? 'cloud' : 'server';

  folder.confirmPath = '';
  folder.confirmFocus = false;
  folder.status = '';
  const info = FLOW_STEPS[context] || FLOW_STEPS.setup;
  flowStep = info.step || 1;

  if (context === 'flow-dest' || context === 'setup') {
    updateFlowChrome();
    renderDestinationView();
    renderFlowFooter();
    if (!folder.loaded && !folder.loading && !folder.error) loadFolder(folder.path || initialDestinationPath(), false);
  } else if (context === 'flow-source') {
    updateFlowChrome();
    folder.selected = folder.selected.length ? folder.selected : cloudSelection.slice();
    folder.selectedFiles = folder.selectedFiles.length ? folder.selectedFiles : cloudFileSelection.slice();
    folder.excluded = excludedFolders.slice();
    folder.all = !!flowSourceAll;
    folder.showExisting = !!flowShowExisting;
    if (!folder.existingDest) folder.existingDest = flowDest;
    renderFlow();
    const root = folder.nodes.get('');
    if (!root || (!root.loaded && !root.loading)) loadCloudNode('');
    else {
      syncExistingDestination();
      requestExistingForLoadedLevels();
    }
    const tree = $('#flow-source-tree');
    if (tree) tree.scrollTop = folder.treeScroll || 0;
    flowAnnounce('Что скачать');
  } else if (context === 'flow-review') {
    const key = flowPreflightKey();
    if (flowPreflight.key !== key) {
      cancelPreflight();
      flowPreflight = { key, status: 'idle', data: null, error: null, seq: 0, controller: null, startedAt: 0 };
    }
    renderFlow();
    ensurePreflight();
    flowAnnounce('Перед скачиванием');
  } else {
    renderFlow();
  }

  /* Каждый шаг начинается сверху: прокрутка прошлого шага не переносится. */
  const content = $('#flow-content');
  if (content) content.scrollTop = 0;
  /* Фокус — на коротком заголовке шага, а не на названии операции. */
  const heading = $('#flow-title');
  if (heading) heading.focus({ preventScroll: true });
}

/* --- каркас: шапка, секции, футер ----------------------------------------- */

function updateFlowChrome() {
  const context = folder.context || 'setup';
  const info = FLOW_STEPS[context] || FLOW_STEPS.setup;
  const title = $('#flow-title');
  const stepTitle = $('#flow-step-title');
  const position = $('#flow-step-pos');
  const stepHead = $('#flow-step-head');
  const foot = $('#flow-foot');
  const dialog = $('#folder-dialog');
  if (title) title.textContent = context === 'flow-source' ? 'Что скачать' : context === 'flow-review' ? 'Перед скачиванием' : 'Куда сохранить';
  if (stepHead) stepHead.hidden = true;
  if (stepTitle) stepTitle.textContent = info.title;
  if (position) {
    position.hidden = !info.step;
    position.textContent = info.step ? `${info.step} из 3` : '';
  }
  for (const number of [1, 2, 3]) {
    const section = document.getElementById(`flow-step-${number}`);
    if (section) section.hidden = number !== (info.step || 1);
  }
  if (foot) foot.dataset.context = context;
  if (dialog) {
    dialog.dataset.context = context;
    dialog.dataset.step = info.step ? String(info.step) : '';
    dialog.removeAttribute('aria-describedby');
  }
}

function renderFlow() {
  updateFlowChrome();
  const context = folder.context;
  if (context === 'flow-source') {
    renderSourceBar();
    renderSourceTree();
  } else if (context === 'flow-review') {
    renderReview();
  } else {
    renderDestinationView();
  }
  renderFlowFooter();
  const dialog = $('#folder-dialog');
  if (dialog) {
    if (folder.loading) dialog.setAttribute('aria-busy', 'true');
    else dialog.removeAttribute('aria-busy');
  }
}

function selectedFolderRoots() {
  return folder.selected.slice().sort();
}

function selectedFileItems() {
  return folder.selectedFiles.slice().sort();
}

function setFlowAlert(message) {
  folder.alert = message || '';
  renderFlowFooter();
  if (message) flowAnnounce(message);
}

/* Пустой выбор — не ошибка: кнопка проверки просто недоступна. */
function flowSelectionEmpty() {
  return !folder.all && !folder.selected.length && !folder.selectedFiles.length;
}

function renderFlowFooter() {
  const context = folder.context || 'setup';
  const back = $('#folder-back');
  const primary = $('#folder-choose');
  const noteNode = $('#flow-foot-note');
  const noteValue = $('#flow-foot-note-text');
  if (!back || !primary) return;
  let noteText = '';
  let noteTone = '';
  back.textContent = context === 'setup' ? 'Отмена' : 'Назад';
  if (context === 'flow-dest') {
    back.hidden = flowEditing !== 'dest';
    back.disabled = false;
    primary.textContent = flowEditing === 'dest' ? 'Сохранить папку' : 'Выбрать эту папку';
    primary.disabled = !!folder.loading || !!folder.error || !folder.path || folder.writable === false;
  } else if (context === 'flow-source') {
    back.hidden = flowEditing !== 'source';
    back.disabled = false;
    primary.textContent = 'Далее';
    primary.disabled = folderBusy() || flowSelectionEmpty();
    noteText = folder.alert || '';
    noteTone = folder.alert ? 'alert' : '';
  } else if (context === 'flow-review') {
    back.hidden = false;
    back.disabled = flowBusy;
    const nofit = flowPreflight.status === 'ready'
      && flowPreflight.data
      && flowPreflight.data.fits === false;
    primary.textContent = 'Скачать';
    primary.disabled = flowBusy || nofit;
    noteText = folder.status || '';
    noteTone = folder.status ? 'status' : '';
  } else {
    back.hidden = false;
    back.textContent = 'Отмена';
    back.disabled = false;
    primary.textContent = 'Выбрать эту папку';
    primary.disabled = !!folder.loading || !!folder.error || !folder.path || folder.writable === false;
  }
  /* Зона над кнопками на шаге выбора зарезервирована под лимит: футер не
     прыгает, а сообщение не теряется среди строк. */
  if (noteNode) {
    noteNode.hidden = !noteText;
    noteNode.dataset.empty = noteText ? 'false' : 'true';
    noteNode.classList.toggle('flow-foot-note--status', noteTone === 'status');
  }
  if (noteValue) noteValue.textContent = noteText;
}

/* --- шаг 1: куда сохранить ------------------------------------------------- */

function folderRootPath() {
  return folder.roots && folder.roots[0] ? String(folder.roots[0]) : (state.downloads || '/downloads');
}

function joinServerPath(base, name) {
  const clean = String(base || '/').replace(/\/+$/, '');
  return clean ? `${clean}/${name}` : `/${name}`;
}

function normFolderEntryPath(entryPath, name) {
  if (entryPath) return String(entryPath);
  return joinServerPath(folder.path || folderRootPath(), name);
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

function skeletonMarkup(rows = 4) {
  return `<div class="skeleton-lines" aria-hidden="true">${'<span></span>'.repeat(Math.max(1, rows))}</div>`;
}

function loadingState(text, rows = 4) {
  return `<div class="folder-state" role="status" aria-busy="true"><p>${escapeHtml(text)}</p>${skeletonMarkup(rows)}</div>`;
}

function folderErrorState(title, detail, retryAttribute) {
  return `<div class="folder-state folder-state--error">
    ${icon('warning', 'folder-state-icon')}
    <p class="folder-state-title">${escapeHtml(title)}</p>
    ${detail ? `<p class="folder-detail">${escapeHtml(detail)}</p>` : ''}
    <button class="btn btn--secondary btn--sm" type="button" ${retryAttribute}>Повторить</button>
  </div>`;
}

function captureFocusSelector(scope) {
  const active = document.activeElement;
  if (!active || !scope || !scope.contains(active) || !active.matches) return '';
  if (active.matches('[data-folder-path]')) return `[data-folder-path="${cssEscape(active.dataset.folderPath || '')}"]`;
  if (active.matches('[data-folder-retry]')) return '[data-folder-retry]';
  return '';
}

function restoreFocus(scope, selector) {
  if (!selector || !scope) return;
  const target = scope.querySelector(selector);
  if (target && !target.disabled) target.focus({ preventScroll: true });
}

function renderDestinationView() {
  const up = $('#folder-up');
  const path = $('#folder-path');
  const body = $('#flow-dest-body');
  if (path) {
    const value = displayPath(folder.path || '');
    path.textContent = value;
    path.title = value;
  }
  if (up) {
    const atRoot = folder.parent == null || !!folder.loading;
    if (atRoot) up.setAttribute('aria-disabled', 'true');
    else up.removeAttribute('aria-disabled');
  }
  if (body) {
    const focusSelector = captureFocusSelector(body);
    if (folder.loading) {
      body.innerHTML = loadingState('Загружаем папки…', 4);
    } else if (folder.error) {
      body.innerHTML = folderErrorState('Не удалось открыть папку', serverMessage(folder.error), 'data-folder-retry');
    } else if (!folder.entries.length) {
      body.innerHTML = `<div class="folder-state folder-state--empty">
        <p class="folder-state-title">В этой папке нет подпапок</p>
        <p class="folder-detail">Сюда можно сохранить файлы или создать новую папку.</p>
      </div>`;
    } else {
      body.innerHTML = `<div class="folder-list">${folder.entries.map((entry) => {
        const rawName = String(entry.name || shortPath(entry.path) || 'Папка');
        const itemPath = normFolderEntryPath(entry.path, rawName);
        return `<button class="folder-row" type="button" data-folder-path="${escapeHtml(itemPath)}">
          ${icon('folder')}
          <span class="folder-row-name" title="${escapeHtml(displayPath(itemPath))}">${escapeHtml(displayPath(rawName))}</span>
          ${icon('chevron', 'folder-row-chevron')}
        </button>`;
      }).join('')}</div>`;
    }
    restoreFocus(body, focusSelector);
  }
  updateFolderCreateUI();
}

async function loadFolder(path, focusAfter) {
  if (folder.target === 'cloud') return;
  const token = ++folderToken;
  folder.path = path;
  folder.loaded = false;
  folder.loading = true;
  folder.error = null;
  renderFlow();
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
    folder.loaded = true;
  } catch (error) {
    if (token !== folderToken) return;
    folder.error = error;
    folder.loaded = false;
  } finally {
    if (token === folderToken) {
      folder.loading = false;
      renderFlow();
      if (focusAfter) focusFolderContent();
    }
  }
}

function navigateUp() {
  if (folder.loading) return;
  if (folder.parent != null) {
    loadFolder(folder.parent, true);
    return;
  }
  const root = folderRootPath().replace(/\/+$/, '');
  const current = displayPath(folder.path).replace(/\/+$/, '');
  if (root && current.startsWith(`${root}/`)) {
    const relative = current.slice(root.length + 1);
    const index = relative.lastIndexOf('/');
    loadFolder(index >= 0 ? `${root}/${relative.slice(0, index)}` : root, true);
  }
}

function folderNavigate(path) {
  if (folder.loading) return;
  loadFolder(path, true);
}

function focusFolderContent() {
  if (folder.target === 'cloud' ? folderBusy() : folder.loading) return;
  const scope = folder.context === 'flow-source' ? $('#flow-source-tree') : $('#flow-dest-body');
  const target = (scope && (scope.querySelector('.folder-row')
    || scope.querySelector('.folder-check:not([disabled])')
    || scope.querySelector('.folder-node-main')))
    || $('#folder-path')
    || $('#folder-choose');
  if (target && typeof target.focus === 'function' && !target.disabled) target.focus({ preventScroll: true });
}

/* --- создание папки на сервере -------------------------------------------- */

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
  if (!available && folder.createOpen && !folder.createBusy) {
    folder.createOpen = false;
    clearFolderCreateError();
  }
  trigger.hidden = !available || folder.createOpen;
  panel.hidden = !(available && folder.createOpen);
  if (!available) return;
  trigger.disabled = !!folder.loading || folder.createBusy;
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
  }
  if (node) { node.hidden = true; node.textContent = ''; }
}

function setFolderCreateError(message) {
  const input = document.getElementById('folder-new-name');
  const node = document.getElementById('folder-create-error');
  if (input) {
    input.setAttribute('aria-invalid', 'true');
    if (message) input.setAttribute('aria-describedby', 'folder-create-error');
    else input.removeAttribute('aria-describedby');
  }
  if (node) {
    node.textContent = message || '';
    node.hidden = !message;
  }
}

/* Название — ровно одна папка: без разделителей пути, «.»/«..» и пустоты. */
function validateFolderName(value) {
  const name = String(value || '').trim();
  if (!name) return 'Введите название папки';
  if (name === '.' || name === '..') return 'Название не может быть «.» или «..»';
  if (/[\\/]/.test(name)) return 'В названии не может быть «/» или «\\»';
  if (/[\u0000-\u001f]/.test(name)) return 'В названии есть недопустимые символы';
  if (name.length > 255) return 'Название длиннее 255 символов';
  return '';
}

function openFolderCreate() {
  if (!folderCreateAvailable() || folder.createBusy) return;
  folder.createOpen = true;
  clearFolderCreateError();
  updateFolderCreateUI();
  const input = document.getElementById('folder-new-name');
  if (input) {
    input.value = '';
    input.focus({ preventScroll: true });
  }
}

function closeFolderCreate(restoreFocus = true) {
  if (folder.createBusy) return;
  folder.createOpen = false;
  clearFolderCreateError();
  updateFolderCreateUI();
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
  if (invalid) {
    setFolderCreateError(invalid);
    if (input) input.focus({ preventScroll: true });
    return;
  }
  const name = String(raw).trim();
  const target = joinServerPath(folder.path || folderRootPath(), name);
  clearFolderCreateError();
  folder.createBusy = true;
  updateFolderCreateUI();
  try {
    const data = await api('/api/fs/dirs', {
      method: 'POST',
      timeout: API_TIMEOUT.mutate,
      body: JSON.stringify({ path: target }),
    });
    const created = data && data.path ? String(data.path) : target;
    folder.createBusy = false;
    folder.createOpen = false;
    await loadFolder(created, false);
    updateFolderCreateUI();
    showToast({ type: 'ok', title: `Папка «${name}» создана`, message: 'Можно сохранить файлы сюда.' });
    /* После создания сразу предлагаем подтвердить папку, а не оставляем
       фокус на текстовом пути: следующее действие — «Выбрать эту папку». */
    const primary = $('#folder-choose');
    const path = $('#folder-path');
    if (primary && !primary.disabled) primary.focus({ preventScroll: true });
    else if (path) path.focus({ preventScroll: true });
  } catch (error) {
    folder.createBusy = false;
    if (error && (error.status === 404 || error.status === 405)) {
      folderCreateUnavailable = true;
      folder.createUnavailable = true;
      folder.createOpen = false;
      updateFolderCreateUI();
      return;
    }
    updateFolderCreateUI();
    setFolderCreateError(error && error.status === 409 ? 'Папка с таким именем уже есть' : friendlyError(error));
    const field = document.getElementById('folder-new-name');
    if (field) field.focus({ preventScroll: true });
  }
}

/* --- шаг 2: что скачать ---------------------------------------------------- */

function cloudFilesAvailable() {
  return !cloudFilesUnavailable;
}

function folderBusy() {
  const root = folder.nodes.get('');
  return !root || root.loading;
}

function childrenId(path) {
  return `folder-children-${encodeURIComponent(path)}`;
}

function filesPanelId(path) {
  return `folder-files-${encodeURIComponent(path)}`;
}

function tagId(path) {
  return `folder-tag-${encodeURIComponent(path)}`;
}

function wholeDiskLabel(excluded) {
  return excluded.length ? 'Весь диск, кроме ' + excluded.map(path => '«' + path + '»').join(', ') : 'Весь диск';
}

function folderCovered(path) {
  return !folder.all && folder.selected.some((ancestor) => ancestor !== path && path.startsWith(`${ancestor}/`));
}

function folderNodeChecked(path) {
  if (folder.all) return !folder.excluded.some(root => path === root || path.startsWith(root + '/'));
  if (folder.selected.includes(path)) return true;
  return folderCovered(path);
}

function selectedFilesUnder(path) {
  return folder.selectedFiles.filter((file) => file.startsWith(`${path}/`)).length;
}

function descendantFolderSelected(path) {
  return folder.selected.some((selected) => selected.startsWith(`${path}/`));
}

function folderIsIndeterminate(path) {
  if (folder.all) return folder.excluded.some(root => root.startsWith(path + '/'));
  if (folderCovered(path) || folderNodeChecked(path)) return false;
  return descendantFolderSelected(path) || selectedFilesUnder(path) > 0;
}

/* Тег — только факт с сервера. Обещания «будет перекачано» нет: правило
   пропуска/перезаписи существующих файлов задаёт сервер. */
function folderTagMarkup(path, entry) {
  if (!entry) return '';
  const id = tagId(path);
  const tag = (modifier, text) => `<span class="folder-tag folder-tag--${modifier}" id="${escapeHtml(id)}">${text}</span>`;
  if (folderCovered(path)) return tag('covered', 'Входит в выбранную папку');
  if (entry.exists && entry.entries > 0) return tag('done', 'Есть на сервере');
  if (entry.exists) return tag('empty', 'Пустая папка на сервере');
  return '';
}

function folderAriaLabel(path, name, options) {
  const { covered } = options;
  if (folder.all) return name;
  if (covered) return `Папка «${name}» — входит в выбранную папку`;
  if (folderIsIndeterminate(path)) return `Папка «${name}»: часть содержимого выбрана. Выбрать всю папку`;
  return `Скачать папку «${name}»`;
}

function folderConfirmMarkup(path, name) {
  return `<div class="folder-confirm" role="group" aria-label="Выбрать всю папку?">
    <p class="folder-confirm-text">Выбрать всю папку «${escapeHtml(name)}»?</p>
    <div class="folder-confirm-actions">
      <button class="btn btn--secondary btn--sm" type="button" data-confirm-folder="${escapeHtml(path)}">Выбрать всю</button>
      <button class="btn btn--quiet btn--sm" type="button" data-confirm-cancel="${escapeHtml(path)}">Отмена</button>
    </div>
  </div>`;
}

function cloudNodeMarkup(path, depth) {
  const name = displayPath(shortPath(path) || path);
  const entry = existingEntry(path);
  const downloaded = existingIsDownloaded(entry);
  const covered = folderCovered(path);
  const indeterminate = folderIsIndeterminate(path);
  const checked = folderNodeChecked(path) && !indeterminate;
  const disabled = covered || folder.all;
  const expanded = folder.expanded.has(path);
  const node = folder.nodes.get(path);
  const canExpand = !node || !node.loaded || (node.dirs && node.dirs.length > 0);
  const tag = folderTagMarkup(path, entry);
  const describedBy = tag ? tagId(path) : '';
  /* Фильтр меняет только видимость. Выбранное и сфокусированное не прячем,
     иначе строка исчезает из-под курсора и фокус уходит на body. */
  const keep = !!folder.keepVisiblePath
    && (path === folder.keepVisiblePath || folder.keepVisiblePath.startsWith(`${path}/`));
  const hiddenByFilter = downloaded && !folder.showExisting && !checked && !indeterminate && !keep;
  const expandButton = (canExpand || expanded)
    ? `<button class="folder-expand" type="button" data-expand="${escapeHtml(path)}" aria-expanded="${expanded}"${expanded ? ` aria-controls="${escapeHtml(childrenId(path))}"` : ''} aria-label="${expanded ? 'Скрыть папки внутри' : 'Показать папки внутри'} «${escapeHtml(name)}»">${icon('chevron')}</button>`
    : '';
  const filesOpen = folder.filePanelPath === path;
  const filesButton = !folder.all && cloudFilesAvailable()
    ? `<button class="folder-files-toggle" type="button" data-preview="${escapeHtml(path)}" aria-expanded="${filesOpen}" aria-controls="${escapeHtml(filesPanelId(path))}" aria-label="${filesOpen ? 'Скрыть файлы' : 'Показать файлы'} в папке «${escapeHtml(name)}»">${icon('eye')}</button>`
    : '';
  const meta = cloudSizesUnavailable ? '' : folderSizeText(path);
  const children = expanded ? cloudNodeChildren(path, depth) : '';
  const panel = !folder.all && filesOpen ? filesPanelMarkup(path) : '';
  const confirm = folder.confirmPath === path ? folderConfirmMarkup(path, name) : '';
  const title = displayCloudPath(path);
  return `<li class="folder-node" style="--depth:${depth}"${hiddenByFilter ? ' hidden' : ''}>
    <div class="folder-node-row${checked ? ' is-selected' : ''}">
      <label class="folder-node-main${disabled ? ' is-disabled' : ''}" data-row-path="${escapeHtml(path)}">
        <input class="folder-check" type="checkbox" data-path="${escapeHtml(path)}"${checked ? ' checked' : ''}${disabled ? ' disabled aria-disabled="true"' : ''} aria-label="${escapeHtml(folderAriaLabel(path, name, { covered }))}"${describedBy ? ` aria-describedby="${escapeHtml(describedBy)}"` : ''}>
        <span class="folder-check-mark" aria-hidden="true"></span>
        <span class="folder-icon" aria-hidden="true">${icon('folder')}</span>
        <span class="folder-row-text">
          <span class="folder-row-title">
            <span class="folder-row-name" title="${escapeHtml(title)}">${escapeHtml(name)}</span>
            ${tag}
          </span>
          ${meta ? `<span class="folder-row-meta">${escapeHtml(meta)}</span>` : ''}
        </span>
      </label>
      <div class="folder-row-actions">${filesButton}${expandButton}</div>
    </div>
    ${confirm}
    ${panel}
    ${children}
  </li>`;
}

function cloudNodeChildren(path, depth) {
  const node = folder.nodes.get(path);
  const innerDepth = depth + 1;
  const wrap = (inner) => `<ul class="folder-children" id="${escapeHtml(childrenId(path))}">${inner}</ul>`;
  if (!node || node.loading) {
    return wrap(`<li class="folder-node-state" style="--depth:${innerDepth}">${skeletonMarkup(2)}<span>Загружаем папки…</span></li>`);
  }
  if (node.error) {
    return wrap(`<li class="folder-node-state" style="--depth:${innerDepth}"><span>${escapeHtml(friendlyError(node.error))}</span><button class="btn btn--ghost btn--sm" type="button" data-retry-node="${escapeHtml(path)}">Повторить</button></li>`);
  }
  if (!node.dirs.length) {
    return wrap(`<li class="folder-node-state" style="--depth:${innerDepth}">В этой папке нет подпапок</li>`);
  }
  return wrap(node.dirs.map((entry) => cloudNodeMarkup(entry.path, innerDepth)).join(''));
}

function renderSourceBar() {
  if (folder.context !== 'flow-source') return;
  const filterRow = $('#folder-show-existing-row');
  if (filterRow) filterRow.hidden = existingUnavailable;
  const toggle = $('#folder-show-existing');
  if (toggle) toggle.checked = !!folder.showExisting;
  const all = $('#select-disk');
  all.checked = folder.all && !folder.excluded.length;
  all.indeterminate = folder.all ? false : !flowSelectionEmpty();
  const unavailable = $('#folder-existing-note');
  if (unavailable) {
    unavailable.hidden = !existingUnavailable;
    unavailable.textContent = existingUnavailable ? 'Проверка недоступна на этом сервере' : '';
  }
  const errorBox = $('#folder-existing-error');
  const errorText = $('#folder-existing-error-text');
  const retry = $('#folder-existing-retry');
  const errorVisible = !!folder.existingError && !existingUnavailable;
  if (errorBox) errorBox.hidden = !errorVisible;
  if (errorText) {
    errorText.textContent = errorVisible ? 'Не удалось проверить, что уже скачано.' : '';
    errorText.title = errorVisible ? folder.existingError : '';
  }
  if (retry) retry.disabled = !errorVisible || folder.existingPending.size > 0;
}

function sourceFocusSelector() {
  const active = document.activeElement;
  if (!active || !active.matches) return '';
  if (active.matches('.folder-check')) return `.folder-check[data-path="${cssEscape(active.dataset.path || '')}"]`;
  if (active.matches('.folder-expand')) return `.folder-expand[data-expand="${cssEscape(active.dataset.expand || '')}"]`;
  if (active.matches('.folder-files-toggle')) return `.folder-files-toggle[data-preview="${cssEscape(active.dataset.preview || '')}"]`;
  if (active.matches('.file-check')) return `.file-check[data-file="${cssEscape(active.dataset.file || '')}"]`;
  if (active.matches('[data-files-close]')) return `[data-files-close="${cssEscape(active.dataset.filesClose || '')}"]`;
  if (active.matches('[data-files-retry]')) return `[data-files-retry="${cssEscape(active.dataset.filesRetry || '')}"]`;
  if (active.matches('[data-select-all]')) return `[data-select-all="${cssEscape(active.dataset.selectAll || '')}"]`;
  if (active.matches('[data-select-folder]')) return `[data-select-folder="${cssEscape(active.dataset.selectFolder || '')}"]`;
  if (active.matches('[data-confirm-folder]')) return `[data-confirm-folder="${cssEscape(active.dataset.confirmFolder || '')}"]`;
  if (active.matches('[data-confirm-cancel]')) return `[data-confirm-cancel="${cssEscape(active.dataset.confirmCancel || '')}"]`;
  if (active.matches('[data-retry-node]')) return `[data-retry-node="${cssEscape(active.dataset.retryNode || '')}"]`;
  if (active.matches('[data-folder-retry]')) return '[data-folder-retry]';
  return '';
}

function focusedTreePath() {
  const active = document.activeElement;
  if (!active || !active.closest) return '';
  const row = active.closest('[data-row-path]');
  return row ? String(row.dataset.rowPath || '') : '';
}

function renderSourceTree() {
  const tree = $('#flow-source-tree');
  if (!tree) return;
  const focusSelector = sourceFocusSelector();
  const scroll = tree.scrollTop;
  folder.keepVisiblePath = focusedTreePath() || folder.confirmPath || '';
  const root = folder.nodes.get('');
  if (!root || root.loading) {
    tree.innerHTML = loadingState('Загружаем папки…', 4);
  } else if (root.error) {
    const notFound = root.error.status === 404;
    const detail = notFound
      ? 'Похоже, облако отключено. Проверьте подключение и повторите.'
      : serverMessage(root.error);
    tree.innerHTML = folderErrorState('Не удалось загрузить папки', detail, 'data-folder-retry');
  } else if (!root.dirs.length) {
    tree.innerHTML = `<div class="folder-state folder-state--empty">
      <p class="folder-state-title">В облаке нет папок</p>
      ${folder.all ? '' : '<p class="folder-detail">Чтобы скачать файлы из корня, отметьте «Весь диск».</p>'}
    </div>`;
  } else {
    let html = `<ul class="folder-tree">${root.dirs.map((entry) => cloudNodeMarkup(entry.path, 0)).join('')}</ul>`;
    const truncated = Array.from(folder.nodes.values()).some((node) => node && node.truncated);
    if (truncated) html += `<p class="folder-truncated">Показаны первые ${pluralFolders(500)}</p>`;
    tree.innerHTML = html;
    for (const checkbox of tree.querySelectorAll('.folder-check')) {
      checkbox.indeterminate = folderIsIndeterminate(checkbox.dataset.path || '');
    }
    for (const selectAll of tree.querySelectorAll('[data-select-all]')) {
      const path = selectAll.dataset.selectAll || '';
      const entry = folder.files.get(path);
      if (!entry || entry.status !== 'loaded') { selectAll.disabled = true; continue; }
      const visible = entry.files.map((file) => cloudApiPath(`${path}/${file.name}`));
      const chosen = visible.filter((file) => folder.selectedFiles.includes(file)).length;
      const fullyCovered = folder.all || folderNodeChecked(path);
      selectAll.checked = fullyCovered || (visible.length > 0 && chosen === visible.length);
      selectAll.indeterminate = !fullyCovered && chosen > 0 && chosen < visible.length;
      selectAll.disabled = fullyCovered || !visible.length;
    }
  }
  renderSourceBar();
  folder.keepVisiblePath = '';
  tree.scrollTop = scroll;
  if (folder.confirmFocus && folder.confirmPath) {
    const confirm = tree.querySelector(`[data-confirm-folder="${cssEscape(folder.confirmPath)}"]`);
    folder.confirmFocus = false;
    if (confirm) { confirm.focus({ preventScroll: true }); return; }
  }
  restoreFocus(tree, focusSelector);
}

/* --- выбор папок и файлов -------------------------------------------------- */

function clearSelectionAlert() {
  if (!folder.alert) return;
  folder.alert = '';
}

/* Лимиты считаем на нормализованном итоге: вложенные потомки входят в
   выбранный корень и не занимают отдельные места. */
function normalizedFolders(paths) {
  const roots = [];
  for (const path of Array.from(new Set(paths)).sort()) {
    if (!roots.some((root) => path === root || path.startsWith(`${root}/`))) roots.push(path);
  }
  return roots;
}

function normalizedFiles(folders, files) {
  const result = [];
  for (const file of Array.from(new Set(files)).sort()) {
    if (folders.some((root) => file.startsWith(`${root}/`))) continue;
    result.push(file);
  }
  return result;
}

function applySelection(folders, files) {
  const roots = normalizedFolders(folders);
  const loose = normalizedFiles(roots, files);
  if (roots.length > MAX_SOURCE_FOLDERS) {
    setFlowAlert('Можно выбрать не больше 20 папок');
    return false;
  }
  if (loose.length > MAX_SOURCE_FILES) {
    setFlowAlert('Можно выбрать не больше 100 отдельных файлов');
    return false;
  }
  folder.selected = roots;
  folder.selectedFiles = loose;
  return true;
}

function requestFolderConfirm(path) {
  if (!path || folder.all) return;
  const tree = $('#flow-source-tree');
  if (tree) folder.treeScroll = tree.scrollTop;
  folder.confirmPath = path;
  folder.confirmFocus = true;
  renderSourceTree();
}

function cancelFolderConfirm(path, restoreFocus = true) {
  folder.confirmPath = '';
  folder.confirmFocus = false;
  renderSourceTree();
  if (!restoreFocus) return;
  const checkbox = document.querySelector(`.folder-check[data-path="${cssEscape(path)}"]`);
  if (checkbox) checkbox.focus({ preventScroll: true });
}

function confirmFolderSelection(path) {
  if (!path || folder.all) return;
  folder.confirmPath = '';
  folder.confirmFocus = false;
  const ok = applySelection([...folder.selected, path], folder.selectedFiles);
  if (ok) clearSelectionAlert();
  renderSourceTree();
  renderFlowFooter();
  const checkbox = document.querySelector(`.folder-check[data-path="${cssEscape(path)}"]`);
  if (checkbox) checkbox.focus({ preventScroll: true });
  if (ok) flowAnnounce(`Выбрана вся папка «${displayPath(shortPath(path) || path)}»`);
}

function toggleFolderSelection(path, checked) {
  if (!path) return;
  /* «Весь диск» выбирается целиком: backend не принимает список исключений
     (POST /api/preflight и /api/jobs отвечают 400 на чужое поле), поэтому
     под режимом «весь диск» папки показаны отмеченными и заблокированы.
     Снять одну папку нельзя — иначе интерфейс обещал бы то, чего сервер
     не умеет. */
  if (folder.all) {
    renderFlow();
    return;
  }
  folder.confirmPath = '';
  folder.confirmFocus = false;
  if (checked) {
    if (folderCovered(path)) {
      renderSourceTree();
      return;
    }
    if (!applySelection([...folder.selected, path], folder.selectedFiles)) {
      renderSourceTree();
      return;
    }
  } else {
    folder.selected = folder.selected.filter((selected) => selected !== path);
  }
  clearSelectionAlert();
  renderSourceTree();
  renderFlowFooter();
}

function toggleFileSelection(path, checked) {
  if (!path || folder.all) return;
  folder.confirmPath = '';
  if (checked) {
    if (folderCovered(path) || folder.selectedFiles.includes(path)) {
      renderSourceTree();
      return;
    }
    if (!applySelection(folder.selected, [...folder.selectedFiles, path])) {
      renderSourceTree();
      return;
    }
  } else {
    folder.selectedFiles = folder.selectedFiles.filter((file) => file !== path);
  }
  clearSelectionAlert();
  renderSourceTree();
  renderFlowFooter();
}

function toggleAllFiles(path, checked) {
  const entry = folder.files.get(path);
  if (!entry || entry.status !== 'loaded') return;
  if (folder.all || folderNodeChecked(path)) return;
  const visible = entry.files.map((file) => cloudApiPath(`${path}/${file.name}`));
  if (checked) {
    const rest = folder.selectedFiles.filter((file) => !visible.includes(file));
    const room = Math.max(0, MAX_SOURCE_FILES - rest.length);
    const missing = visible.filter((file) => !rest.includes(file));
    const added = missing.slice(0, room);
    applySelection(folder.selected, [...rest, ...added]);
    if (added.length < missing.length) setFlowAlert('Можно выбрать не больше 100 отдельных файлов');
    else clearSelectionAlert();
  } else {
    folder.selectedFiles = folder.selectedFiles.filter((file) => !visible.includes(file));
    clearSelectionAlert();
  }
  renderSourceTree();
  renderFlowFooter();
}

function toggleCloudNode(path) {
  if (!path) return;
  const tree = $('#flow-source-tree');
  if (tree) folder.treeScroll = tree.scrollTop;
  if (folder.expanded.has(path)) {
    folder.expanded.delete(path);
    renderSourceTree();
    return;
  }
  folder.expanded.add(path);
  const node = folder.nodes.get(path);
  if (!node || !node.loaded || node.error) loadCloudNode(path);
  else renderSourceTree();
}

async function loadCloudNode(path) {
  const token = folderToken;
  const previous = folder.nodes.get(path) || {};
  folder.nodes.set(path, { ...previous, loading: true, loaded: false, error: null });
  renderFlow();
  const account = currentAccount();
  try {
    if (!account) throw Object.assign(new Error('Подключение не найдено.'), { status: 404 });
    const data = await api(`/api/accounts/${encodeURIComponent(account.id)}/folders?path=${encodeURIComponent(path)}`, {
      timeout: API_TIMEOUT.folders,
    });
    if (token !== folderToken) return;
    const dirs = normalizeCloudDirs(data.dirs || data.entries, path);
    folder.nodes.set(path, {
      loading: false,
      loaded: true,
      error: null,
      dirs,
      truncated: !!data.truncated,
    });
    renderFlow();
    requestCloudSizes(dirs.map((entry) => entry.path));
    requestExisting(dirs.map((entry) => entry.path));
  } catch (error) {
    if (token !== folderToken) return;
    folder.nodes.set(path, { loading: false, loaded: false, error, dirs: [], truncated: false });
    renderFlow();
  }
}

/* --- панель файлов (инлайн под строкой папки) ------------------------------ */

function formatFileDate(value) {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  const day = String(date.getDate()).padStart(2, '0');
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const year = String(date.getFullYear()).slice(-2);
  return `${day}.${month}.${year}`;
}

function previewFileSize(value) {
  const size = Number(value);
  if (!Number.isFinite(size) || size < 0) return '';
  return humanBytes(size);
}

function fileMarkup(path, file) {
  const filePath = cloudApiPath(`${path}/${file.name}`);
  const covered = folder.all || folderNodeChecked(path);
  const selected = folder.selectedFiles.includes(filePath);
  const disabled = covered;
  const checked = covered || selected;
  const size = previewFileSize(file.size);
  const date = formatFileDate(file.modified);
  const label = `Скачать файл «${file.name}»${size ? `, ${size}` : ''}`;
  return `<li class="file-row">
    <label class="file-main${disabled ? ' is-disabled' : ''}">
      <input class="file-check" type="checkbox" data-file="${escapeHtml(filePath)}"${checked ? ' checked' : ''}${disabled ? ' disabled aria-disabled="true"' : ''} aria-label="${escapeHtml(label)}">
      <span class="folder-check-mark" aria-hidden="true"></span>
      <span class="file-name" title="${escapeHtml(file.name)}">${escapeHtml(displayPath(file.name))}</span>
      <span class="file-size">${escapeHtml(size)}</span>
      <span class="file-date">${escapeHtml(date)}</span>
    </label>
  </li>`;
}

function filesPanelMarkup(path) {
  const entry = folder.files.get(path);
  const name = displayPath(shortPath(path) || path);
  const covered = folder.all || folderNodeChecked(path);
  const label = `Файлы в «${name}»`;
  const focusSelector = sourceFocusSelector();
  const children = [];
  if (!entry || entry.status === 'loading') {
    children.push(`<div class="files-state" role="status" aria-busy="true">${skeletonMarkup(3)}<p>Загружаем файлы…</p></div>`);
  } else if (entry.status === 'error') {
    children.push(`<div class="files-state files-state--error">
      <p>${escapeHtml(serverMessage(entry.error))}</p>
      <button class="btn btn--secondary btn--sm" type="button" data-files-retry="${escapeHtml(path)}">Повторить</button>
    </div>`);
  } else if (!entry.files.length) {
    children.push('<div class="files-state"><p>В папке нет файлов</p></div>');
  } else {
    if (covered) {
      children.push('<p class="files-note">Папка выбрана целиком — все файлы скачаются.</p>');
    }
    children.push(`<label class="file-select-all" for="files-all-${escapeHtml(encodeURIComponent(path))}">
      <input id="files-all-${escapeHtml(encodeURIComponent(path))}" class="file-check" type="checkbox" data-select-all="${escapeHtml(path)}"${covered ? ' checked disabled aria-disabled="true"' : ''}>
      <span class="folder-check-mark" aria-hidden="true"></span>
      <span>Выбрать все файлы</span>
    </label>`);
    children.push(`<ul class="file-list">${entry.files.map((file) => fileMarkup(path, file)).join('')}</ul>`);
    if (entry.truncated) {
      const shown = Math.max(entry.files.length, Number(entry.limit) || CLOUD_FILES_LIMIT);
      children.push(`<p class="files-note">Показаны первые ${shown} файлов. Чтобы скачать всё, выберите папку.</p>`);
      if (!covered) {
        children.push(`<button class="btn btn--quiet btn--sm files-choose" type="button" data-select-folder="${escapeHtml(path)}">Выбрать папку</button>`);
      }
    }
  }
  return `<div class="files-panel" id="${escapeHtml(filesPanelId(path))}" role="group" aria-label="${escapeHtml(label)}">
    <div class="files-head">
      <p class="files-title" title="${escapeHtml(label)}">${escapeHtml(label)}</p>
      <button class="btn btn--ghost btn--sm files-hide" type="button" data-files-close="${escapeHtml(path)}">К папкам</button>
    </div>
    ${children.join('')}
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

function cancelCloudFiles() {
  if (!folder || !folder.filesInflight) return;
  for (const controller of folder.filesInflight.values()) {
    try { controller.abort(); } catch { /* запрос уже завершён */ }
  }
  folder.filesInflight.clear();
}

async function requestCloudFiles(path) {
  if (!cloudFilesAvailable() || !path) return;
  const account = currentAccount();
  const entry = folder.files.get(path);
  if (!account || !entry) return;
  const token = folderToken;
  const limit = Number(entry.limit) || CLOUD_FILES_LIMIT;
  const controller = typeof AbortController === 'function' ? new AbortController() : null;
  if (controller) folder.filesInflight.set(path, controller);
  const isCurrent = () => token === folderToken
    && folder.files.get(path) === entry
    && (!controller || folder.filesInflight.get(path) === controller);
  entry.status = 'loading';
  entry.error = null;
  if (folder.filePanelPath === path) renderSourceTree();
  try {
    const data = await api(
      `/api/accounts/${encodeURIComponent(account.id)}/files?path=${encodeURIComponent(cloudApiPath(path))}&limit=${limit}`,
      { timeout: API_TIMEOUT.folders, signal: controller ? controller.signal : undefined },
    );
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
      cloudFilesUnavailable = true;
      cancelCloudFiles();
      folder.files.clear();
      folder.filePanelPath = '';
      renderSourceTree();
      return;
    }
    entry.files = [];
    entry.truncated = false;
    entry.count = 0;
    entry.error = error;
    entry.status = 'error';
  } finally {
    if (controller && folder.filesInflight.get(path) === controller) folder.filesInflight.delete(path);
    if (token === folderToken && folder.filePanelPath === path) renderSourceTree();
  }
}

function openFilePanel(path) {
  if (!path || !cloudFilesAvailable()) return;
  if (folder.filePanelPath === path) {
    closeFilePanel();
    return;
  }
  folder.filePanelPath = path;
  let entry = folder.files.get(path);
  renderSourceTree();
  if (!entry) {
    entry = {
      path,
      status: 'loading',
      files: [],
      count: 0,
      truncated: false,
      limit: CLOUD_FILES_LIMIT,
      error: null,
    };
    folder.files.set(path, entry);
    renderSourceTree();
    requestCloudFiles(path);
  }
}

function closeFilePanel() {
  const path = folder.filePanelPath;
  if (!path) return;
  folder.filePanelPath = '';
  renderSourceTree();
  const toggle = document.querySelector(`.folder-files-toggle[data-preview="${cssEscape(path)}"]`);
  if (toggle) toggle.focus({ preventScroll: true });
  else focusFolderContent();
}

function retryFilePanel(path) {
  const entry = folder.files.get(path);
  if (!entry || entry.status === 'loading') return;
  entry.status = 'loading';
  entry.error = null;
  entry.limit = CLOUD_FILES_LIMIT;
  renderSourceTree();
  requestCloudFiles(path);
}

/* --- размеры папок в облаке ---------------------------------------------- */

function folderSizeText(path) {
  if (cloudSizesUnavailable) return '';
  const entry = folder.sizes && folder.sizes.get(path);
  if (!entry) return '\u2026';
  if (entry.error) return '';
  return `${formatFiles(entry.files)} · ${humanBytes(entry.bytes)}`;
}

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
          cloudSizesUnavailable = true;
          break;
        }
        for (const path of chunk) folder.sizes.set(path, { error: true });
      }
    }
  } finally {
    for (const path of pending) folder.sizePending.delete(path);
    if (token === folderToken && folder.context === 'flow-source') renderSourceTree();
  }
}

/* --- что уже скачано в назначение ----------------------------------------- */

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

function resetExistingCache() {
  existingCache.clear();
  existingCacheScope = '';
  if (!folder) return;
  if (folder.existingPending) folder.existingPending.clear();
  if (folder.existingFailed) folder.existingFailed.clear();
  folder.existingError = '';
  folder.existingDest = '';
}

function existingKey(dest, path) {
  return `${dest}\u0000${path}`;
}

function existingEntry(path) {
  if (existingUnavailable || !flowDest || !path) return null;
  if (!syncExistingCache()) return null;
  return existingCache.get(existingKey(flowDest, path)) || null;
}

function existingIsDownloaded(entry) {
  return !!entry && entry.exists && entry.entries > 0;
}

function syncExistingDestination() {
  if (folder.existingDest === flowDest) return;
  folder.existingDest = flowDest;
  folder.existingPending.clear();
  folder.existingFailed.clear();
  folder.existingError = '';
  requestExistingForLoadedLevels();
}

async function requestExisting(paths, { retry = false } = {}) {
  if (existingUnavailable || folder.context !== 'flow-source' || !flowDest) return;
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
  if (retry) folder.existingError = '';
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
        const answered = new Set(items.map((item) => cloudApiPath(item && item.path)).filter(Boolean));
        for (const path of chunk) {
          if (!answered.has(path)) folder.existingFailed.add(path);
        }
      } catch (error) {
        if (token !== folderToken) return;
        if (error && (error.status === 404 || error.status === 405)) {
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
              title: 'Проверка недоступна на этом сервере',
              message: 'Можно выбирать любые папки.',
            });
          }
          if (folder.context === 'flow-source') {
            renderSourceBar();
            renderSourceTree();
            renderFlowFooter();
          }
          return;
        }
        for (const rest of chunks.slice(index)) {
          for (const path of rest) folder.existingFailed.add(path);
        }
        folder.existingError = serverMessage(error);
        break;
      }
    }
  } finally {
    for (const path of pending) folder.existingPending.delete(path);
    if (token === folderToken && folder.context === 'flow-source') {
      if (!folder.existingFailed.size) folder.existingError = '';
      renderSourceBar();
      renderSourceTree();
      renderFlowFooter();
    }
  }
}

function retryExisting() {
  if (existingUnavailable || !folder.existingFailed.size) return;
  requestExisting(Array.from(folder.existingFailed), { retry: true });
}

function requestExistingForLoadedLevels() {
  if (existingUnavailable || folder.context !== 'flow-source') return;
  for (const node of folder.nodes.values()) {
    if (!node || !node.loaded || !Array.isArray(node.dirs) || !node.dirs.length) continue;
    requestExisting(node.dirs.map((entry) => entry.path));
  }
}

/* --- переходы между шагами ------------------------------------------------ */

function syncSelectionFromFolder() {
  const account = currentAccount();
  excludedFolders = folder.all ? folder.excluded.slice().sort() : [];
  flowSourceAll = !!folder.all;
  flowShowExisting = !!folder.showExisting;
  cloudSelection = folder.all ? [] : folder.selected.slice().sort();
  cloudFileSelection = folder.all ? [] : folder.selectedFiles.slice().sort();
  cloudSelectionAccount = account ? account.id : '';
}

/* Шаг 1 → 2: фиксируем назначение, переносим сохранённый выбор в сессию.
   При входе из проверки тот же шаг возвращает к сводке с «Сохранить папку». */
function flowConfirmDestination() {
  if (folder.loading || folder.error || !folder.path || folder.writable === false) return;
  flowDest = displayPath(folder.path);
  flowFree = Number.isFinite(Number(folder.free)) ? Number(folder.free) : null;
  flowTotal = Number.isFinite(Number(folder.total)) ? Number(folder.total) : null;
  if (flowEditing === 'dest') {
    flowEditing = '';
    showFlowStep('flow-review');
    return;
  }
  flowStep = 2;
  folder.excluded = excludedFolders.slice();
  folder.all = !!flowSourceAll;
  folder.showExisting = !!flowShowExisting;
  folder.selected = cloudSelection.slice();
  folder.selectedFiles = cloudFileSelection.slice();
  folder.alert = '';
  showFlowStep('flow-source');
}

/* Шаг 2 → 1: выбор и раскрытые ветки остаются в сессии диалога. */
/* «Назад» из режима «Изменить» — отмена правки: сводка остаётся прежней,
   а незавершённый черновик не подменяет подтверждённые значения. */
function cancelFlowEdit() {
  if (flowEditing === 'source') {
    folder.selected = cloudSelection.slice();
    folder.selectedFiles = cloudFileSelection.slice();
    folder.alert = '';
  } else if (flowEditing === 'dest') {
    folder.path = flowDest || initialDestinationPath();
    folder.parent = null;
    folder.entries = [];
    folder.loaded = false;
    folder.error = null;
  }
  flowEditing = '';
  showFlowStep('flow-review');
}

function flowBackToDest() {
  if (folder.context !== 'flow-source') return;
  const tree = $('#flow-source-tree');
  if (tree) folder.treeScroll = tree.scrollTop;
  syncSelectionFromFolder();
  folder.alert = '';
  flowStep = 1;
  showFlowStep('flow-dest');
}

/* Шаг 2 → 3: подтверждаем источник и запускаем проверку. */
function flowToReview() {
  if (folderBusy()) return;
  const account = currentAccount();
  if (!account) return;
  if (!folder.all && !folder.selected.length && !folder.selectedFiles.length) {
    setFlowAlert('Выберите папки или файлы.');
    const first = document.querySelector('#flow-source-tree .folder-check:not([disabled])');
    if (first) first.focus({ preventScroll: true });
    return;
  }
  syncSelectionFromFolder();
  folder.alert = '';
  flowEditing = '';
  flowStep = 3;
  showFlowStep('flow-review');
}

/* --- шаг 3: проверка ------------------------------------------------------- */

function flowPreflightKey() {
  const account = currentAccount();
  return [
    account ? account.id : '',
    flowSourceAll ? 'all' : 'selected',
    JSON.stringify(excludedFolders),
    cloudSelection.slice().sort().join('\n'),
    cloudFileSelection.slice().sort().join('\n'),
    displayPath(flowDest),
    'copy',
  ].join('\u0001');
}

function preflightTimeout(error) {
  if (!error) return false;
  if (error.kind === 'timeout') return true;
  const status = Number(error.status) || 0;
  return status === 408 || status === 504;
}

function cancelPreflight() {
  clearTimeout(reviewWaitTimer);
  reviewWaitTimer = 0;
  if (flowPreflight && flowPreflight.controller) {
    try { flowPreflight.controller.abort(); } catch { /* запрос уже завершён */ }
    flowPreflight.controller = null;
  }
}

/* Автоматическая оценка при входе на шаг 3. Повторный вызов того же ключа не
   создаёт второй запрос; поздние ответы отбрасываются по seq/ключу. Смена
   выбора отменяет ожидание старого ответа и запускает новый расчёт. */
function ensurePreflight({ retry = false } = {}) {
  if (folder.context !== 'flow-review') return;
  const account = currentAccount();
  if (!account) return;
  const key = flowPreflightKey();
  if (flowPreflight.key !== key) {
    cancelPreflight();
    flowPreflight = { key, status: 'idle', data: null, error: null, seq: 0, controller: null, startedAt: 0 };
  }
  if (flowPreflight.status === 'loading') return;
  if (!retry && flowPreflight.status === 'ready') return;
  if (flowPreflight.controller) cancelPreflight();
  const seq = flowPreflight.seq + 1;
  const controller = typeof AbortController === 'function' ? new AbortController() : null;
  const startedAt = Date.now();
  clearTimeout(reviewWaitTimer);
  flowPreflight = {
    key,
    status: 'loading',
    data: retry ? null : flowPreflight.data,
    error: null,
    seq,
    controller,
    startedAt,
  };
  reviewWaitTimer = setTimeout(() => {
    if (folder.context === 'flow-review' && flowPreflight.status === 'loading') renderReview();
  }, Math.max(0, ESTIMATE_SLOW_MS - (Date.now() - startedAt)) + 60);
  renderReview();
  renderFlowFooter();
  const payload = {
    accountId: account.id,
    sourcePaths: flowSourceAll ? [] : cloudSelection.slice(),
    sourceFiles: flowSourceAll ? [] : cloudFileSelection.slice(),
    destPath: displayPath(flowDest),
  };
  (async () => {
    let data = null;
    let failure = null;
    try {
      data = await api('/api/preflight', {
        method: 'POST',
        timeout: API_TIMEOUT.preflight,
        signal: controller ? controller.signal : undefined,
        body: JSON.stringify(payload),
      });
    } catch (error) {
      failure = error;
    }
    if (seq !== flowPreflight.seq || flowPreflight.key !== key) return;
    if (controller && controller.signal.aborted) return;
    if (failure && failure.kind === 'aborted') return;
    clearTimeout(reviewWaitTimer);
    reviewWaitTimer = 0;
    flowPreflight = {
      key,
      status: failure ? 'error' : 'ready',
      data: failure ? null : (data || {}),
      error: failure,
      seq,
      controller: null,
      startedAt,
    };
    if (folder.context === 'flow-review' && !folder.finished) {
      renderReview();
      renderFlowFooter();
      const view = estimateView();
      if (view.kind === 'error') {
        flowAnnounce(preflightTimeout(failure)
          ? 'Оценка не завершилась за 10 минут'
          : 'Не удалось оценить объём');
      } else if (view.kind === 'nofit') {
        flowAnnounce(`Не хватает ${humanBytes(estimateShortage(view))}`);
      } else if (view.partial) {
        flowAnnounce(`Учтено ${formatFiles(view.files)} · ${humanBytes(view.bytes)}. Часть файлов не удалось оценить`);
      } else {
        flowAnnounce(`Объём: ${formatFiles(view.files)} · ${humanBytes(view.bytes)}`);
      }
    }
  })();
}

/* Одно место, где решается, что показать в строках «К скачиванию». */
function estimateView() {
  const cached = flowPreflight;
  if (cached.status === 'idle' || cached.status === 'loading') {
    return {
      kind: 'loading',
      slow: cached.status === 'loading' && cached.startedAt > 0
        && Date.now() - cached.startedAt >= ESTIMATE_SLOW_MS,
      files: 0,
      bytes: 0,
      free: 0,
      partial: false,
    };
  }
  if (cached.status === 'error') {
    return { kind: 'error', timeout: preflightTimeout(cached.error), files: 0, bytes: 0, free: 0, partial: false };
  }
  const data = cached.data || {};
  const files = Math.max(0, Number(data.files) || 0);
  const bytes = Math.max(0, Number(data.bytes) || 0);
  const free = Math.max(0, Number(data.free) || 0);
  /* Частичный результат: сервер сам сообщает, что часть файлов не оценена.
     Без этого флага объём считается полным. */
  const partial = data.partial === true || (Number(data.unknownFiles) || 0) > 0;
  return { kind: data.fits === false ? 'nofit' : 'ready', files, bytes, free, partial };
}

function estimateShortage(view) {
  /* Сервер блокирует перенос при 90% свободного места (transferLimit). */
  const usable = Math.floor((Number(view.free) || 0) * 0.9);
  return Math.max(0, (Number(view.bytes) || 0) - usable);
}

function knownFree(view) {
  if (view.kind === 'ready' || view.kind === 'nofit') {
    if (Number(view.free) > 0) return Number(view.free);
  }
  return Number(flowFree) > 0 ? Number(flowFree) : 0;
}

function estimateValueMarkup() {
  const view = estimateView();
  if (view.kind === 'loading') {
    let html = '<span class="review-value review-value--muted review-value--total" id="review-estimate" tabindex="-1">Считаем объём…</span>';
    if (view.slow) html += '<span class="review-sub">Оценка ещё идёт</span>';
    return html;
  }
  if (view.kind === 'error') {
    const title = view.timeout ? 'Оценка не завершилась за 10 минут' : 'Не удалось оценить объём';
    return `<span class="review-value review-value--muted review-value--total" id="review-estimate" tabindex="-1">${escapeHtml(title)}</span>
      <button class="btn btn--quiet btn--sm review-retry" type="button" id="review-retry">Повторить</button>`;
  }
  let html = `<span class="review-value review-value--total" id="review-estimate" tabindex="-1">${escapeHtml(formatFiles(view.files))} · ${escapeHtml(humanBytes(view.bytes))}</span>`;
  if (view.kind === 'nofit') {
    html += `<span class="review-short">${icon('warning', 'review-short-icon')}Не хватает ${escapeHtml(humanBytes(estimateShortage(view)))}</span>`;
  } else if (view.partial) {
    html += `<span class="review-sub">Часть файлов не удалось оценить</span>
      <button class="btn btn--quiet btn--sm review-retry" type="button" id="review-retry">Повторить оценку</button>`;
  }
  if (view.files === 0 && view.bytes === 0) {
    html += '<span class="review-sub">Файлов не найдено — возможно, выбранные папки пусты.</span>';
  }
  return html;
}

/* Чипы источника: до 3 папок и до 3 файлов, остальные — «+N». */
function reviewChips(folders, files) {
  const shownFolders = reviewShowAll ? folders : folders.slice(0, 3);
  const shownFiles = reviewShowAll ? files : files.slice(0, 3);
  const hiddenCount = (folders.length - shownFolders.length) + (files.length - shownFiles.length);
  const chips = [
    ...shownFolders.map((path) => `<li class="review-chip" title="${escapeHtml(displayCloudPath(path))}">${escapeHtml(displayCloudPath(path))}</li>`),
    ...shownFiles.map((path) => `<li class="review-chip review-chip--file" title="${escapeHtml(displayPath(path))}">${icon('file')}<span>${escapeHtml(shortPath(path))}</span></li>`),
  ];
  let more = '';
  if (hiddenCount > 0) {
    more = `<li class="review-chip-more"><button class="btn btn--quiet btn--sm" type="button" id="review-show-all" aria-expanded="${reviewShowAll}">${reviewShowAll ? 'Свернуть' : `+${hiddenCount}`}</button></li>`;
  }
  return `<ul class="review-chips">${chips.join('')}${more}</ul>`;
}

function captureReviewFocus(scope) {
  const active = document.activeElement;
  if (!active || !scope.contains(active)) return '';
  if (active.id === 'review-retry') return '#review-retry';
  if (active.id === 'review-show-all') return '#review-show-all';
  if (active.id === 'review-estimate') return '#review-estimate';
  return '';
}

function renderReview() {
  const body = $('#review-body');
  if (!body || folder.context !== 'flow-review') return;
  const focusSelector = captureReviewFocus(body);
  const folders = flowSourceAll ? [] : cloudSelection;
  const files = flowSourceAll ? [] : cloudFileSelection;
  const sourceValue = flowSourceAll ? wholeDiskLabel(excludedFolders) : selectionLabel(folders, files);
  const view = estimateView();
  const free = knownFree(view);
  body.innerHTML = `
    <dl class="review-list">
      <div class="review-row">
        <dt>Источник</dt>
        <dd>
          <div class="review-line">
            <span class="review-value review-value--source">${escapeHtml(sourceValue)}</span>
            <button class="review-change" type="button" data-edit="source">Изменить</button>
          </div>
          ${folders.length || files.length ? reviewChips(folders, files) : ''}
        </dd>
      </div>
      <div class="review-row">
        <dt>Папка на сервере</dt>
        <dd>
          <div class="review-line">
            <span class="review-path" title="${escapeHtml(flowDest)}">${escapeHtml(flowDest)}</span>
            <button class="review-change" type="button" data-edit="dest">Изменить</button>
          </div>
        </dd>
      </div>
      <div class="review-row">
        <dt>К скачиванию</dt>
        <dd class="review-estimate">${estimateValueMarkup()}</dd>
      </div>
      <div class="review-row">
        <dt>Свободно</dt>
        <dd><span class="review-value">${free > 0 ? escapeHtml(humanBytes(free)) : '—'}</span></dd>
      </div>
    </dl>`;
  let target = focusSelector ? body.querySelector(focusSelector) : null;
  if (!target && focusSelector === '#review-retry') target = body.querySelector('#review-estimate');
  if (target && !target.disabled) target.focus({ preventScroll: true });
}

function clearReviewError() {
  const node = $('#review-error');
  if (!node) return;
  node.hidden = true;
  node.textContent = '';
}

function reviewError(message) {
  const node = $('#review-error');
  if (!node) return;
  node.textContent = message || '';
  node.hidden = !message;
  if (message) node.focus({ preventScroll: true });
}

/* Потеря ответа: две повторные отправки того же ключа с нарастающей паузой. */
const START_RETRY_ATTEMPTS = 2;
const START_RETRY_DELAY = 800;

async function startTransfer() {
  if (flowBusy) return;
  const account = currentAccount();
  const dialog = $('#folder-dialog');
  if (!account) {
    requestCloseFlow();
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
  if (estimateView().kind === 'nofit') {
    reviewError('Свободного места не хватает. Измените папку или выбор.');
    return;
  }
  const primary = $('#folder-choose');
  const back = $('#folder-back');
  const estimate = estimateView();
  const facts = estimate.kind === 'ready' || estimate.kind === 'nofit'
    ? ` · ${formatFiles(estimate.files)} · ${humanBytes(estimate.bytes)}`
    : '';
  const sourcePaths = flowSourcePaths();
  const sourceFiles = flowSourceFiles();
  const destPath = flowDest;
  /* Тот же выбор — тот же ключ: повтор после потери ответа не создаст вторую
     задачу. Смена выбора или успешный старт дают новый ключ. */
  const token = launchToken(flowPreflightKey());
  flowBusy = true;
  folder.status = 'Запускаем…';
  renderFlowFooter();
  flowAnnounce('Запускаем скачивание');
  if (primary) {
    setBusy(primary, true);
    primary.setAttribute('aria-label', 'Запускаем скачивание');
  }
  if (back) back.setAttribute('aria-disabled', 'true');
  if (dialog) {
    dialog.setAttribute('aria-busy', 'true');
    setDialogCancelBlocked(dialog, true);
  }
  let result = null;
  let failure = null;
  try {
    result = await createJob(account, sourcePaths, sourceFiles, destPath, token);
  } catch (error) {
    failure = error;
  }
  if (!failure) {
    finishTransfer(dialog, sourcePaths, sourceFiles, destPath, facts, replayedAnswer(result));
    return;
  }
  if (idempotencyConflict(failure)) {
    resetLaunchToken();
    releaseStart();
    reviewError('Параметры изменились — проверьте выбор и запустите снова');
    return;
  }
  if (!startOutcomeUnknown(failure)) {
    releaseStart();
    reviewError(friendlyError(failure));
    return;
  }
  /* Ответ потерян. Повторяем тот же запрос с тем же ключом: сервер либо
     создаст задачу, либо вернёт уже созданную (200 replay), но не вторую. */
  folder.status = 'Проверяем, началось ли скачивание…';
  renderFlowFooter();
  flowAnnounce('Проверяем, началось ли скачивание');
  for (let attempt = 0; attempt < START_RETRY_ATTEMPTS; attempt += 1) {
    await new Promise((resolve) => setTimeout(resolve, START_RETRY_DELAY * (attempt + 1)));
    try {
      result = await createJob(account, sourcePaths, sourceFiles, destPath, token);
    } catch (error) {
      failure = error;
      if (idempotencyConflict(error)) {
        resetLaunchToken();
        releaseStart();
        reviewError('Параметры изменились — проверьте выбор и запустите снова');
        return;
      }
      if (!startOutcomeUnknown(error)) {
        releaseStart();
        reviewError(friendlyError(error));
        return;
      }
      continue;
    }
    finishTransfer(dialog, sourcePaths, sourceFiles, destPath, facts, replayedAnswer(result));
    return;
  }
  releaseStart();
  reviewError('Не удалось подтвердить запуск. Проверьте «Сейчас»: возможно, задача уже началась.');
}

/* Ключ на попытку запуска: неизменный выбор переиспользует ключ, другой —
   получает новый. */
function launchToken(key) {
  if (!flowLaunchToken || flowLaunchKey !== key) {
    flowLaunchToken = newLaunchToken();
    flowLaunchKey = key;
  }
  return flowLaunchToken;
}

/* randomUUID есть не во всех браузерах; фолбэк остаётся в алфавите ключа
   [A-Za-z0-9._:-] и длине 8–128 символов. */
function newLaunchToken() {
  try {
    if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
      return crypto.randomUUID();
    }
  } catch { /* старый браузер — ниже */ }
  try {
    const bytes = new Uint8Array(16);
    crypto.getRandomValues(bytes);
    return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
  } catch {
    return `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 12)}`;
  }
}

function resetLaunchToken() {
  flowLaunchToken = '';
  flowLaunchKey = '';
}

/* POST /api/jobs с ключом. meta отличает 201 (создано) от 200 (повтор). */
async function createJob(account, sourcePaths, sourceFiles, destPath, token) {
  return api('/api/jobs', {
    method: 'POST',
    timeout: API_TIMEOUT.mutate,
    meta: true,
    body: JSON.stringify({
      accountId: account.id,
      /* Ключ считается по отсортированному выбору, поэтому тело тоже
         каноническое: смена порядка папок не даст 409 на тот же набор. */
      sourcePaths: sourcePaths.slice().sort(),
      sourceFiles: sourceFiles.slice().sort(),
      destPath,
      /* Интерфейс всегда только добавляет и обновляет файлы: mode=copy. */
      mode: 'copy',
      clientToken: token,
    }),
  });
}

function replayedAnswer(result) {
  if (!result) return false;
  if ((Number(result.status) || 0) === 200) return true;
  return !!(result.headers && result.headers.get && result.headers.get('Idempotent-Replay') === 'true');
}

function idempotencyConflict(error) {
  if (!error || (Number(error.status) || 0) !== 409) return false;
  return String((error.data && error.data.code) || '') === 'idempotency_conflict';
}

/* Таймаут или обрыв сети: задача могла создаться, а ответ — нет. */
function startOutcomeUnknown(error) {
  if (!error) return false;
  return error.kind === 'timeout' || error.kind === 'network' || (Number(error.status) || 0) === 0;
}

function releaseStart() {
  const primary = $('#folder-choose');
  const back = $('#folder-back');
  const dialog = $('#folder-dialog');
  flowBusy = false;
  folder.status = '';
  if (primary) {
    setBusy(primary, false);
    primary.removeAttribute('aria-label');
  }
  if (back) back.removeAttribute('aria-disabled');
  if (dialog) {
    dialog.removeAttribute('aria-busy');
    setDialogCancelBlocked(dialog, false);
  }
  renderReview();
  renderFlowFooter();
}

function finishTransfer(dialog, sourcePaths, sourceFiles, destPath, facts, replayed) {
  flowFocusNow = true;
  resetFlowDraft();
  if (dialog && dialog.open) dialog.close();
  showToast({
    type: 'ok',
    title: replayed ? 'Скачивание уже запущено' : 'Скачивание началось',
    message: `${selectionLabel(sourcePaths, sourceFiles)} → ${displayPath(destPath)}${facts}`,
  });
  refreshAfterAction().catch(() => {}).then(() => {
    const title = $('#now-title');
    if (title) title.focus({ preventScroll: true });
  });
}

/* --- закрытие ------------------------------------------------------------- */

function requestCloseFlow() {
  const dialog = $('#folder-dialog');
  if (!dialog || !dialog.open) return;
  if (flowBusy) return;
  /* Закрытие X/Esc не очищает черновик: выбор шага 2 сохраняем в модуле. */
  if (folder.context === 'flow-source') {
    const tree = $('#flow-source-tree');
    if (tree) folder.treeScroll = tree.scrollTop;
    syncSelectionFromFolder();
  }
  forceCloseFolderDialog();
}

function forceCloseFolderDialog() {
  const dialog = $('#folder-dialog');
  if (!dialog) return;
  if (dialog.open) dialog.close();
  finishFolderDialog();
}

function finishFolderDialog() {
  const dialog = $('#folder-dialog');
  if (dialog && dialog.open) return;
  resumeAfterDialog();
  if (!folder || folder.finished) return;
  folder.finished = true;
  const context = folder.context;
  const opener = folder.opener;
  const chosen = folder.chosen;
  const target = folder.target;
  cancelCloudFiles();
  folderToken += 1;
  /* Черновик флоу остаётся в памяти; сбрасываем только сессию диалога. */
  folder = folderSession('setup', null);
  if (flowFocusNow) {
    flowFocusNow = false;
    return;
  }
  if (context === 'setup') {
    const field = chosen && target === 'server' ? document.getElementById('f-dest') : null;
    const nextFocus = field || opener;
    if (nextFocus && nextFocus.isConnected && nextFocus.getClientRects().length) {
      focusSoon(nextFocus, opener || null);
    }
    return;
  }
  const targetFocus = opener && opener.isConnected && opener.getClientRects().length ? opener : $('#flow-start');
  if (targetFocus && targetFocus.isConnected && targetFocus.getClientRects().length) focusSoon(targetFocus);
}

/* Пикер в мастере: фиксируем папку и закрываем диалог. */
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

/* --- общий обработчик кнопок футера --------------------------------------- */

function flowChooseAction() {
  if (folder.context === 'flow-dest') {
    flowConfirmDestination();
    return;
  }
  if (folder.context === 'flow-source') {
    flowToReview();
    return;
  }
  if (folder.context === 'flow-review') {
    startTransfer();
    return;
  }
  chooseSetupFolder();
}

function flowBackAction() {
  if (folder.context === 'flow-dest') {
    if (flowEditing === 'dest') {
      cancelFlowEdit();
      return;
    }
    requestCloseFlow();
    return;
  }
  if (folder.context === 'setup') {
    requestCloseFlow();
    return;
  }
  if (folder.context === 'flow-source') {
    if (flowEditing === 'source') {
      cancelFlowEdit();
      return;
    }
    flowBackToDest();
    return;
  }
  if (folder.context === 'flow-review') {
    if (flowBusy) return;
    flowEditing = '';
    flowStep = 2;
    showFlowStep('flow-source');
  }
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

  if (action === 'dismiss-job') {
    const job = accountJobs().find(item => item.id === id);
    if (!job || job.status !== 'done') return;
    dismissedJobs.add(id);
    try { localStorage.setItem('cloud-ferry.dismissed-jobs', JSON.stringify([...dismissedJobs])); }
    catch { /* Скрытие продолжает работать до перезагрузки страницы. */ }
    renderNow();
    return;
  }

  if (action === 'exit') {
    trigger.closest('details').open = false;
    openExitDialog();
    return;
  }

  if (action === 'start-flow') {
    openFlowStart(trigger);
    return;
  }

  if (action === 'account-open') {
    openAccountPanel();
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

/* Панель аккаунта: чип — единственный вход, Esc и «Закрыть» возвращают
   фокус на него. Проверка и выход живут внутри панели, не в меню. */
document.addEventListener('click', (event) => {
  const menu = document.querySelector('.drive-menu[open]');
  if (menu && !menu.contains(event.target)) menu.open = false;
});
document.addEventListener('keydown', (event) => {
  const menu = document.querySelector('.drive-menu[open]');
  if (event.key === 'Escape' && menu) {
    menu.open = false;
    menu.querySelector('summary').focus();
  }
});
$('#account-chip')?.addEventListener('click', () => openAccountPanel());
$('#account-probe')?.addEventListener('click', () => probeAccount($('#account-probe')));
$('#account-reconnect')?.addEventListener('click', () => {
  closeAccountPanel(false);
  location.hash = '#/setup';
});
$('#account-logout')?.addEventListener('click', () => { openExitDialog(); });

$('#account-dialog')?.addEventListener('keydown', (event) => keepDialogFocus(event.currentTarget, event));
$('#account-dialog')?.addEventListener('close', () => { finishAccountDialog(); });
$('#account-dialog')?.addEventListener('cancel', (event) => {
  event.preventDefault();
  closeAccountPanel(true);
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
/* Chrome может не присылать close после Esc-закрытия, поэтому очистку и возврат
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
  const menuTrigger = document.querySelector('.drive-menu summary');
  if (menuTrigger) focusSoon(menuTrigger);
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

/* Пока идёт запуск скачивания, «Закрыть» и Esc не должны закрывать диалог. */
function setDialogCancelBlocked(dialog, blocked) {
  if (!dialog) return;
  for (const button of dialog.querySelectorAll('[data-dialog-close]')) {
    if (blocked) button.setAttribute('aria-disabled', 'true');
    else button.removeAttribute('aria-disabled');
  }
}

function closeDialog(dialog) {
  if (!dialog || !dialog.open) return false;
  if (dialog.id === 'exit-dialog' && exitBusy) return false;
  if (dialog.id === 'folder-dialog') {
    if (flowBusy) return false;
    requestCloseFlow();
    return true;
  }
  dialog.close();
  if (dialog.id === 'exit-dialog') finishExitDialog();
  return true;
}

/* Смена hash закрывает открытые диалоги; черновик флоу при этом сохраняется. */
function closeDialogsForRoute() {
  for (const dialog of $$('dialog[open]')) {
    if (dialog.id === 'exit-dialog' && exitBusy) continue;
    if (dialog.id === 'folder-dialog') {
      forceCloseFolderDialog();
      continue;
    }
    closeDialog(dialog);
  }
}

for (const button of $$('[data-dialog-close]')) {
  button.addEventListener('click', () => {
    const dialog = button.closest('dialog');
    if (dialog && dialog.id === 'folder-dialog') {
      requestCloseFlow();
      return;
    }
    closeDialog(dialog);
  });
}

$('#exit-dialog')?.addEventListener('keydown', (event) => keepDialogFocus(event.currentTarget, event));
$('#exit-dialog')?.addEventListener('close', () => { finishExitDialog(); });
$('#exit-dialog')?.addEventListener('cancel', (event) => {
  event.preventDefault();
  if (exitBusy) return; // Esc не прерывает уже отправленный DELETE
  closeDialog(event.currentTarget);
});

/* --- события флоу «Новое скачивание» -------------------------------------- */

/* Esc: сначала закрывает открытую форму создания папки, затем диалог.
   Во время запуска задачи Esc игнорируется (X/«Назад» — aria-disabled). */
function handleFlowEscape() {
  if (flowBusy) return;

  if (folder.confirmPath) {
    cancelFolderConfirm(folder.confirmPath);
    return;
  }
  if (folder.createOpen && folder.context !== 'flow-source') {
    closeFolderCreate();
    return;
  }
  requestCloseFlow();
}

$('#folder-dialog')?.addEventListener('keydown', (event) => {
  if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    handleFlowEscape();
    return;
  }
  keepDialogFocus(event.currentTarget, event);
});
$('#folder-dialog')?.addEventListener('close', () => { finishFolderDialog(); });
$('#folder-dialog')?.addEventListener('cancel', (event) => {
  event.preventDefault();
  handleFlowEscape();
});

$('#folder-choose')?.addEventListener('click', flowChooseAction);
$('#folder-back')?.addEventListener('click', flowBackAction);

$('#folder-up')?.addEventListener('click', (event) => {
  if (event.currentTarget.getAttribute('aria-disabled') === 'true') return;
  navigateUp();
});

$('#folder-create')?.addEventListener('click', openFolderCreate);
$('#folder-create-cancel')?.addEventListener('click', () => closeFolderCreate());
$('#folder-create-panel')?.addEventListener('submit', (event) => {
  event.preventDefault();
  createFolder();
});
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

/* Шаг 1: список папок на сервере и повтор загрузки. */
$('#flow-dest-body')?.addEventListener('click', (event) => {
  if (event.target.closest('[data-folder-retry]')) {
    loadFolder(folder.path || initialDestinationPath(), true);
    return;
  }
  const row = event.target.closest('[data-folder-path]');
  if (!row) return;
  folderNavigate(row.dataset.folderPath);
});

/* Шаг 2: клавиши/клики по дереву: eye, шеврон, повтор уровня, «Выбрать папку». */
$('#flow-source-tree')?.addEventListener('click', (event) => {
  const confirmYes = event.target.closest('[data-confirm-folder]');
  if (confirmYes) {
    confirmFolderSelection(confirmYes.dataset.confirmFolder || '');
    return;
  }
  const confirmNo = event.target.closest('[data-confirm-cancel]');
  if (confirmNo) {
    cancelFolderConfirm(confirmNo.dataset.confirmCancel || '');
    return;
  }
  /* Смешанная галка означает частичный выбор. Клик по ней не расширяет
     выбор молча — сначала компактное подтверждение на всю папку. Chrome
     меняет checked до события click, поэтому смотрим на модель выбора. */
  const checkbox = event.target.closest('.folder-check');
  if (checkbox && !checkbox.disabled) {
    const path = checkbox.dataset.path || '';
    if (!folder.all && !folderNodeChecked(path) && folderIsIndeterminate(path)) {
      event.preventDefault();
      checkbox.checked = false;
      requestFolderConfirm(path);
      return;
    }
  }
  const filesClose = event.target.closest('[data-files-close]');
  if (filesClose) {
    closeFilePanel();
    return;
  }
  const filesRetry = event.target.closest('[data-files-retry]');
  if (filesRetry) {
    retryFilePanel(filesRetry.dataset.filesRetry || '');
    return;
  }
  const selectFolder = event.target.closest('[data-select-folder]');
  if (selectFolder) {
    const path = selectFolder.dataset.selectFolder || '';
    toggleFolderSelection(path, true);
    return;
  }
  const filesToggle = event.target.closest('[data-preview]');
  if (filesToggle) {
    openFilePanel(filesToggle.dataset.preview || '');
    return;
  }
  if (event.target.closest('[data-folder-retry]')) {
    loadCloudNode('');
    return;
  }
  const retry = event.target.closest('[data-retry-node]');
  if (retry) {
    loadCloudNode(retry.dataset.retryNode || '');
    return;
  }
  const expand = event.target.closest('[data-expand]');
  if (expand) {
    toggleCloudNode(expand.dataset.expand || '');
  }
});

$('#flow-source-tree')?.addEventListener('change', (event) => {
  const target = event.target;
  if (target.matches('.folder-check')) {
    const path = target.dataset.path || '';
    /* Запоздавший change от клика по смешанной галке: выбор уже подтверждается
       отдельным действием, тихо расширять его нельзя. */
    if (folder.confirmPath === path && target.checked) {
      target.checked = false;
      return;
    }
    toggleFolderSelection(path, target.checked);
    return;
  }
  if (target.matches('.file-check') && target.dataset.selectAll) {
    toggleAllFiles(target.dataset.selectAll, target.checked);
    return;
  }
  if (target.matches('.file-check') && target.dataset.file) {
    toggleFileSelection(target.dataset.file, target.checked);
  }
});

$('#select-disk').addEventListener('change', event => {
  folder.all = event.target.checked;
  folder.excluded = [];
  folder.selected = [];
  folder.selectedFiles = [];
  folder.alert = '';
  folder.confirmPath = '';
  renderFlow();
  requestExistingForLoadedLevels();
});

/* «Показать уже скачанные» — только видимость. Выбор и payload не меняются:
   снятая галка прячет известные совпадения, но не трогает отметки. */
$('#folder-show-existing')?.addEventListener('change', (event) => {
  if (folder.context !== 'flow-source') return;
  folder.showExisting = !!event.target.checked;
  flowShowExisting = folder.showExisting;
  folder.confirmPath = '';
  renderSourceBar();
  renderSourceTree();
  renderFlowFooter();
  flowAnnounce(folder.showExisting ? 'Показаны уже скачанные' : 'Уже скачанные скрыты');
});

$('#folder-existing-retry')?.addEventListener('click', retryExisting);

/* Шаг 3: повтор оценки, «Изменить» у строк сводки, раскрытие выбора. */
$('#review-body')?.addEventListener('click', (event) => {
  if (event.target.closest('#review-retry')) {
    ensurePreflight({ retry: true });
    const anchor = $('#review-estimate');
    if (anchor) anchor.focus({ preventScroll: true });
    return;
  }
  if (event.target.closest('#review-show-all')) {
    reviewShowAll = !reviewShowAll;
    renderReview();
    const button = $('#review-show-all');
    if (button) button.focus({ preventScroll: true });
    return;
  }
  const edit = event.target.closest('[data-edit]');
  if (!edit) return;
  clearReviewError();
  if (edit.dataset.edit === 'source') {
    flowEditing = 'source';
    showFlowStep('flow-source');
    return;
  }
  flowEditing = 'dest';
  showFlowStep('flow-dest');
});

$('#exit-confirm')?.addEventListener('click', confirmExit);
for (const button of $$('.js-offline-retry')) {
  button.addEventListener('click', () => retryConnection(button));
}

window.addEventListener('hashchange', () => {
  route();
});

/* --- boot ----------------------------------------------------------------- */

(async function boot() {
  try {
    await loadData();
    hideBoot();
    route();
  } catch {
    /* Первый запрос не прошёл: скелет остаётся на месте, под шапкой
       появляется полоса «Нет связи» с повтором. */
    const bootView = $('#boot-view');
    if (bootView) bootView.setAttribute('aria-busy', 'false');
    setOffline(true);
  } finally {
    /* Даже если первый запрос не прошёл, опрос должен сам восстановить
       связь — иначе «Повторить» остаётся единственным путём. */
    startPolling();
  }
})();
