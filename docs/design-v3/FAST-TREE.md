# Cloud Ferry — быстрое открытие «Что скачать» и тема без inline-скрипта

Дата: 19 ноября 2026.
Задача: `task-14` — убрать автозагрузку размеров папок в дереве облака и починить
применение темы при CSP `script-src 'self'`.
Изменены только `internal/httpapi/web/{app.js,index.html,style.css}` и новый
`internal/httpapi/web/theme.js`; отчёт и свидетельства — в `docs/design-v3/`.
Go-код (включая `/api/cloud/sizes` и preflight) не менялся. Коммит не делался.

## 1. Причина медленного открытия

`loadCloudNode` после загрузки каждого уровня вызывал `requestCloudSizes()`, а
`POST /api/cloud/sizes` на backend рекурсивно обходит каждую папку через rclone
(`operations/size`). При открытии «Что скачать», раскрытии уровней и повторном
открытии это десятки тяжёлых обходов, поэтому список висел.

## 2. Что изменено

### 2.1. Автозагрузка размеров убрана

- `loadCloudNode` больше не вызывает `requestCloudSizes(...)`: после уровня
  остаётся только `requestExisting(...)` (проверка «Есть на сервере»).
- Размер больше не показывается в строке папки: удалены `folderSizeText()`,
  метаданные `…` и `N файлов · X ГБ`. Строка показывает галочку, иконку, имя и
  теги «Есть на сервере» / «Пустая папка на сервере» / «Входит в выбранную папку».
- Удалён мёртвый код: `requestCloudSizes()`, `folderSizeText()`,
  `cloudSizesUnavailable`, `folder.sizes`, `folder.sizePending`,
  `CLOUD_SIZES_BATCH` (в обоих описаниях сессии диалога), CSS `.folder-row-meta`
  в обоих слоях, устаревший комментарий про размер в строке.
- `POST /api/cloud/sizes` и preflight не тронуты: общий объём по-прежнему
  считается на шаге «Перед скачиванием» (один `POST /api/preflight`).
  `API_TIMEOUT.folders` остался — он используется загрузкой папок.

`grep` по `internal/httpapi/web` на `cloud/sizes`, `requestCloudSizes`,
`cloudSizesUnavailable`, `folderSizeText`, `CLOUD_SIZES_BATCH`, `sizePending`,
`folder-row-meta` — пусто.

### 2.2. Тема вынесена в `theme.js`

Реальный Go-сервер отдаёт заголовок:

```
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline';
  img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'
```

Поэтому inline-скрипт темы в `<head>` блокировался браузером; в консоли было
предупреждение безопасности:

> Executing inline script violates the following Content Security Policy directive
> 'script-src 'self''. … The action has been blocked.

Следствие: `data-theme` на `<html>` при загрузке не выставлялся, первый кадр и
дальнейшая работа шли на светлых значениях `:root`; выбранная/системная тёмная
тема применялась только после ручного переключения. В замере на одну загрузку
страницы приходится одно такое сообщение (в задаче упомянуты два — вероятно,
считались две загрузки или два прохода).

Что сделано:

- новый `internal/httpapi/web/theme.js` — тот же IIFE, что был inline:
  `localStorage['cloudferry-theme']` → иначе `prefers-color-scheme`; ставит
  `data-theme` и `color-scheme` на `<html>`, обновляет `meta[name="theme-color"]`;
- в `index.html` inline-скрипт удалён, вместо него
  `<script src="/theme.js"></script>` **до** `<link rel="stylesheet" href="/style.css">`
  (синхронный внешний скрипт, без `defer`/`async`, поэтому тема выставляется до
  разбора стилей и до первого кадра);
- inline-скриптов в документе не осталось (`document.scripts` — только
  `/theme.js` и `/app.js`).

Go-код не менялся: `theme.js` попадает в существующий `//go:embed all:web` и
отдаётся статическим обработчиком как `200 text/javascript; charset=utf-8`.

## 3. Замеры

Методика: реальный `./cmd/cloudferry` на `127.0.0.1:8877` с фейковым `rclone rc`
через unix-socket, засеянный аккаунт и состояние; Chromium headless, чистый
профиль, CDP Network/Log. Сценарий: открыть «Что скачать» → раскрыть три уровня
(«Фотографии» → «Фотографии/2024» → «Документы») → выбрать папку → «Весь диск» →
шаг «Перед скачиванием». Считаются только запросы `/api/*`; фоновый опрос
`/api/state` и `/api/system` в счётчики не входит.

