# Cloud Ferry: сборка, публикация и установка на Umbrel

От исходников до работающего приложения в community app store. Публикация в
официальный `getumbrel/umbrel-apps` не требуется.

> Состояние на 0.3.1: образ `ghcr.io/chikurov/cloud-ferry:0.3.1` опубликован,
> digest `sha256:70cbd481…347d` закреплён в
> `cloudferry-migrator/docker-compose.yml`, пакет GHCR публичный, репозиторий
> `github.com/chikurov/cloud-ferry`. Для установки достаточно раздела
> «Установка из store»; остальное — про выпуск следующей версии.

## Требования

| Что | Зачем |
| --- | --- |
| GitHub-аккаунт, публичный репозиторий | community store и GHCR-образ должны быть доступны без авторизации |
| Docker Engine 24+ с `buildx` | multi-arch сборка `linux/amd64` + `linux/arm64` и проверки реестра |
| GitHub token со scope `write:packages` (и `read:packages`) | локальный push в GHCR; в CI достаточно `GITHUB_TOKEN` |
| Umbrel с umbrelOS **2.0+**, свободный порт `8741` | `manifestVersion: "2.0"`, `folderAccess`, `storage.dataRoot` |
| SSH-доступ к Umbrel | диагностика контейнеров и сокета |
| Go 1.24+ (только для локальных проверок) | `go build ./...`, `go test ./...` |

## Контракт версии и digest

Единый формат — **без ведущей `v` в образе**:

| Место | Значение для 0.3.1 |
| --- | --- |
| git-тег | `v0.3.1` |
| тег образа | `ghcr.io/chikurov/cloud-ferry:0.3.1` |
| `umbrel-app.yml` → `version` | `"0.3.1"` |
| `Makefile` → `TAG` | `0.3.1` |
| compose | `cloud-ferry:0.3.1@sha256:<digest индекса>` |

CI снимает ведущую `v` (`${GITHUB_REF_NAME#v}`), падает на теге, который не
похож на `vX.Y.Z`, и передаёт в Dockerfile `VERSION` без `v`.

Digest в compose — это digest **manifest list** (индекс с `linux/amd64` и
`linux/arm64`), а не digest отдельной архитектуры: с дайджестом архитектуры
установка пройдёт только на одной из них.

### Нулевой digest недопустим

`sha256:000…000` (64 нуля) означает «образ ещё не публиковался». Такой пин
нельзя установить: тег `v0.3.1` однажды уже уехал с нулевым digest, а реальный
`sha256:70cbd481…347d` появился отдельным коммитом после тега — то есть
установка по тегу гарантированно ломалась. Сейчас это жёсткая ошибка везде:

- `make check-version` падает с `the digest is the zero placeholder`, а он же
  выполняется на каждый push в `main` и pull request;
- job `image` на релизном теге падает (`Release tag must pin a real digest`);
- `make release-tag` не создаст тег, пока digest нулевой;
- `make pin-digest` отказывается записывать нулевой digest.

Проверить текущее состояние:

```sh
make check-version
grep -n 'image: ghcr.io/.*/cloud-ferry' cloudferry-migrator/docker-compose.yml
```

## Владелец образа: один источник

Владелец записан **литералом** в `cloudferry-migrator/docker-compose.yml`
(`image: ghcr.io/chikurov/cloud-ferry:…`) и это единственный источник правды:

- `Makefile` читает его оттуда (`OWNER`, `make owner`);
- `umbrel-app.yml` (`website`, `repo`, `support`, `submission`, `icon`) и
  `Dockerfile` (OCI-лейблы) обязаны использовать того же владельца.

`${OWNER}` в compose **не используется намеренно**: umbreld не раскрывает
переменные из `exports.sh` при pull образа, и неразвёрнутый владелец
превращается в `ghcr.io//cloud-ferry` → 404 при установке. Поэтому
`exports.sh` больше не экспортирует `OWNER` (там только rc-секрет), а
`make check-version` отдельно запрещает `export OWNER=` в `exports.sh` и
`${OWNER}` в строке образа.

Смена владельца одной командой:

```sh
make set-owner OWNER=<github-login>   # только строчные буквы, цифры и дефис
git diff -- cloudferry-migrator Dockerfile
```

Команда переписывает ссылку на образ в compose, URL в манифесте и лейблы в
`Dockerfile`, принимает только `^[a-z0-9-]+$` (пути GHCR регистрозависимы,
логин GitHub — нет) и проверяет результат по **новому** значению. Поэтому она
идемпотентна и не падает на grep, если в файлах остался владелец в другом
регистре или старый логин.

Если владелец в compose не совпадает с владельцем репозитория, оба job'а CI
падают с явной ошибкой — форк не опубликует образ в чужой namespace.

## Порядок релиза: две фазы

Digest появляется только после сборки, а тег должен указывать на коммит, в
котором digest уже закреплён. Поэтому релиз разделён на две фазы — публикацию
и проверку — и идёт в таком порядке:

1. **опубликовать** образ новой версии, пока `main` ещё на старой: никто не
   получит манифест, который ссылается на несуществующий образ;
