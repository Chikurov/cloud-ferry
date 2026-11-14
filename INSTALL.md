# Cloud Ferry: сборка, публикация и установка на Umbrel

Пошаговая инструкция от исходников до работающего приложения в community
app store. Публикация в официальный `getumbrel/umbrel-apps` не требуется.

> Состояние на момент подготовки: образ ещё **не опубликован**, digest в
> `cloudferry-migrator/docker-compose.yml` — заглушка из нулей, `OWNER` — тоже
> заглушка. Пока не выполнены шаги 1–3, приложение не установится: Umbrel
> попытается скачать несуществующий `ghcr.io/OWNER/cloud-ferry:0.1.0`.
> Ничего не закоммичено и не опубликовано — все команды ниже выполняет
> владелец репозитория после получения GitHub-логина и отдельного согласования
> на git-операции.

## Требования

| Что | Зачем |
| --- | --- |
| GitHub-аккаунт, публичный репозиторий | community store и GHCR-образ должны быть публично доступны |
| Docker Engine 24+ с `buildx` | multi-arch сборка `linux/amd64` + `linux/arm64` |
| GitHub token со scope `write:packages` (и `read:packages`) | локальный push в GHCR; в CI достаточно `GITHUB_TOKEN` |
| Umbrel с umbrelOS **2.0+**, свободный порт `8741` | `manifestVersion: "2.0"`, `folderAccess`, `storage.dataRoot` |
| SSH-доступ к Umbrel | диагностика контейнеров и сокета |
| Go 1.24+ (только для локальных проверок) | `go build ./...`, `go test ./...` |

## Формат версий: один тег на всё

Единый формат — **без ведущей `v` в образе**:

| Место | Значение |
| --- | --- |
| git-тег | `v0.1.0` |
| тег образа | `ghcr.io/<owner>/cloud-ferry:0.1.0` |
| `umbrel-app.yml` → `version` | `"0.1.0"` |
| `Makefile` → `TAG` | `0.1.0` |
| compose | `cloud-ferry:0.1.0@sha256:<digest>` |

GitHub Actions снимает ведущую `v` (`${GITHUB_REF_NAME#v}`) и падает, если
версия в манифесте или тег в compose не совпадают с тегом. Именно это
рассинхронизировало раньше: compose ждал `0.1.0`, а CI публиковал `v0.1.0`.

Проверка локально:

```sh
make check-version          # version в манифесте == TAG == тег в compose
make -n docker-push         # что именно соберётся
```

## 1. Подставить свой GitHub-логин

`OWNER` встречается в нескольких местах и меняется одной командой:

```sh
make set-owner OWNER=<github-login>
```

Что команда делает:

- `cloudferry-migrator/exports.sh`: `export OWNER="<login>"` — compose
  подставляет его в `image: ghcr.io/${OWNER}/cloud-ferry:...`;
- `cloudferry-migrator/umbrel-app.yml`: `website`, `repo`, `support`, `icon`
  и `submission` — в них `github.com/OWNER/cloud-ferry` заменяется на ваш
  логин;
- `Dockerfile`: OCI-лейблы `image.source` и `image.url`.

Проверьте diff (коммит — только после отдельного согласования):

```sh
git diff -- cloudferry-migrator/exports.sh cloudferry-migrator/umbrel-app.yml
grep -n '^export OWNER=' cloudferry-migrator/exports.sh
grep -n 'github.com/' cloudferry-migrator/umbrel-app.yml
```

Если в вашем umbrelOS `${OWNER}` из `exports.sh` не доходит до подстановки
compose (проверяется на устройстве, см. шаг 6), замените в
`cloudferry-migrator/docker-compose.yml` `${OWNER}` на логин вручную — это
равнозначный вариант, просто менее удобный при смене аккаунта.

## 2. Собрать и опубликовать образ

### Вариант A — GitHub Actions (рекомендуется)

Workflow `.github/workflows/build.yml` собирает `linux/amd64` и
`linux/arm64` и публикует в GHCR при push тега `v*`.

```sh
# git-операции — только с отдельного согласования
git tag v0.1.0
git push origin v0.1.0
```

