# Cloud Ferry — тёмная тема и чистка мёртвого кода (design v3)

Дата: 18 ноября 2026.
Основа: `docs/design-v3/INTEGRATION.md`, список задачи `task-7`.
Изменены только `internal/httpapi/web/{index.html,style.css,app.js}` и `docs/design-v3/`.
Go-код не менялся. Коммит не делался.

## 1. Что добавлено

### 1.1. Тема до первого кадра

В `<head>` перед `<link rel="stylesheet">` добавлен inline-скрипт:

1. читает `localStorage['cloudferry-theme']` (`light` | `dark`), иначе
   `prefers-color-scheme`;
2. ставит `data-theme` и `color-scheme` на `<html>`;
3. обновляет `meta[name="theme-color"]` (`#f5f4f8` / `#16151a`).

Скрипт стоит до стилей, поэтому первый вычисленный кадр уже в нужной теме:
на `DOMContentLoaded` `data-theme` и фон `body` совпадают с темой
(`theme-cleanup-report.json`, проверки «theme set before DOMContentLoaded»
и «first styled paint in theme»). Если `localStorage` недоступен, тема
безопасно берётся из системной настройки.

### 1.2. Переключатель

Две кнопки одного класса `.js-theme-toggle` (id не дублируются):

- шапка приложения (`#app-view .shell-actions`);
- шапка первого запуска (`.setup-brand`) — чтобы тема переключалась и до
  подключения диска.

Кнопка: `type="button"`, `aria-label="Тёмная тема"`, `aria-pressed`,
`title`, иконки солнца и луны (SVG, `currentColor`, видимость через
`[data-theme]`). Клик: переключает `data-theme` и `color-scheme`,
сохраняет выбор в `localStorage`, обновляет `meta[name="theme-color"]`,
`aria-pressed` у обеих кнопок. Сохранённый выбор переживает перезагрузку и
перекрывает системную настройку (проверено под противоположной
`prefers-color-scheme`).

### 1.3. Полные наборы токенов

В `style.css` блок «Cloud Ferry — рабочая область» переписан: вместо
`light-dark()` — явные наборы:

- `:root, :root[data-theme="light"]` — `color-scheme: light` + светлые значения;
- `:root[data-theme="dark"]` — `color-scheme: dark` + тёмные значения.

Переопределены все токены дизайн-слоя: `--page`, `--surface`,
`--surface-raised`, `--ink`, `--ink-soft`, `--muted`, `--accent`,
`--accent-hover`, `--accent-active`, `--accent-soft`, `--hairline`, `--line`,
`--border-strong`, `--border-control`, `--border-hover`, `--surface-sunken`,
`--focus`, `--focus-ring`, `--ok`, `--warn`, `--err`, `--amber`.
Добавлены производные токены, чтобы убрать последние `light-dark()`
в свойствах: `--on-accent`, `--skeleton`, `--switch-off`,
`--check-off-border`, `--check-off-bg`, `--check-disabled`.
`grep "light-dark("` по `style.css` пуст (упоминание осталось только
в комментарии).

Контраст ключевых пар (WCAG 2.x, посчитан по значениям токенов и проверен
по computed styles в браузере):

| Пара | Светлая | Тёмная | Порог |
|---|---|---|---|
| `--ink` на `--surface` | 15,55:1 | 14,09:1 | ≥4,5 |
| `--ink-soft` на `--surface` | 9,20:1 | 10,95:1 | ≥4,5 |
| `--muted` на `--surface` | 6,20:1 | 7,72:1 | ≥4,5 |
| `--accent` на `--surface` | 6,86:1 | 6,58:1 | ≥4,5 |
| `--accent` на `--accent-soft` | 5,66:1 | 5,21:1 | ≥4,5 |
| `--on-accent` на `--accent` | 6,86:1 | 7,09:1 | ≥4,5 |
| `--ok` / `--warn` / `--err` / `--amber` на `--surface` | 6,02–6,87:1 | 8,05–10,31:1 | ≥4,5 |
| `--border-control` на `--surface` | 3,51:1 | 3,70:1 | ≥3 |
| `--border-control` на `--surface-sunken` | 3,10:1 | 3,31:1 | ≥3 |

Границы контролов усилены: светлая `--border-control` `#94909b` → `#8b8792`
(было 2,76:1 на `--surface-sunken`), тёмная `#777080` → `#7d7787`
(было ровно 3,00:1). Декоративные hairline-разделители (`--hairline`)
не менялись: WCAG 1.4.11 их не требует.

### 1.4. Исправленные контрастные дефекты (найдены проверкой обеих тем)