2. **поднять версию и закрепить digest одним коммитом** в `main`;
3. **поставить тег** и дать CI проверить, что пин совпадает с опубликованным
   образом.

Job `image` на теге **не пересобирает** образ, а сверяет пин с реестром:
повторная сборка того же тега даёт другой digest индекса (слои не
воспроизводятся байт в байт), и тогда проверка «compose == только что
собранный индекс» либо ложно ломает каждый релиз, либо подменяет артефакт,
который пользователи уже скачали. Проверять надо именно то, что они получат.

### Шаг 1. Опубликовать образ

Через GitHub Actions (рекомендуется):

- **Actions → build → Run workflow**, ветка `main`, `version` = `0.3.2`
  (без `v`); кнопка появляется после того, как workflow попал в `main`;
- job `publish` ждёт `test`, собирает `linux/amd64` + `linux/arm64`, печатает
  `Manifest-list digest: sha256:…` в Summary и падает, если образ этой версии
  уже опубликован (перевыпуск запрещён) или workflow запущен не из `main`.

Локально:

```sh
make docker-push TAG=0.3.2
make digest TAG=0.3.2         # digest индекса, не отдельной архитектуры
```

Пока версия в `main` не поднята, старый манифест продолжает отдавать старую
версию с её настоящим digest — установка не ломается.

При первой публикации нового пакета: **GitHub → Packages → `cloud-ferry` →
Package settings → Change visibility → Public**. Пакет, созданный
`GITHUB_TOKEN`, по умолчанию приватный, а Umbrel тянет образ без авторизации —
приватный пакет даёт 404/`denied` при установке. Для `0.3.1` это уже сделано.

### Шаг 2. Поднять версию и закрепить digest

Обе команды выполняются до коммита, одним состоянием:

```sh
make set-version VERSION=0.3.2     # TAG, version и тег в compose; digest -> нули
make pin-digest TAG=0.3.2 DIGEST=sha256:<digest из шага 1>
make check-version                 # OK: версия и реальный digest совпадают
git add Makefile cloudferry-migrator/umbrel-app.yml cloudferry-migrator/docker-compose.yml
git commit -m "0.3.2: bump version and pin image digest"
git push origin main               # только после согласования
```

Между `set-version` и `pin-digest` в `main` ничего не пушится: коммит с
нулевым digest означает 404 у всех, кто ставит или обновляет приложение.
Поэтому CI запускает `make check-version` на каждый push в `main` и на pull
request — такой коммит краснеет, даже если его всё же запушили.

`set-version` меняет `TAG` в `Makefile`, `version` в манифесте и тег в compose
одной операцией и обнуляет digest; `pin-digest` записывает в него digest
индекса из шага 1.

### Шаг 3. Поставить тег

```sh
make release-tag TAG=0.3.2
```

`release-tag` ничего не пушит. Он проверяет и только затем создаёт
аннотированный тег `v0.3.2`:

- версия совпадает в `Makefile`, манифесте и compose;
- digest не нулевой, формата `sha256:<64 hex>`;
- пин закоммичен (`git diff HEAD` по релизным файлам пуст, а в `HEAD` compose
  уже содержит нужный digest);
- в реестре для `ghcr.io/<owner>/cloud-ferry:0.3.2` лежит ровно этот digest;
- в индексе есть `linux/amd64` и `linux/arm64`;
- пакет публичный (анонимный pull);
- тега `v0.3.2` ещё нет — опубликованный тег никогда не переставляется.

Без Docker проверки реестра пропускаются
(`make release-tag TAG=0.3.2 IMAGE_CHECK=0`) — тогда тег ставить нельзя, пока
digest и архитектуры не сверены вручную.

Пуш тега — отдельное действие, после проверки:

```sh
git push origin v0.3.2
```

После пуша CI запускает `test`, затем job `image`. Он проверяет, что тег стоит
на `main`, версия совпадает везде, digest не нулевой, совпадает с
опубликованным индексом, в индексе обе архитектуры, пакет публичный. Любое
расхождение — красный job, релиз не считается выпущенным.

### Откат на предыдущую версию

Umbrel обновляет приложение только при смене `version` в манифесте, а compose
обязан ссылаться на тег своей версии. Поэтому откат — это новая версия с
прежним кодом, а не возврат старого digest под новой версией (такой тег в
реестре не существует, и `make check-image` справедливо его не пропустит):

```sh
git revert <плохой коммит>        # исходники возвращаются к прежнему коду
# дальше обычный порядок: publish 0.3.3 -> set-version + pin-digest -> release-tag
```

Вариант без смены версии — удалить и установить приложение заново, но это
удалит данные приложения.

## Локальные проверки

```sh
make check-version     # офлайн: версии, digest, владелец, rclone, Dockerfile
make check-image       # реестр: digest = пин, amd64+arm64, публичность (нужен docker)
make -n docker-push    # что именно соберётся
sh -n cloudferry-migrator/exports.sh
```

`check-version` не ходит в сеть и работает без Docker; `check-image` требует
Docker и опубликованный пакет. `make lint` — официальный линтер Umbrel (нужен
клон `getumbrel/umbrel-apps` с `npm ci`):

