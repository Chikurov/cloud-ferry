# Cloud Ferry — интеграция дизайна v3 с backend

Дата: 18 ноября 2026.
Источник дизайна: локальная копия UI (`internal/httpapi/web/{index.html,style.css,app.js}`).
Предыдущие файлы сохранены без изменений в `agent-work/legacy-web/` (эталон).
Новый интерфейс развёрнут в `internal/httpapi/web/` (три файла заменены). Go-код не менялся.

## 1. Что сделано

1. Три файла дизайна скопированы в `internal/httpapi/web/` (index.html, style.css, app.js).
2. Пройден весь обмен браузера с backend: 20 используемых ручек сверены с Go-обработчиками
   (`internal/httpapi/*.go`), тела запросов — с `DisallowUnknownFields`, ответы — с JSON-полями.
3. Найденные расхождения исправлены только в web-файлах (список в разделе 2).
4. Интерфейс проверен на реальном сервере `./cmd/cloudferry` с фейковым `rclone rc`
   (unix-socket) и на моке `agent-work/preview/preview.py` в Chromium на 390 и 1440 px.
5. Составлен список неиспользуемых мест (раздел 4) — решения по ним за лидом.

## 2. Расхождения «экран → вызов → расхождение → исправление»

| Экран | Вызов API | Расхождение с нашим backend | Исправление |
|---|---|---|---|
| «Новое скачивание», шаг 2→3 (оценка объёма) | `POST /api/preflight` | Дизайн отправлял поле `excludedFolders`; backend отклоняет неизвестные поля JSON (`decodeJSON` → `DisallowUnknownFields`), ответ 400 «Не удалось прочитать запрос». Оценка на шаге 3 никогда не запускалась, если пользователь исключал папку из «Весь диск» | Поле убрано из тела запроса (`ensurePreflight`); список исключений отключён (см. ниже) |
| «Новое скачивание», шаг 3 → «Скачать» | `POST /api/jobs` | То же поле `excludedFolders` → 400, задача не создавалась | Поле убрано из тела (`createJob`) |
| «Новое скачивание», шаг 2 «Что скачать» | `POST /api/cloud/sizes`, `POST /api/jobs/existing`, `POST /api/preflight`, `POST /api/jobs` | В дизайне было «Весь диск, кроме N папок» (снятие галки у папки под «Весь диск»). Backend не принимает исключения ни в одном из этих запросов — реализовать без правок Go нельзя | Под «Весь диск» папки показаны отмеченными и заблокированы (`toggleFolderSelection`, `cloudNodeMarkup`), отметка «Весь диск» больше не даёт неполный выбор; обещание исключений убрано |
| Главный экран, карточка задачи | `GET /api/state` | Заголовок карточки читал `job.excludedFolders` — такого поля в `store.Job` нет | Мёртвая ветка удалена (`renderJobList`) |
| Папки на сервере: создание папки, проверка «что уже скачано» | `POST /api/fs/dirs`, `GET /api/fs/folders`, `POST /api/jobs/existing` | 403 от backend («в эту папку сохранять нельзя», «папка ведёт за пределы доступной») дизайн показывал как «Нужно войти заново»: потеряна ветка старого интерфейса `localFolderRequest` и поле `error.path` | Возвращены `error.path` в `api()` и отдельная обработка 403 для `/api/fs/*` и `/api/jobs/existing` в `friendlyError()` |
| Все экраны, тёмная тема | — | Панель файлов имела жёсткий светлый фон `#f7f8fa`, а скелетоны, переключатель и заблокированные чекбоксы — жёсткие светлые цвета; на тёмной теме панель становилась белой со светлым текстом (нечитаемо) | Жёсткие цвета заменены на `light-dark()`/токены (`--surface-sunken`); контраст текста панели проверен в обеих темах |
| Все экраны (ошибки) | любые 4xx/5xx | Ветки `data.retryAfterHint` (validate) и `data.partial`/`unknownFiles` (preflight) backend никогда не присылает | Оставлены как безвредные защитные ветки; в отчёте помечены как неиспользуемые (раздел 4) |

Контракты, которые совпали без правок (проверены запросами и ответами):