| Показатель | До | После |
|---|---|---|
| Запросы при открытии диалога | 3: `/folders` 1, `/api/cloud/sizes` 1, `/api/jobs/existing` 1 | 2: `/folders` 1, `/api/cloud/sizes` **0**, `/api/jobs/existing` 1 |
| Раскрытие 3 уровней (дополнительно) | `/folders` 3, `/api/cloud/sizes` 2 | `/folders` 3, `/api/cloud/sizes` **0** |
| Выбор папки и «Весь диск» | 0 запросов | 0 запросов |
| Шаг «Перед скачиванием» | 1 `POST /api/preflight` | 1 `POST /api/preflight` (не менялся) |
| Всего `POST /api/cloud/sizes` за сценарий | **3** | **0** |
| `operations/size` в rclone за сценарий | **9** (8 — автозагрузка размеров 4+2+2 + 1 preflight) | **1** (только preflight) |

Строк «N файлов · X ГБ» и `…` в дереве после изменения нет: в раскрытом дереве
8 строк, у всех `folder-row-meta` = 0, текст строки — имя и тег. Один запрос
`/api/accounts/{id}/folders?path=` на каждый загружаемый уровень сохранился.

Свидетельства: `evidence/fast-tree-before.json`, `evidence/fast-tree-after.json`
(полные списки запросов по фазам), `evidence/fast-tree-rclone-{before,after}.log`
(вызовы rclone).

## 4. Проверка темы и CSP на реальном сервере

| Проверка | Результат |
|---|---|
| CSP реального сервера | `script-src 'self'` (без `unsafe-inline`), `evidence/fast-tree-csp-header.txt` |
| `/theme.js` | 200, `text/javascript; charset=utf-8` |
| Предупреждения CSP/security в консоли | **0** (до правки — 1 violation «Executing inline script violates… script-src 'self'») |
| Inline-скрипты в DOM | 0 |
| Порядок в `<head>` | `theme.js` (индекс 5) раньше `style.css` (индекс 7); сетевой старт `theme.js` раньше `style.css` |
| `data-theme` и `color-scheme` к `DOMContentLoaded` | совпадают с ожидаемой темой в 4 комбинациях (светлая/тёмная × 390/1440) |
| Первый отрисованный кадр (screencast, пиксели) | тёмная — `rgb(22, 21, 26)` (`#16151a`), светлая — `rgb(245, 244, 248)` (`#f5f4f8`); кадров чужой темы и белой вспышки нет |
| Системная тема без сохранённого выбора | применяется (`prefers-color-scheme`) |
| Сохранённая тема важнее системной | тёмная из `localStorage` переживает перезагрузку под светлой ОС и ставится до `DOMContentLoaded` |
| Переключатель | одна видимая кнопка, `aria-pressed`/`aria-label`/`meta theme-color` синхронны, выбор сохраняется |

Скриншоты первого кадра: `screens/fast-tree-first-paint-{dark,light}-1440.png`.

## 5. Проверки

| Проверка | Результат |
|---|---|
| `node --check app.js` | OK |
| `node --check theme.js` | OK |
| `go build ./...` | OK |
| `go test ./...` | все пакеты `ok` (httpapi, jobs, store, cmd и остальные) |
| `go vet ./...` | OK |
| Браузерные проверки на реальном сервере | **173 проверки, 0 проблем** (`evidence/fast-tree-report.json`) |

Браузерный прогон: Chromium, 390 и 1440, светлая и тёмная темы. Для каждого
сочетания: главный экран, открытие «Что скачать», раскрытие трёх уровней,
выбор папки, «Весь диск», шаг «Перед скачиванием», `document.documentElement.
scrollWidth == window.innerWidth` (и `body.scrollWidth <= innerWidth`) на всех
экранах, консоль без ошибок/исключений/CSP-предупреждений, ни одного ответа
API ≥ 400. Скриншоты: `screens/fast-tree-{home,dialog,dialog-expanded,review}-
{light,dark}-{390,1440}.png`.

## 6. Границы и остаточные замечания

- Backend `/api/cloud/sizes` не удалён и не изменён: он остаётся доступным, но
  интерфейс его больше не вызывает; preflight, как и раньше, сам считает объём.
- Строки папок на 390 px с длинным тегом («Пустая папка на сервере») по-прежнему
  усекают имя многоточием — это поведение компоновки тега, не связанное с
  размером, и в объём задачи не входило.
- Тестовое окружение (фейковый `rclone rc`, сценарии Chromium) лежит вне
  репозитория: `agent-work/fast-tree/` (cdp.mjs, fake_rclone.py, measure.mjs,
  check.mjs).
- Коммит не делался.
