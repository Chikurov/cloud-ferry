# Проверка на Umbrel без App Store

Быстрый способ запустить Cloud Ferry на реальном сервере до публикации пакета.
Образ собирается на самом Umbrel, приватный registry не нужен.

## 1. Скопировать проект на сервер

С ноутбука:

```bash
rsync -av --exclude .gocache --exclude .gomodcache --exclude bin \
  cloud-ferry/ umbrel@umbrel.local:/home/umbrel/cloud-ferry-src/
```

## 2. Подготовить каталоги и настройки

По SSH (`ssh umbrel@umbrel.local`):

```bash
cd ~/cloud-ferry-src/deploy/standalone
mkdir -p ~/cloud-ferry/state ~/cloud-ferry/run ~/cloud-ferry/rclone ~/cloud-ferry/cache
mkdir -p ~/umbrel/home/YandexDisk
cp .env.example .env
nano .env          # задайте CF_BASIC_PASS и RC_PASS (openssl rand -hex 16)
```

Проверьте путь `CF_DEST`: если домашняя папка Umbrel отличается, посмотрите её
через `ls ~/umbrel`.

Папка `CF_DEST` монтируется в оба контейнера — `api` и `rclone`. `api` читает
свободное место и проверяет пути, `rclone` пишет туда файлы. Монтировать папку
только в `api` нельзя: в этом случае rclone сохранит файлы в слой контейнера, и
они пропадут при его пересоздании.

## 3. Собрать и запустить

```bash
docker compose up -d --build
docker compose ps                 # api и rclone -> healthy, prepare -> exited (0)
docker compose logs -f api
```

Сборка занимает несколько минут: на устройстве скачивается базовый образ Go.
Одноразовый сервис `prepare` удаляет stale `*.sock` из `~/cloud-ferry/run` до
старта rclone, поэтому грязная остановка больше не даёт
`address already in use`. rclone, в свою очередь, стартует только после
`prepare`, а `api` — только когда rclone отвечает на `rc core/version`.

Проверка готовности:

```bash
docker compose exec api wget -q -O - \
  --user "$CF_BASIC_USER" --password "$CF_BASIC_PASS" \
  http://127.0.0.1:3000/api/readyz
# {"ok":true,...}; если rclone недоступен — HTTP 503
```

## 4. Открыть

`http://umbrel.local:8741` — войдите с логином и паролем из `.env`.

Дальше: «Подключить облако» → пароль приложения или своё OAuth-приложение →
выберите папку в облаке и папку на сервере → «Скачать».

## 5. Остановить

```bash
docker compose down
```

Состояние и файлы останутся в `CF_DATA` и `CF_DEST`.

## Если что-то не так

```bash
docker compose ps
docker compose logs --tail=100 api rclone
docker info --format '{{.Architecture}}'
docker inspect --format '{{.State.Health.Status}}' \
  $(docker compose ps -q api) $(docker compose ps -q rclone)
```

- `permission denied` на томах — каталоги должны принадлежать uid 1000
  (пользователь `umbrel`), создавайте их из-под него, а не через `sudo`.
- Порт занят — поменяйте `8741` в `docker-compose.yml`.
- rclone «не отвечает» — проверьте, что оба контейнера поднялись и файл
  `~/cloud-ferry/run/rclone.sock` существует. Если в логах rclone
  `address already in use`, удалите сокет и перезапустите:
  `rm -f ~/cloud-ferry/run/*.sock && docker compose up -d` (обычно это делает
  сервис `prepare`).
- `api` в статусе `unhealthy` при живом rclone — выполните вручную
  `docker compose exec api /usr/local/bin/cloudferry -healthcheck` и посмотрите
  код возврата: проверка учитывает `CF_BASIC_*`.
- Скачанные файлы не появились в `CF_DEST` — проверьте, что папка смонтирована
  в сервисе `rclone`, а не только в `api`:
  `docker compose exec rclone rclone lsf /downloads --max-depth 1`.
