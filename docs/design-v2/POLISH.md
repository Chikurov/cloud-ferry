# Cloud Ferry — полировка по дизайн-скиллам

Дата: 16.11.2026. Владелец задачи: `design-polish` (shared task `task-5`).
Область: `internal/httpapi/web/{app.js,index.html,style.css}` и `docs/design-v2/`.
Ничего не устанавливалось: источники прочитаны как публичные тексты через `web_fetch`.
Бэкенд, разметка и JS-поведение не менялись — только CSS (`style.css`, +150/−28 строк).

## 1. Что прочитано

| Источник | Что взято |
|---|---|
| [make-interfaces-feel-better / SKILL.md](https://raw.githubusercontent.com/jakubkrehel/make-interfaces-feel-better/main/skills/make-interfaces-feel-better/SKILL.md) и подстраницы [typography](https://raw.githubusercontent.com/jakubkrehel/make-interfaces-feel-better/main/skills/make-interfaces-feel-better/typography.md), [surfaces](https://raw.githubusercontent.com/jakubkrehel/make-interfaces-feel-better/main/skills/make-interfaces-feel-better/surfaces.md), [animations](https://raw.githubusercontent.com/jakubkrehel/make-interfaces-feel-better/main/skills/make-interfaces-feel-better/animations.md), [icons](https://raw.githubusercontent.com/jakubkrehel/make-interfaces-feel-better/main/skills/make-interfaces-feel-better/icons.md), [performance](https://raw.githubusercontent.com/jakubkrehel/make-interfaces-feel-better/main/skills/make-interfaces-feel-better/performance.md) | Основной список: концентрические радиусы, тени вместо рамок, сглаживание шрифта, tabular-nums, balance/pretty, `scale(0.96)`, hit-area 44/40, currentColor и fill для активного, умеренность движения, явные transition-property |
| [vercel-labs/agent-skills README](https://raw.githubusercontent.com/vercel-labs/agent-skills/main/README.md) | Категории `web-design-guidelines`: доступность, focus-visible, формы, `prefers-reduced-motion`, compositor-friendly transforms, tabular-nums, dark mode/theming, touch-action, Intl — чек-лист аудита. Сам SKILL.md не открывался (в README только перечень правил) |
| [Leonxlnx/taste-skill README](https://raw.githubusercontent.com/Leonxlnx/taste-skill/main/README.md) | Одна дизайн-система, спокойные микровзаимодействия, проверка в реальном браузере. Остальное — про лендинги компаний (фотографии, shader-сцены, hero-анимации) — к продуктовому экрану не применимо |
| [emilkowalski/skills README](https://raw.githubusercontent.com/emilkowalski/skills/main/README.md) | Подтверждает: ease-out для входов, тени вместо сплошных рамок, «лишнее движение убирать, а не добавлять»; сами SKILL.md не открывались |

Проектный контекст: `docs/design-brief-v2.md`, `docs/ui-standards.md`, `docs/design-v2/REPORT.md` (приёмка V2), токены и структура `style.css`.

## 2. Что уже было в порядке (проверено, не менялось)

- `transition: all` — 0 вхождений; все переходы уже называют свойства (`background-color`, `border-color`, `color`, `transform`, `width`).
- Иконки — один набор: сетка 24, `stroke-width 1.6`, `currentColor`, `fill: none`, `round` caps, `aria-hidden/focusable=false`; 16 иконок на главном, 25 в сценарии скачивания. Разных библиотек и вложенных цветов нет.
- Высота контролов 40 px (44 на тач) и токены радиусов уже соответствуют шкале; темы не трогались.
- Есть `:focus-visible`, `aria-disabled` вместо `disabled` у busy-кнопок, отдельный блок `prefers-reduced-motion`, `aria-live`-статусы.
- `app.js` и `index.html` правок не потребовали: это CSS-полировка, поведение и разметка сохранены.

## 3. Применённые правки

Таблица: принцип → место → сейчас (до) → стало (после) → почему.

| # | Приоритет | Принцип | Место (`style.css`) | Сейчас | Стало | Почему |
|---|---|---|---|---|---|---|
| 1 | MEDIUM | Концентрические радиусы | `.method-switch` (10 px, padding 4) → `.method-option` | внутренний радиус 8 px | `--r-inner: 6px` (10 − 4) | У вложенных сегментов был почти такой же радиус, как у контейнера — углы «зажимались»; теперь внешний = внутренний + отступ |
| 2 | MEDIUM | Концентрические радиусы | `.menu` (10 px, padding 8) → `.menu-item` (8 px) | padding 8, внутренний радиус 8 | padding 4, внутренний радиус 6 | Та же формула 10 = 6 + 4; пункт меню 40 px не изменился, попап стал на 8 px ниже и углы концентричны |
| 3 | MEDIUM | Тени вместо декоративных рамок | `.cell`, `.setup-card`, `.shell-head`, `.modal`, `.toast`, `.menu` | `border: 1px solid var(--border)` + мягкая тень | `border: 1px solid transparent` + `--ring: inset 0 0 0 1px var(--border)` в `box-shadow` вместе с прежней тенью | Рамка существует только для глубины; тень-кольцо даёт ту же линию 1 px, не участвует в раскладке и переживёт смену фона/темы. Толщина и положение линии визуально не изменились |
| 4 | — | Рамки остались структурными и у состояний | поля ввода, `.btn--secondary`, разделители (`--line`), `.offline-bar` (warn), ошибки, выбранный сегмент, selected-строки | — | без изменений | Границы полей и кнопок несут функцию и контраст (WCAG 1.4.11, 3:1), разделители — структуру, цветные рамки — состояние. По правилу скилла их не заменяют тенью |
| 5 | LOW | Сглаживание шрифта на корне (macOS) | `html` / `body` | `-webkit-font-smoothing: antialiased` только на `body` | перенесено на `html`, добавлено `-moz-osx-font-smoothing: grayscale` | По скиллу настройка ставится один раз на корне; так одинаково рендерятся и обычный текст, и текст в диалогах, и заголовки |
| 6 | MEDIUM | Tabular-nums для обновляющихся чисел | `.review-value` (файлы · объём в «Проверьте выбор»), `.account-fact dd` (занятая ёмкость) | пропорциональные цифры | `font-variant-numeric: tabular-nums` | Числа пересчитываются при смене выбора/объёма — без tabular-nums цифры прыгают по ширине и строка «дышит». Всего в файле теперь 19 таких объявлений |
| 7 | LOW | `text-wrap: balance` для заголовков | `h1, h2, h3` (`.cell-title`, `.modal-title`, `.setup-title`, `.flow-step-title`) | обычный перенос | `text-wrap: balance` | Короткие заголовки не оставляют одно слово на второй строке. У `.cell-title` остаётся `nowrap` + ellipsis — это осознанное усечение по продукту, balance там не применяется |
| 8 | LOW | `text-wrap: pretty` для текста | `p, li, dd, figcaption` + заметки-обёртки `.now-note, .now-error, .review-sub, .folder-detail, .files-note, .account-danger-note, .toast-message` | обычный перенос | `text-wrap: pretty` | Последняя строка абзаца не остаётся с одним словом; на длинный текст не влияет |
| 9 | MEDIUM | `scale(0.96)` на нажатие, прерываемо | `.btn`, `.account-chip`, `.connect-row`, `.method-option`, `.menu-item` | отклик только цветом | `:active { scale: .96 }` для включённых; `scale` добавлен в `transition-property` (150 мс, ease-out) | Тактильный отклик кнопки. Это CSS-transition, а не keyframes: отпускание посреди нажатия плавно возвращает кнопку. Для `disabled`/`aria-disabled`/`aria-busy` отклик отключён |
| 10 | MEDIUM | Hit-area 44/40 псевдоэлементом | 28 px: `.dest-summary-edit`, `.review-change`, `.review-retry`, `.token-help-toggle`; 32 px: `.files-hide`, `.flow-step-action`, `.toast-close`; 36 px: `.btn--sm`, `.folder-new`, `.source-menu-btn`, `.field-action`; 32 px `.file-select-all` | визуально 28–36 px | `::after` с абсолютным `inset`, площадь 40×40 на десктопе | Мелкие текстовые/иконочные действия получают нормальную площадь нажатия без изменения макета. У кнопок с рамкой 1 px поправка на padding-box; пересечений соседних целей нет (проверено in-browser) |
| 11 | LOW | Тач-цели 44 px | `@media (pointer: coarse)` | 44 px у части контролов | добавлены `.token-help-toggle`, `.file-select-all`; псевдоэлементы там обнулены | На тач-устройствах высота уже 44: растянутые псевдоэлементы дали бы наложение областей соседних целей |
| 12 | LOW | Один SVG: outline по умолчанию, fill у активного | иконка папки в дереве `.folder-node-row.is-selected .folder-icon .icon` | outline, цвет `--text-2` | `fill: var(--accent)` + `color: var(--accent)` только у выбранной папки | Активное состояние помечается заливкой той же иконки, без второго SVG и без новых ассетов; остальные иконки остаются outline |
| 13 | LOW | Умеренность движения, ≤150 мс | токен `--duration`, `.folder-check-mark`, `.folder-expand .icon`, `.toast` | 160 мс у части состояний, toast 200 мс | `--duration: .15s`; 160 мс → `var(--duration)`; toast 150 мс | Высокочастотные состояния (hover/press/чекбокс/раскрытие) укладываются в 150 мс; вход тоста короче и не отвлекает |
| 14 | — | Только явные transition-property | все интерактивные состояния | — | проверено, `transition: all` нет; новые переходы перечисляют свойства (`scale`, `background-color`) | Браузер не следит за всеми свойствами, нет неожиданных анимаций цвета/отступов |

HIGH-дефектов аудит не нашёл: все найденные пункты — MEDIUM (заметная неаккуратность/риск) и LOW (полировка).

Итого: 14 смысловых групп правок, все — в `internal/httpapi/web/style.css`. `app.js` и `index.html` не менялись, поэтому поведение и разметка идентичны.

## 4. Отклонённые правки (и почему)

| Кандидат | Почему отклонено |
|---|---|
| Строгие концентрические радиусы у `.toast-close` (внешний 16, padding 12 → внутренний 4), у кнопок в `.shell-head` (16 − 12 = 4), у `.logo` | Это отдельные плитки-иконки с зазором до скруглённого угла, а не вложенные поверхности: при радиусе 4 px 32–40 px контролы выглядят обрезанными. Скилл сам разрешает считать слои раздельными, когда отступ сопоставим с радиусом. Визуального «зажима» здесь нет |
| Радиус строк файлов внутри `.files-panel` (внешний 10, padding 12, строки 10) | padding больше радиуса — вложенной пары «угол в угол» не возникает; строки читаются как отдельные поверхности. Пересчитывать нечего, менять — значит ломать текущую сетку панели |
| Убрать рамки у полей и `.btn--secondary` в пользу тени | Рамки полей — структурные и несут контраст границы (WCAG 1.4.11, 3:1); замена тенью ухудшит доступность. Кнопки с рамкой — тоже граница контрола, а не декор |
| Симметричное оптическое выравнивание иконки в кнопках (`padding` иконки = `padding` текста − 2 px) | В vanilla-разметке иконки стоят и отдельными узлами, и рядом с текстовыми узлами, без общего класса — надёжного CSS-хука нет (`:has` + текстовые узлы не покрываются). Выигрыш 2 px не стоит риска регрессии в 20+ кнопках |
| Кросс-фейд иконок луны/солнца в переключателе темы (opacity/scale 0.25→1) | Требует наложения иконок абсолютным позиционированием и согласования с JS, который синхронизирует `hidden`/`aria-label`; переключение темы редкое, статическая подсказка (иконка) уже есть. Риск layout-shift больше пользы |
| Убрать «дыхание» `.progress--live > i` и пульс `.yandex-pulse` | Это индикаторы «идёт процесс/ждём подтверждения», а не реакция на частые действия; оба — только opacity, `prefers-reduced-motion` их уже отключает (проверено). Убрали бы единственную подсказку, что прогресс жив при редких обновлениях |
| `transition: width .7s` у полосы прогресса → ≤150 мс | Прогресс меняется шагами от API; короткий переход даёт дёргание, а плавный — читаемое движение. Это не интерактивное состояние, поэтому правило «≤150 мс для частых действий» к нему не применялось |
| `will-change` для кнопок/прогресса | По скиллу добавляется только при замеченном stutter первого кадра; в прогоне его не было, а каждый слой стоит памяти |
| Staggered-вход экрана (каскад заголовок → описание → кнопки) | Экраны приложения открываются часто (навигация, смена темы, возврат из диалога); скилл прямо запрещает каскады на рутинных переходах. Одноразовые входы (тост, диалог, шаг флоу) уже есть и оставлены |
| `outline` 1 px у изображений (image outline из скилла) | В интерфейсе нет изображений — только иконки и favicon; правило не к чему применять |
| Расширение hit-area у inline-ссылок (`.note a`) и у текстовых `<label>` полей | Ссылки — часть строки текста; `inline-block`/padding изменит перенос строк. Подписи полей не самостоятельные цели — сама цель это поле 40/44 px. Пересечения и так нет |
| Правила taste-skill про «один экран на секцию», фотографии, shader-сцены, hero-моменты | Скилл для лендингов компаний; к продуктовому экрану с двумя карточками неприменим и противоречил бы брифу V2 («не добавлять анимацию ради украшения») |

## 5. Проверки

| Проверка | Команда / способ | Результат |
|---|---|---|
| Синтаксис JS | `node --check app.js` | OK |
| Go | `go build ./... && go test ./... && go vet ./...` | exit 0, все пакеты `ok` |
| Предпросмотр | мок `agent-work/preview/preview.py` на `http://127.0.0.1:8899`, headless Chromium 156 (CDP) | приложение стартует, флоу открывается, ошибок в консоли нет |
| Переполнение | 390×844 и 1440×900, обе темы, 8 состояний (главный, аккаунт, «Что скачать», «Проверьте выбор») | `scrollWidth == innerWidth` везде |
| Консоль | `Runtime.exceptionThrown`, `console.error/warning`, `Log` | 0 ошибок приложения на всех проверенных экранах |
| Площадь нажатия | все видимые `button`/`[role]`/label-цели в открытом слое | ни одной цели < 40×40 на десктопе; в `pointer: coarse` (touch-эмуляция) — ни одной < 44×44 |
| Пересечение hit-area | попарные пересечения расширенных областей | 0 пересечений (десктоп и touch-эмуляция) |
| Нажатие | CDP `mousePressed` → computed `scale` | `0.96` в нажатом состоянии, `1`/`none` после отпускания |
| Движение | computed `transition-property/duration` | `transition: all` — 0; интерактивные переходы 150 мс, прогресс 700 мс (осознанно); `prefers-reduced-motion`: переходы `1e-06s`, анимации `none` |
| Типографика | computed styles | `-webkit-font-smoothing: antialiased` на `html`; заголовки `balance`, текст `pretty`; tabular-nums у всех обновляющихся чисел |
| Радиусы | computed `border-radius` | `.method-switch`: 10 = 6 + 4; `.menu`: 10 = 6 + 4 |
| Иконки | computed `stroke-width`/`fill` у `svg.icon` | ровно один вес `1.6px`, `fill: none` по умолчанию |
| Скриншоты | `docs/design-v2/polish-*.png` | 9 файлов, обе темы, 1440 и 390 |

Скриншоты:

- `polish-home-light-1440.png`, `polish-home-dark-1440.png` — главный экран с активным скачиванием;
- `polish-source-light-1440.png`, `polish-source-dark-1440.png` — «Что скачать»;
- `polish-home-light-390.png`, `polish-home-dark-390.png`, `polish-source-light-390.png`, `polish-source-dark-390.png` — те же экраны на 390;
- `polish-menu-light-1440.png` — меню «Ещё» (концентрические радиусы) и выбранная папка (fill активной иконки).

Ограничения проверки: всё выполнено в headless Chromium 156 (CDP), `pointer: coarse` — touch-эмуляцией, не реальным iOS. Панель файлов проверялась на локальном расширенном моке: в `preview.py` нет эндпоинта `/api/accounts/{id}/files` (на нём ожидаемо 404), остальные экраны — на штатном моке 8899. Реальный Safari/Firefox не прогонялись; новые значения (`text-wrap`, CSS `scale`) в них либо поддерживаются, либо деградируют до обычного переноса/отсутствия scale — раскладка не ломается.

## 6. Остаток

- Прогнать на реальном iOS/Safari и Firefox: `text-wrap: pretty/balance`, CSS `scale`, `::after`-hit-area — ожидаемое поведение описано выше, но живой девайс не проверялся.
- Если у `.files-panel` когда-нибудь уменьшится padding (< 10 px), радиус строк стоит пересчитать по формуле внешний = внутренний + padding.
- `.toast-close` и иконки-кнопки в шапке оставлены с радиусом 10 px осознанно (см. отклонённые); если дизайн решит сделать их строго концентричными, это отдельное решение, не полировка.
- Полоса прогресса сохраняет плавный переход 0.7 с; при желании сделать её «мгновенной» это отдельное продуктовое решение (пострадает читаемость обновлений).
- 200 % zoom и полный контрастный прогон этой задачей не перепроверялись — они были в приёмке V2 (`docs/design-v2/REPORT.md`) и CSS-правки их не затрагивают.
