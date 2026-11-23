'use strict';

/* Способы входа. «Войти через Яндекс» — основной, когда сервер настроен:
   пароль приложения и токен остаются запасными и видны ниже. */
const SETUP_METHODS = {
  yandex: 'Яндекс ID',
  webdav: 'Пароль приложения',
  'yandex-token': 'Токен',
};

const YANDEX_DEVICE_URL = 'https://oauth.yandex.ru/device';
/* Требование макета: проверяем подтверждение каждые 5 секунд. */
const YANDEX_POLL_MS = 5000;
const YANDEX_ACCOUNT_NAME = 'Яндекс Диск';

function setupFieldValue(name) {
  return escapeHtml(wizard.fields[name] || '');
}

/* Сегменты-переключатели: все доступные способы видны сразу, выбор не теряет
   ввод. Основной способ показываем первым. */
function availableMethods() {
  return Object.keys(SETUP_METHODS).filter((method) => method !== 'yandex' || yandexAuth.available === true || wizard.customOAuth);
}

function methodSwitch() {
  const options = availableMethods().map((method) => {
    const active = wizard.method === method;
    return `<button class="method-option" type="button" role="radio" id="method-${method}" data-method="${method}" aria-checked="${active ? 'true' : 'false'}" tabindex="${active ? '0' : '-1'}">${escapeHtml(SETUP_METHODS[method])}</button>`;
  }).join('');
  return `<div class="method-switch" role="radiogroup" aria-label="Способ входа">${options}</div>`;
}

function loginFields() {
  return `
    <form class="setup-form" id="setup-form" novalidate autocomplete="on">
      <div class="control">
        <label class="control-label" for="f-user">Логин Яндекса</label>
        <input id="f-user" name="username" type="text" autocomplete="username" inputmode="text" spellcheck="false" autocapitalize="none" autocorrect="off" placeholder="Логин на Яндексе" value="${setupFieldValue('user')}">
      </div>
      <div class="control">
        <label class="control-label" for="f-pass">Пароль приложения</label>
        <div class="field-with-action">
          <input id="f-pass" name="password" type="password" autocomplete="current-password" inputmode="text" spellcheck="false" autocapitalize="none" autocorrect="off" placeholder="Пароль из Яндекс ID" value="${setupFieldValue('pass')}">
          <button class="field-action" type="button" id="f-pass-toggle" aria-label="Показать пароль" aria-pressed="false" title="Показать пароль">${icon('eye')}</button>
        </div>
      </div>
      <p class="note" id="webdav-note"><a href="https://id.yandex.ru/security/app-passwords" target="_blank" rel="noreferrer">Создать пароль приложения в Яндекс ID ↗</a></p>
      <button class="sr-only" type="submit" tabindex="-1" aria-hidden="true">Подключиться</button>
    </form>`;
}

function tokenFields() {
  return `
    <form class="setup-form" id="setup-form" novalidate autocomplete="off">
      <div class="control control--token">
        <div class="control-head">
          <label class="control-label" for="f-token">Токен</label>
          <button class="token-help-toggle" type="button" id="token-help-toggle">${icon('code')}Как получить токен?</button>
        </div>
        <textarea id="f-token" name="token" rows="3" spellcheck="false" autocapitalize="none" autocorrect="off" autocomplete="off" placeholder="Вставьте токен или JSON из ответа">${setupFieldValue('token')}</textarea>
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
      ${yandexAuth.available ? '<p class="note" id="yandex-note">Подтвердите вход в своём аккаунте Яндекса.</p>' : `
        <div class="control">
          <label class="control-label" for="f-client">ID OAuth-приложения</label>
          <input id="f-client" type="text" autocomplete="off" spellcheck="false" value="${setupFieldValue('clientID')}">
        </div>
        <div class="control">
          <label class="control-label" for="f-secret">Секрет приложения</label>
          <input id="f-secret" type="password" autocomplete="off" value="${setupFieldValue('clientSecret')}">
        </div>
        <p class="note"><a href="https://oauth.yandex.ru/client/new" target="_blank" rel="noreferrer">Создать приложение в Яндексе ↗</a> с доступом к чтению Диска.</p>`}
      <button class="btn btn--primary" type="button" id="yandex-start">Получить код</button>
    </div>`;
}

function yandexWaitingFields() {
  const code = formatUserCode(yandexAuth.userCode);
  const url = safeHttpUrl(yandexAuth.verificationUrl, YANDEX_DEVICE_URL);
  return `
    <div class="yandex-panel" id="yandex-panel">
      <p class="control-label" id="yandex-code-label">Код для входа</p>
      <div class="code-row"><p class="yandex-code" id="yandex-code" tabindex="-1" aria-labelledby="yandex-code-label">${escapeHtml(code)}</p><button type="button" class="btn btn--quiet btn--icon" id="copy-device-code" aria-label="Скопировать код" title="Скопировать код">${icon('copy')}</button></div>
      <p class="yandex-hint"><a class="btn btn--primary" href="${escapeHtml(url)}" target="_blank" rel="noreferrer">Ввести код в Яндексе ↗</a></p>
      <p class="yandex-timer" id="yandex-timer">Код действует <span id="yandex-timer-value">${formatCountdown(yandexAuth.remaining)}</span></p>
      <p class="yandex-state" id="yandex-state" role="status" aria-live="polite"><span class="yandex-pulse" aria-hidden="true"></span><span id="yandex-state-text"></span></p>
      <button class="btn btn--secondary" type="button" id="yandex-cancel">Отменить</button>
    </div>`;
}

function yandexFailureFields() {
  const retry = yandexAuth.retryLabel || (yandexAuth.hasCode ? 'Получить новый код' : 'Получить код');
  return `
    <div class="yandex-panel yandex-panel--error" id="yandex-panel">
      <p class="yandex-state yandex-state--error" id="yandex-state" role="status" aria-live="polite">${icon('warning')}<span id="yandex-state-text"></span></p>
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
  if (wizard.method !== 'yandex') return '';
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

function setupStepBody() {
  return `
      <h1 class="setup-title" id="setup-title" tabindex="-1">Подключить Яндекс Диск</h1>
      ${methodSwitch()}
      <div class="setup-fields" id="setup-fields">${methodFields()}</div>`;
}