После сборки:

- в логе job `image` и в Summary будет строка
  `Manifest-list digest: sha256:...`;
- отдельный шаг `docker buildx imagetools inspect` печатает обе платформы
  (`linux/amd64`, `linux/arm64`) и падает, если какой-то нет;
- обязательный шаг вручную: **GitHub → Packages → `cloud-ferry` → Package
  settings → Change visibility → Public**. Пакет, созданный `GITHUB_TOKEN`,
  по умолчанию приватный, а Umbrel тянет образ без авторизации.

Тег-сборка намеренно не требует уже подставленного digest: digest появляется
только в результате этой сборки. Контракт «версия совпадает» проверяется на
теге, а пиннинг digest — отдельный шаг 3.

### Вариант B — локально

```sh
# smoke-сборка для текущей архитектуры
make docker
```
```sh
# если multi-arch builder ещё не создан
docker buildx create --name cloudferry --use --bootstrap
```
```sh
# вход в GHCR; токен не печатать и не коммитить
echo "$GHCR_TOKEN" | docker login ghcr.io -u <github-login> --password-stdin

# сразу две архитектуры и push
make docker-push

# digest манифест-листа (не отдельной архитектуры)
make digest
```

`make docker-push` и `make pin-digest` требуют заданного `OWNER` (не
заглушку) и завершатся ошибкой, пока он не подставлен.

## 3. Подставить digest в compose

```sh
# digest берётся из реестра
make pin-digest

# или из вывода CI
make pin-digest DIGEST=sha256:<digest_из_CI>
```

Команда обязана подставить **индекс-дайджест** (manifest list) — именно его
печатает CI и `make digest`. Дайджест отдельной архитектуры приведёт к тому,
что на второй архитектуре установка упадёт.

Проверка:

```sh
make check-version
# ожидаемо: OK: ... and ... use 0.1.0; rclone is pinned
grep -n 'cloud-ferry:0.1.0@' cloudferry-migrator/docker-compose.yml
docker buildx imagetools inspect rclone/rclone:1.76.0@sha256:7cbeec24a44f206067597c0368be4de90ae19a64d5113c7d1938bd737a81e466
```

`rclone/rclone:1.76.0@sha256:7cbeec2...` уже запинен в обоих compose-файлах:
это официальный multi-arch индекс (`linux/amd64` + `linux/arm64`), дайджест
сверен с Docker Hub.

После пиннинга закоммитьте `docker-compose.yml` (и `exports.sh`/манифест из
шага 1) в ветку по умолчанию — это и есть установочное состояние store.

## 4. Добавить community store в Umbrel

1. Репозиторий должен быть **публичным**, в ветке по умолчанию — корневой
   `umbrel-app-store.yml` и папка `cloudferry-migrator/`.
2. На Umbrel: **App Store → ⋮ → Community App Stores → Add → URL
   `https://github.com/<login>/cloud-ferry`**.
3. Откройте добавленный магазин: там должен появиться **Cloud Ferry 0.1.0**
   с иконкой.
4. `umbrel-app-store.yml` имеет `id: cloudferry`; id приложения
   `cloudferry-migrator` начинается с `cloudferry-` — это требование
   community store.

Что откуда берётся в манифесте:

- `icon` — `icon.png` (512×512, лежит в корне репозитория, отдаётся по
  raw-ссылке; `icon.svg` оставлен источником);
- `releaseNotes` — текст для диалога обновлений;
- `installSize` — ориентировочный размер в байтах; поле есть в схеме
  `AppManifestSchema` umbreld 2.0 («In bytes»), значение надо уточнить после
  первой публикации:
  `docker image inspect --format '{{.Size}}' ghcr.io/<login>/cloud-ferry:0.1.0`;
- `permissions: [STORAGE_DOWNLOADS]` — образ монтирует общую папку Downloads.

## 5. Установить и открыть

1. В карточке приложения — **Install**.
2. В настройках приложения выберите папку назначения (**Downloads** по
   умолчанию; второй слот — необязательная папка, в интерфейсе её путь
   `/destination`).