| Экран | Вызов | Что совпало |
|---|---|---|
| Загрузка приложения, главный экран | `GET /api/state` | `accounts[]` (`id`, `name`, `provider`, `login`, `status`, `checkedAt`), `jobs[]` (`status`, `sourcePaths`, `sourceFiles`, `dest`, `stats.*`, `error*`, `failures`, `failureCount`), `downloads` |
| Главный экран, место на Диске/сервере | `GET /api/system` | `roots[]` (`path`, `free`, `total`), `cloud.status/accountId/total/used/stale/updatedAt` (числа приходят `null`, а не нулём) |
| Панель «Настройки» | `POST /api/accounts/{id}/probe` | `about{total,used,free}`, `warning`, `knownUser`, `login`; ошибка помечает `account.status = needs-auth` |
| Подключение | `POST /api/accounts/validate`, `POST /api/accounts`, `GET/DELETE /api/oauth/yandex/device/{id}`, `POST /api/oauth/yandex/device/start`, `GET /api/oauth/yandex/status` | тела запросов проходят `DisallowUnknownFields`; `available`, `flowId/userCode/verificationUrl/expiresIn`, `status: pending/done/error`, `account` |
| Диалог скачивания, шаг 1 | `GET /api/fs/folders`, `POST /api/fs/dirs` | `root,path,parent,dirs,entries,roots,free,total,truncated,writable`; создание → 201 `{path}` |
| Шаг 2 «Что скачать» | `GET /api/accounts/{id}/folders`, `GET /api/accounts/{id}/files`, `POST /api/cloud/sizes`, `POST /api/jobs/existing` | `dirs/entries`, `files[{name,size,modified}]`, `truncated/count`, `entries[{path,bytes,files,error}]`, `items[{path,exists,entries}]` |
| Шаг 3 «Проверьте выбор» | `POST /api/preflight` | `files,bytes,free,fits` (порог 90 % как у backend); ошибки `{error,field}` |
| Запуск и управление задачами | `POST /api/jobs`, `POST /api/jobs/{id}/stop`, `POST /api/jobs/{id}/resume`, `DELETE /api/accounts/{id}` | 201 + тело задачи, 200 + `Idempotent-Replay` для повтора с тем же `clientToken`; `{stopped,warning}` у stop; 409 на конфликт пути и идемпотентности |

## 3. Проверки

| Проверка | Результат |
|---|---|
| `node --check internal/httpapi/web/app.js` | OK |
| `go build ./...` | OK |
| `go test ./...` | все пакеты `ok` (httpapi, jobs, store, rc, fsbrowse, health, yandex, disk, humanize, cmd) |
| `go vet ./...` | OK |
| Реальный сервер: `./cmd/cloudferry` + фейковый `rclone rc` на unix-socket, засеянные аккаунт и задачи (running/failed/done) | сценарии пройдены в Chromium 156 headless, консоль без ошибок приложения и без исключений, ни одного неожиданного ответа ≥ 400 |
| 1440 px | главный экран (3 карточки, статусы, прогресс, список неудачных файлов), панель «Настройки» + «Проверить», смена «Приостановить → Продолжить», «Убрать из списка», полный сценарий «Новое скачивание»: создание папки, вложенные папки, размеры, «уже на сервере», предпросмотр файлов, preflight, запуск (201), новая карточка |
| 390 px | главный экран и диалог скачивания: `document.documentElement.scrollWidth == window.innerWidth` (390/390), горизонтальной прокрутки нет, консоль чистая |
| Обе темы | светлая и тёмная: `scrollWidth == innerWidth`, контраст заголовка и имён файлов в панели файлов ≥ 3:1 (фактически 9,8:1 в тёмной и 8,1:1 в светлой) |
| Мок `agent-work/preview/preview.py` (обычный) | интерфейс показывает ровно данные мока: «12,3 из 29,0 ГБ», 42 %, «8,4 МБ/с», «5 ч 56 мин», «128,5 из 200 ГБ»; в разметке нет зашитых демо-значений |
| Мок `MOCK_EMPTY=1` | пустой экран «Подключить диск»: ни чисел, ни единиц измерения, ни процентов — выдуманных данных нет |

Всего 149 автоматических браузерных проверок (24 + 62 + 28 + 13 + 22) — все прошли.
Машиночитаемые отчёты: `docs/design-v3/evidence/report-*.json`, скриншоты: `docs/design-v3/screens/`.
Сценарии проверки и фейковый `rclone rc` лежат вне коммита: `agent-work/design-integration/`
(`fake_rclone.py`, `setup_env.py`, `ui-check.mjs`, `ui-scenario2.mjs`, `theme-check.mjs`, `mock-check.mjs`, `cdp.mjs`).