| Место | Было | Стало |
|---|---|---|
| `.status-pill--running` | текст `#0a5cbf` — в тёмной теме нечитаем на `--accent-soft` | `color: var(--accent)` (5,66:1 / 5,21:1) |
| `.account-probe-result--busy` | тот же `#0a5cbf` | `color: var(--accent)` |
| `::placeholder` | `#6b7280` — в тёмной теме 3,28:1 | `color: var(--muted)` |
| `input:focus` | фон `#fff` — в тёмной теме светлый текст на белом | `background: var(--surface-raised)` |
| галочки чекбоксов | `#fff` на `--accent` — в тёмной теме 2,2:1 | `var(--on-accent)` |
| `.btn--primary` спиннер/текст | `--spin: #fff` в первой CSS-слое | `--spin: var(--on-accent)` |
| `.logo` | `light-dark(white, #22162e)` | `var(--on-accent)` |

В браузере проверены computed-цвета всех видимых текстов и границ на
экранах: главный, «Настройки», «Что скачать», «Сохранить на Umbrel»,
«Подключение» — в обеих темах и на 390/1440. Текстовых нарушений <4,5:1
нет; значимых границ <3:1 нет. Единственное исключение — декоративная
hairline-рамка `.account-chip` (1,25:1 светлая / 1,52:1 тёмная): кнопка
опознаётся по контрастной иконке, рамка не является обязательной границей
по WCAG 1.4.11. Исключение зафиксировано в evidence.

## 2. Что удалено (по списку task-7)

Перед удалением каждый пункт проверен `grep` на обработчики и вызовы;
после — повторный `grep` по всем трём файлам не находит ни одного
идентификатора (список проверок — в разделе 4).

**Недостижимые шаги мастера «Папка на сервере» и «Готово»**
(+ всё, что стало мертвым вместе с ними):

- `STEP_TITLES`, ветки `setupStepBody()` для шагов 1–2 и разметка
  `done-hero`/`done-icon`;
- `wizard.step`, `wizard.dest`, `wizard.announcedStep`; ветки шагов
  в `renderSetup()`, `onSetupNext()`, `onSetupBack()`, `runConnect()`;
- поле `#f-dest`, кнопка `data-setup-browse`, `data-browse`-обработчик
  на главном, `saveSetupFields()`-чтение `#f-dest`;
- `chooseSetupFolder()` вместе с вызовом несуществующей `renderStorage()`
  (был бы `ReferenceError`);
- `applyPreferredDest()` и `preferredDest` (папку мастера больше никто не
  выбирает; назначение берётся из `flowDest`/`state.downloads`);
- `openFolderDialog()` и контекст `setup` сессии диалога (пикер мастера);
  `FLOW_STEPS.setup`, ветки `setup` в `renderFlowFooter()`,
  `flowChooseAction()`, `flowBackAction()`, `finishFolderDialog()`;
- DOM-прогресс шагов `#setup-steps` и скрытый `#setup-step-text`;
- CSS: `.setup-steps`, `.done-hero`, `.done-icon`, `.setup-title--done`,
  `.btn--browse`, `.field-row`.

**Невидимый счётчик «N из 3» в диалоге скачивания:**

- `#flow-step-pos` из разметки, `position`-код из `updateFlowChrome()`,
  CSS `.flow-step-pos`.

**Мёртвые DOM-узлы и обработчики:**

- `#account-logout`, `#account-reconnect` (обработчики; элементов в HTML нет);
- `[data-browse]` (обработчик на `#home-view`);
- `#account-cell-method`, `#account-cell-status`, `#account-cell-usage`,
  `#account-cell-note` в `renderAccountCell()` (элементов нет; осталась
  только запись логина) + мёртвые классы `.account-cell-*` в CSS.

**Мёртвые ветки под поля, которых backend не отдаёт:**

- `data.retryAfterHint` в `validationFailure()`;
- `data.partial` / `data.unknownFiles` в `estimateView()`, ветка «часть
  файлов не удалось оценить» в `estimateValueMarkup()` и `flowAnnounce()`.

Итог: `app.js` −251/+80 строк (5693 → 5523), `style.css` −127/+88
(3544 → 3506), `index.html` −4/+23 (269 → 288, добавлены скрипт темы и две
кнопки). Go-файлы не менялись.

## 3. clientToken и повтор запуска

Ничего в запуске не менялось; проверено, что механика цела.