3. Откройте приложение на `http://umbrel.local:8741` (логин Umbrel).
4. Подключите Яндекс Диск (способы — в README) и запустите тестовое
   скачивание.

## 6. Smoke-тест после установки

Проверять по порядку; `<app-data>` — каталог приложения, обычно
`~/umbrel/app-data/cloudferry-migrator` (для 2.0 с переносом данных путь
может отличаться — смотрите в настройках приложения).

### 6.1 Контейнеры и healthcheck

```sh
sudo docker ps --filter name=cloudferry
# оба контейнера Up (healthy); app_proxy в umbrelOS 2.0 не создаётся отдельно
sudo docker inspect --format '{{.Name}} {{.State.Health.Status}}' $(sudo docker ps -q --filter name=cloudferry)
```

Ожидаемо: `api` — `healthy`, `rclone` — `healthy`. Если `unhealthy`, ниже —
логи и сокет.

### 6.2 Логи

```sh
sudo docker logs --tail=100 $(sudo docker ps -q --filter name=cloudferry-migrator_api_1)
sudo docker logs --tail=100 $(sudo docker ps -q --filter name=cloudferry-migrator_rclone_1)
```

Не должно быть повторяющегося `bind: address already in use` у rclone и
`dial unix /run/rclone/rclone.sock` у api после старта.

### 6.3 Сокет rclone

```sh
ls -l <app-data>/data/run/
sudo docker exec $(sudo docker ps -q --filter name=cloudferry-migrator_rclone_1) \
  rclone rc --unix-socket /run/rclone/rclone.sock \
  --user "$(sudo docker exec $(sudo docker ps -q --filter name=cloudferry-migrator_rclone_1) printenv RCLONE_RC_USER)" \
  --pass "$(sudo docker exec $(sudo docker ps -q --filter name=cloudferry-migrator_rclone_1) printenv RCLONE_RC_PASS)" \
  --no-output core/version
```

Команда должна завершиться кодом 0. Файл сокета существует ровно один
(`rclone.sock`), мусорных `*.sock` нет.

### 6.4 Готовность API

```sh
sudo docker exec $(sudo docker ps -q --filter name=cloudferry-migrator_api_1) \
  wget -q -O - http://127.0.0.1:3000/api/readyz
```

Ожидаемо `{"ok":true,...}`. Проверка «rclone недоступен»: остановите rclone
(`sudo docker stop <rclone>`) — `/api/readyz` должен вернуть 503, а
`/api/healthz` — по-прежнему 200 с `"ok":false`. Поднимите контейнер обратно.

### 6.5 `/downloads` виден движку

```sh
sudo docker exec $(sudo docker ps -q --filter name=cloudferry-migrator_rclone_1) \
  rclone lsf /downloads --max-depth 1
```

Папка должна читаться (пустой список — тоже норма, если файлов ещё нет).
Если команда падает — папка не смонтирована в `rclone`.

### 6.6 Подключение облака

- «Подключить облако» → пароль приложения (Яндекс 360) или своё
  OAuth-приложение / вход по коду.
- «Проверить подключение» в меню аккаунта: должны прийти занятое/всего место
  в облаке и список папок.
- В логах api не должно быть токенов и паролей.

### 6.7 Тестовая папка

Создайте в облаке папку `cloudferry-smoke` с 2–3 небольшими файлами, запустите
скачивание в `/downloads/cloudferry-smoke`. Проверьте:

- файлы появились через Umbrel **Files → Downloads → cloudferry-smoke**;
- прогресс, скорость и оставшееся время обновляются;
- по завершении статус «Готово», ошибок нет.

### 6.8 Пауза и продолжение

- Поставьте скачивание на паузу в середине, затем «Продолжить»: уже
  скачанные файлы не перекачиваются, недокачанный файл начинается заново
  (ограничение rclone, см. README).
- Остановите и запустите снова — задача не должна «зависнуть» в работе.

### 6.9 Вторая папка `/destination`

- В настройках приложения выберите второй слот папки, в интерфейсе укажите
  `/destination`.
- Скачайте тестовую папку туда: файлы видны в Files в выбранной папке, в
  `/downloads` их нет.