## 4. Что не используется (решение за лидом)

### 4.1. Мёртвые экраны и шаги

| Место | Что не так |
|---|---|
| Мастер подключения, шаги 2 «Папка на сервере» и 3 «Готово» (`STEP_TITLES`, `renderSetup`, `#f-dest`, `data-setup-browse`, `chooseSetupFolder`, `applyPreferredDest`) | Недостижимы: `wizard.step` нигде не становится `1` — после успешного подключения `finishConnect()` сразу уводит на `#/`. Пункты «Папка на сервере»/«Готово» и выбор папки в мастере — мёртвый код. Вдобавок `chooseSetupFolder()` вызывает несуществующую `renderStorage()` — если шаг когда-нибудь включат, будет `ReferenceError` |
| Индикатор шага в диалоге скачивания (`#flow-step-pos`, «2 из 3») | `updateFlowChrome()` скрывает родительский `#flow-step-head`, внутри которого лежит счётчик, — номер шага не показывается никогда |
| `renderAccountCell()`: `#account-cell-method/-status/-usage/-note` | Этих элементов нет в разметке (`homeMarkup()` рисует только `#account-cell-login`), обновление молча ничего не делает |
| `#account-logout` и `#account-reconnect` | Элементов нет в index.html; обработчики — мёртвый код (выход реально доступен из меню «•••» на карточке диска) |
| Обработчик `[data-browse]` на главном экране | В разметке главного экрана нет кнопки с `data-browse` |
| `job.excludedFolders` в карточке задачи | Поля нет в `store.Job`; ветка удалена при интеграции |
| `data.retryAfterHint` (validate), `data.partial`/`unknownFiles` (preflight) | Backend не присылает эти поля; ветки безвредны, но не работают |
| Остатки `excludedFolders`/`folder.excluded` в коде | После отключения исключений массивы всегда пусты; код можно вычистить отдельной задачей |

### 4.2. Ручки backend, которые новый интерфейс не использует

| Ручка | Комментарий |
|---|---|
| `GET /api/widget` | Виджет Umbrel; интерфейс его не запрашивает (это внешний потребитель) |
| `POST /api/jobs/{id}/check` | «Проверка целостности» в новом дизайне не выведена; поле `job.check` тоже не отображается |
| `POST /api/oauth/yandex/start`, `POST /api/oauth/yandex/finish` | Старый OAuth-поток; дизайн использует device flow (`/api/oauth/yandex/device/*`) |
| `GET /api/fs/dirs` | Алиас `GET /api/fs/folders`; дизайн использует `/api/fs/folders` |
| `POST /api/jobs/preflight` | Алиас `POST /api/preflight`; дизайн использует `/api/preflight` |
| `GET /api/healthz`, `GET /api/readyz`, `GET /api/accounts/validate` (guard) | Служебные/мета-ручки, интерфейсу не нужны |

### 4.3. Что осталось

1. **Живой Яндекс Диск не проверялся** — реальных учётных данных нет. Проверка входа (validate), панель «Настройки» и «Проверить», device flow проверены на фейковом `rclone rc`; device flow в браузере не открывался, потому что у сервера не заданы `CF_YANDEX_CLIENT_ID/SECRET` (`/api/oauth/yandex/status` → `available:false`, способ входа скрыт).
2. **Шаги мастера 2–3** — нужно решение: либо вернуть переход на «Папку на сервере» после подключения (доработать `finishConnect`, добавить `renderStorage`), либо удалить мёртвые шаги. Сейчас UI честно ведёт себя как мастер из одного шага.
3. **Исключения при выборе «Весь диск»** — UI-возможность отключена, потому что backend не принимает список исключений. Если функцию сохранять, нужны правки Go (поле `excludedFolders` в preflight/jobs и фильтр rclone).
4. **Частичная оценка объёма** (`partial`, `unknownFiles`) — интерфейс к ней готов, backend пока не отдаёт; нужны правки Go или удаление ветки.
5. Панель файлов/скелетоны/переключатели в тёмной теме исправлены минимально; если дизайнер пришлёт обновление палитры, эти правки стоит сверить.
6. Коммит не делался: изменены только `internal/httpapi/web/{app.js,index.html,style.css}`, добавлен `docs/design-v3/`. Go-код не изменён.