```sh
make lint UMBREL_APPS=/tmp/umbrel-apps
make lint-images UMBREL_APPS=/tmp/umbrel-apps
```

## Установка из store

1. Репозиторий **публичный**, в ветке по умолчанию лежат корневой
   `umbrel-app-store.yml` и папка `cloudferry-migrator/`.
2. На Umbrel: **App Store → ⋮ → Community App Stores → Add → URL**
   `https://github.com/chikurov/cloud-ferry`.
3. В добавленном магазине появится **Cloud Ferry 0.3.1**; **Install**.
4. В настройках приложения выберите папку назначения (**Downloads** по
   умолчанию, второй слот — необязательная папка `/destination`).
5. Откройте приложение на `http://umbrel.local:8741` и подключите Яндекс Диск
   (способы — в README).

`umbrel-app-store.yml` имеет `id: cloudferry`; id приложения
`cloudferry-migrator` начинается с `cloudferry-` — требование community store.

## Smoke-тест после установки

```sh
sudo docker ps --filter name=cloudferry
# api и rclone -> Up (healthy); app_proxy отдельно в umbrelOS 2.0 не создаётся
sudo docker logs --tail=100 $(sudo docker ps -q --filter name=cloudferry-migrator_api_1)
sudo docker exec $(sudo docker ps -q --filter name=cloudferry-migrator_api_1) \
  wget -q -O - http://127.0.0.1:3000/api/readyz      # {"ok":true,...}
```

Проверьте по порядку: оба контейнера `healthy`; в логах нет
`address already in use` и `dial unix /run/rclone/rclone.sock`; `/api/readyz`
отдаёт 200, а при остановленном rclone — 503; папка видна движку
(`rclone lsf /downloads`); скачивание тестовой папки, пауза и продолжение;
перезагрузка Umbrel — приложение поднялось само, незавершённая задача показана
как «Прервано» и продолжается кнопкой; виджет на домашнем экране показывает
прогресс.

## Диагностика

### 404 при установке или обновлении

404 (или `manifest unknown` / `denied`) означает, что Umbrel не может скачать
образ. Проверяйте по порядку:

```sh
# 1. пакет публичный? анонимный pull должен вернуть 200
curl -s -o /dev/null -w '%{http_code}\n' \
  -H 'Accept: application/vnd.oci.image.index.v1+json' \
  "https://ghcr.io/v2/chikurov/cloud-ferry/manifests/0.3.1"

# 2. такой образ существует и это индекс с двумя архитектурами?
docker buildx imagetools inspect ghcr.io/chikurov/cloud-ferry:0.3.1

# 3. пин в compose — реальный digest из шага 1, не нулевой?
make check-version
```

`200` — пакет публичный, `401`/`403` — приватный или такого образа нет.

Частые причины: пакет приватный (**Package settings → Change visibility →
Public**); в compose нулевой digest или digest другой сборки; версия в
манифесте выше, чем опубликованный образ (обновили манифест, но не собрали);
владелец в compose не совпадает с репозиторием.

### Приложение не обновляется

Umbrel видит обновление только при смене `version` в `umbrel-app.yml`. Если
версия не изменилась, пользователь останется на старом образе — поэтому откат
оформляется новой версией.

### Контейнеры не поднимаются

```sh
sudo docker ps --filter name=cloudferry
sudo docker logs --tail=100 <api|rclone>
```

`permission denied` на томах — каталоги должны принадлежать uid 1000;
`rclone не отвечает` — проверьте `*.sock` в `<app-data>/data/run` (stale-сокет
удаляет `hooks/pre-start`); `api` в `unhealthy` при живом rclone — запустите
`/usr/local/bin/cloudferry -healthcheck` вручную.

## Что не проверено в этой среде

Docker и Umbrel здесь недоступны, поэтому не проверялись вживую:

- multi-arch сборка `docker buildx` и шаг `HEALTHCHECK` в образе;
- `make check-image` и `make release-tag` с проверками реестра (нет Docker);
  digest, архитектуры и публичность `0.3.1` сверены напрямую через Registry API
  GHCR: `sha256:70cbd481…347d`, `linux/amd64` + `linux/arm64`, анонимный pull
  отдаёт 200;
- сам workflow на GitHub Actions (локально проверены только YAML и shell-шаги);
- официальный линтер Umbrel (`make lint`);
- `hooks/pre-start` и виджет на устройстве.

## Чего не делать

- Не ставить тег до того, как пин закоммичен, и не переставлять уже
  опубликованный тег.
- Не публиковать повторно существующую версию: меняйте версию, иначе у
  пользователей останется старый digest.
- Не подставлять `${OWNER}` в compose и не возвращать `export OWNER=` в
  `exports.sh`.
- Не публиковать образ с тегом `latest` и не пинить digest отдельной
  архитектуры.
- Не коммитить токены `GHCR_TOKEN`, пароли приложений и `rclone.conf`.
- Не использовать `client_id` самого rclone для Яндекса — права отозваны,
  запросы возвращают `410 GoneError`.
- Не запускать `git commit`, `git push` и `git tag` без отдельного
  согласования: документ описывает подготовку, а не публикацию.