**Клиент.** `createJob()` отправляет `clientToken` из `launchToken()`, ключ
переживает потерю ответа; повтор с тем же выбором переиспользует ключ.
Браузерная проверка на моке: первый `POST /api/jobs` искусственно оборван
(сеть), повторный запрос ушёл с тем же `clientToken`; в диалоге —
«Скачивание началось». Тело: `clientToken` одинаковый, `sourcePaths`
канонические (`docs/design-v3/evidence/theme-cleanup-client-token.json`).

**Сервер.** Реальный `./cmd/cloudferry` + фейковый `rclone rc`
(`agent-work/design-integration/fake_rclone.py`, `setup_env.py`):

```curl
POST /api/jobs  same clientToken  → 201, job 20261118T170856-5f6108e9fc3a8372
POST /api/jobs  same clientToken  → 200, Idempotent-Replay: true, тот же id
GET  /api/state                   → задач на одну больше (4 против 3 засеянных)
```

Серверная часть также покрыта `go test ./...` (`internal/httpapi`,
идемпотентность: 409 на чужой ключ, replay с `Idempotent-Replay`).

## 4. Проверки

| Проверка | Результат |
|---|---|
| `node --check internal/httpapi/web/app.js` | OK |
| `go build ./...` | OK |
| `go test ./...` | все пакеты `ok` (httpapi 4,8 s; jobs, store, rc, fsbrowse, health, yandex, disk, humanize, cmd — cache/ok) |
| `go vet ./...` | OK |
| Chromium, 390×844 и 1440×900, обе темы (`node agent-work/theme-cleanup/ui-check-v3.mjs`) | 223 проверки, 0 провалов |
| Консоль приложения и сеть | 0 console error, 0 исключений, 0 неожиданных ответов ≥400 (на всех экранах и обеих темах) |
| `scrollWidth == innerWidth` | 390/390, 1440/1440 на главном, в диалоге и на «Подключении»; обе темы |
| Тема без вспышки | `data-theme` и фон `body` уже верные на `DOMContentLoaded`; сохранённый выбор перекрывает противоположную системную настройку после reload |
| Контраст | текст ≥4,5:1 и значимые границы ≥3:1 — нарушений нет; декоративная рамка `.account-chip` — исключение WCAG 1.4.11 |
| Мёртвый код | `grep` по `wizard.step`, `STEP_TITLES`, `chooseSetupFolder`, `renderStorage`, `#f-dest`, `data-setup-browse`, `#flow-step-pos`, `#account-logout/-reconnect`, `#account-cell-*`, `partial`, `unknownFiles`, `retryAfterHint` — 0 совпадений в `app.js`, `index.html`, `style.css` |
| `clientToken` / идемпотентность | см. раздел 3, обе проверки прошли |

Скрипты и до/после: `cloud-ferry/agent-work/theme-cleanup/`
(`ui-check-v3.mjs`, `contrast.py`, `apply_*.py`, `before/`).
Машиночитаемые отчёты: `docs/design-v3/evidence/theme-cleanup-report.json`,
`theme-cleanup-{light,dark}-{390,1440}-audit.json`,
`theme-cleanup-client-token.json`. Скриншоты (22 шт., обе темы и ширины,
главный/диалог/проверка/настройки/подключение): `docs/design-v3/screens/theme-v3-*.png`.

## 5. Что осталось (осознанно не трогалось)

1. **Остатки `excludedFolders` / `folder.excluded`** — по `INTEGRATION.md`
   §4.1 вынесены в отдельную задачу; они не входят в список task-7.
2. **Шаги флоу как числа.** `FLOW_STEPS[*].step` по-прежнему управляет
   видимостью секций и `data-step` диалога; `#flow-step-head` и
   `#flow-step-title` остались в разметке (на них ссылаются
   `aria-labelledby`), но `updateFlowChrome()` держит узел `hidden` —
   заголовок шага дублируется `#flow-title`. Это не входило в список.
3. **Дубли CSS из старых слоёв** (`.folder-check-mark`, `.dashboard`,
   `.cell--account`, `.account-danger`, `.boot*`, `.status-pill--unknown`,
   `.btn--start` и т. п.) — вне списка task-7.
4. **Живое следование системной теме.** Если пользователь не выбирал тему,
   при загрузке берётся системная; переключение системной настройки на
   открытой странице не отслеживается (listener `prefers-color-scheme` не
   добавлен). При необходимости — отдельная небольшая правка.
5. **Boot-скелет** не содержит переключателя (экран живёт доли секунды,
   реальные шапки — приложение и «Подключение»).
6. Живой Яндекс Диск не проверялся (учётных данных нет) — как и в
   `INTEGRATION.md`; тема от провайдера не зависит.