- Проверьте, что свободное место в интерфейсе соответствует этой папке.

### 6.10 Перезагрузка Umbrel

```sh
sudo reboot
```

После загрузки: приложение запустилось само, оба контейнера `healthy`,
`/api/readyz` = 200, состояние и история скачиваний на месте, незавершённая
задача показана как «Прервано» и продолжается кнопкой.

### 6.11 Грязная остановка (главная проверка stale-сокета)

1. `sudo docker kill $(sudo docker ps -q --filter name=cloudferry-migrator_rclone_1)`
   — остаётся файл сокета.
2. Перезапустите приложение в Umbrel (или `sudo docker start` контейнеров).
3. Ожидаемо: `hooks/pre-start` удалил `*.sock`, rclone поднялся, в логах нет
   `address already in use`, оба контейнера `healthy`.

### 6.12 Виджет

- Umbrel home screen → добавить виджет **Cloud Ferry**.
- Запустите скачивание: виджет показывает «N из M ГБ · скорость» и прогресс;
  без активных задач — «Нет активных скачиваний».
- Виджет ходит на `api:3000/api/widget` без cookies; в Umbrel-режиме у API нет
  собственной авторизации, поэтому запрос проходит.

## 7. Откат

### Быстрый откат образа

1. Верните предыдущий digest в `cloudferry-migrator/docker-compose.yml`
   (`git revert` коммита пиннинга или `make pin-digest TAG=<old> DIGEST=<old>`).
2. Umbrel обновляет приложение только при смене `version` в манифесте. Поэтому
   для отката поднимите версию (например, `0.1.1` при откате образа `0.1.0`)
   и закоммитьте манифест: пользователь получит «обновление» с прежним кодом.
   Вариант без смены версии — удалить и установить приложение заново, но это
   удалит данные приложения.
3. `make check-version` перед пушем ветки.

### Откат данных

- `state.json` пишется атомарно, рядом лежит `state.json.bak`; при
  повреждении основной файл подменяется бэкапом.
- `rclone.conf` **не** попадает в бэкапы Umbrel (`backupIgnore`): после
  восстановления устройства облако нужно подключить заново. Это осознанное
  решение — токены не уходят в бэкап.
- Перед откатом сделайте копию `<app-data>/data/` (без `rclone.conf`, если
  копия уедет с устройства).

### Убрать store

App Store → ⋮ → Community App Stores → удалить магазин. Уже установленное
приложение при этом не удаляется; удаляется само приложение через его карточку.

## 8. Что осталось проверить на устройстве

В среде подготовки не было Docker и Umbrel, поэтому не проверено вживую:

- сборка образа и multi-arch (`docker buildx`), в том числе шаг
  `HEALTHCHECK` в Dockerfile;
- `rclone rc --unix-socket` в образе 1.76.0 — синтаксис сверен с
  документацией rclone v1.76.0, но не запускался;
- официальный линтер Umbrel: `make lint` не запускался (нужен клон
  `getumbrel/umbrel-apps` с `npm ci`); правила сверены с исходником
  `.tools/lint-apps.mjs` вручную;
- передача `OWNER` из `exports.sh` в подстановку compose в umbrelOS 2.0;
- срабатывание `hooks/pre-start` (удаление stale-сокета) до старта compose;
- `installSize` — отображается ли он для community store и в каких единицах;
- отрисовка PNG-иконки в карточке store;
- публичная видимость пакета GHCR после первого push;
- свободен ли порт `8741` на конкретном устройстве;
- виджет на домашнем экране Umbrel (endpoint и формат ответа).

## 9. Чего не делать

- Не коммитить токены `GHCR_TOKEN`, пароли приложений и `rclone.conf`:
  секреты хранятся только в окружении/на устройстве.
- Не публиковать образ с тегом `latest` и не подставлять дайджест отдельной
  архитектуры.
- Не использовать `client_id` самого rclone для Яндекса — права отозваны,
  запросы возвращают `410 GoneError` (см. README).
- Не запускать `git commit`/`git push` без отдельного согласования: этот
  документ описывает подготовку, а не публикацию.
