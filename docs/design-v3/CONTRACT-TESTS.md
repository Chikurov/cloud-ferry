# Контрактные тесты Cloud Ferry: пробелы ревью

Задача: закрыть Go-контрактными тестами 8 пробелов из ревью. Продакшен-код не менялся,
коммитов нет. Только новые `*_test.go` и этот отчёт.

Итог: **11 новых top-level тестов** (35 табличных подкейсов) в трёх новых файлах.
Новых багов не выявлено — все тесты зелёные на текущем коде. Часть пунктов задачи уже была
покрыта регрессионными тестами коммита `10451f2` («Fixes from review», 15.11.2026); их
намеренно не дублировал, а прогнал в общем прогоне и расширил реальные пробелы.

## Что добавлено

| Файл | Тест | Пункт |
|---|---|---|
| `internal/jobs/contract_test.go` | `TestConcurrentStartDifferentKeysOneDestConflict` | 1 |
| `internal/jobs/contract_test.go` | `TestStopWhileLaunchInFlightStopsOrphanTransfer` | 4 (вторая половина окна) |
| `internal/jobs/contract_test.go` | `TestLaunchConfigCarriesTransferLimit` | 8 (проводка в `_config`) |
| `internal/httpapi/contract_test.go` | `TestConcurrentCreateJobDifferentKeysOneConflict` | 1 (HTTP-контракт) |
| `internal/httpapi/contract_test.go` | `TestPreflightSourceFolderErrorsAreBadRequests` | 2 (расширение) |
| `internal/httpapi/contract_test.go` | `TestCreateAccountRejectsLocalHostsAndEnforcesLengths` | 6 |
| `internal/httpapi/contract_test.go` | `TestCreateJobClientTokenKeyBoundaries` | 7 |
| `internal/httpapi/contract_test.go` | `TestCreateJobValidHeaderWinsOverInvalidClientToken` | 7 (приоритет) |
| `internal/httpapi/contract_test.go` | `TestCreateJobConflictNamesKeyField` | 7 (409 + `field`) |
| `internal/httpapi/contract_test.go` | `TestFailedLaunchRetrySucceedsAfterEngineRecovers` | 5 (расширение) |
| `internal/store/contract_test.go` | `TestPruneJobsKeepsActiveJobsOverLimit` | 3 (дополнение) |

## Покрытие 8 пунктов

| № | Пробел | Тест(ы) | Что проверяет | Статус |
|---|---|---|---|---|
| 1 | Конкурентный `claimDest` с **разными** ключами | `TestConcurrentStartDifferentKeysOneDestConflict`, `TestConcurrentCreateJobDifferentKeysOneConflict` | Два одновременных старта с разными `Idempotency-Key` в одну папку: ровно одна задача создана (running), вторая получает `ErrConflict`/409, в state ровно одна запись, активных задач на dest ровно одна, `sync/copy` вызван один раз. В manager-тесте оба запроса детерминированно проходят `operations/stat` (барьер в `fakeRC.onCall`) до захвата dest — окно гонки максимальное, без `time.Sleep`. | новый |
| 2 | Preflight отсутствующей папки → 400 с `field`, не 502 | `TestReviewPreflightMissingFolderIsBadRequest` (был) + `TestPreflightSourceFolderErrorsAreBadRequests` | Уже было: `sourcePath`/`sourcePaths` с пустым `item`. Добавлено: rclone-404 (`*rc.APIError{Status:404}`), путь существует, но это файл, и частично отсутствующий список папок — во всех случаях 400, правильное поле, имя папки в тексте, `operations/size` не вызван. | расширен |
| 3 | `pruneJobs`: живой ключ не вытесняется, истёкший — вытесняется | `TestReviewPruneKeepsLiveIdempotencyKeys` (был) + `TestPruneJobsKeepsActiveJobsOverLimit` | Уже было: живой ключ спасает самую старую запись при >100 задачах, ключ старше 24 ч не защищает. Добавлено: running/queued/verifying с истёкшим ключом не вытесняются никогда (запись идёт по статусу раньше ключа), обычные старые записи при этом всё равно режутся до `maxJobs`. | расширен |
| 4 | `Stop` до `launch` не запускает движок | `TestReviewStopBeforeLaunchPreventsTransfer` (был) + `TestStopWhileLaunchInFlightStopsOrphanTransfer` | Уже было: остановка queued-записи до `launchQueued` → `errJobNotQueued`, `sync/copy` = 0, статус canceled. Добавлено: `Stop` приходит, когда `sync/copy` уже в полёте — `launchQueued` возвращает `errLaunchWindowLost`, запись canceled без rclone id, свежий engine-job останавливается (`job/stop` = 1) — не остаётся «сироты», пишущей в папку. | расширен |
| 5 | Повтор с тем же ключом после `launch`-ошибки | `TestReviewStartRetriesAfterLaunchFailure`, `TestReviewStoredFailedJobWithoutTransferIsNotReplayed`, `TestReviewFailedLaunchIsRetriedWithSameKey` (были) + `TestFailedLaunchRetrySucceedsAfterEngineRecovers` | Уже было: ключ снимается при `markFailed`; два POST с одним ключом дают два 502 и два запуска движка, без `Idempotent-Replay`; сохранённая failed-запись без rclone-id не реплеится. Добавлено: после восстановления движка тот же ключ даёт третий запуск — 201, running, ключ сохранён на успешной задаче; две прошлые задачи остались failed (ключ не «залип» на ошибке). | расширен |
| 6 | Валидация `POST /api/accounts`: приватные/loopback/link-local, границы длины | `TestReviewCreateAccountValidatesInput` (был) + `TestCreateAccountRejectsLocalHostsAndEnforcesLengths` | Уже было: 127.0.0.1, 192.168.1.10, 169.254.10.1, `::1`, `localhost`, длинный user, неизвестный provider; rclone не вызывается. Добавлено: 10/8, 172.16/12, `0.0.0.0`, IPv6 ULA `fc00::1`, IPv6 link-local `fe80::1`, `foo.localhost` отклоняются; публичные IPv4/IPv6-литералы и hostname принимаются (201 + `config/create`); все границы длины (URL 2048, user 256, password 4096, token 16384, clientId 256, clientSecret 256) — ровно на лимите 201, на 1 больше 400 с нужным `field`, в rclone не уходит. | расширен |
| 7 | Ключ идемпотентности: границы 8/128, символы, приоритет заголовка, 409 | `TestCreateJobInvalidKeyRejected`, `TestCreateJobKeyBoundaries`, `TestCreateJobHeaderWinsOverClientToken`, `TestCreateJobSameKeyOtherParametersConflict` (были) + `TestCreateJobClientTokenKeyBoundaries`, `TestCreateJobValidHeaderWinsOverInvalidClientToken`, `TestCreateJobConflictNamesKeyField` | Уже было: 7/8/128/129 в заголовке, пробел и `/` в заголовке, кириллица в `clientToken`, невалидный заголовок важнее валидного токена, 409 при других dest/source/mode, replay заголовка. Добавлено: границы и алфавит именно `clientToken` (8/128/129, пробел, `+`, `/`, `@`), валидный заголовок важнее **невалидного** `clientToken` (тело не мешает), 409 называет поле источника ключа (`Idempotency-Key` или `clientToken`), а не только код. | расширен |
| 8 | `transferLimit` при свободном месте <1 ГиБ | `TestReviewTransferLimitForSmallFreeSpace` (был) + `TestLaunchConfigCarriesTransferLimit` | Уже было: 512 МиБ → `460M`, 2 МиБ → `1M`, 500 Б → `450B`, 0 → лимита нет (раньше лимит включался только выше 1 ГиБ). Добавлено: `launch` реально кладёт `MaxTransfer` в `_config` запроса `sync/copy`, и значение совпадает с `transferLimitForFree(disk.Free(dest))` — формула не отрывается от проводки. | расширен |

## Уже было покрыто до задачи

В коммите `10451f2` (15.11.2026) вместе с исправлениями уже добавлены регрессионные тесты:
`internal/jobs/review_backend_test.go`, `internal/httpapi/review_backend_test.go`,
`internal/store/review_prune_test.go`. По пунктам 2, 3, 4, 5, 6, 8 они закрывают основную
часть контракта (и по 7 — значительную). Дублировать их не стал: пункт «не дублировать
существующие» важнее формального «у каждого пункта новый тест». Все они участвуют в общем
прогоне и зелёные.

## Найденные баги

В продакшен-коде новых багов не выявлено: 11 новых тестов проходят на текущем коде.

Отдельно: **премиса задачи частично устарела** — 6 из 8 пунктов уже имели регрессионные
тесты (см. выше), поэтому работа свелась к реальным пробелам: пункт 1 (конкурентность с
разными ключами), расширения пунктов 2, 5, 6, 7 и дополнения 3, 4, 8. Это стоит учесть при
следующем ревью, чтобы не заводить задачи на уже покрытое.

## Наблюдения и остаточные риски (тестами не закреплено)

Это не баги по текущей документации кода, поэтому падающие тесты не добавлял (suite должен
оставаться зелёным, а продакшен менять нельзя):

1. **Zone-qualified IPv6 обходит запрет link-local.** `POST /api/accounts` с
   `url: "http://[fe80::1%25eth0]"` проходит валидацию: `url.Hostname()` = `fe80::1%eth0`,
   `net.ParseIP` → nil, `isLocalHost` → false (проверено отдельным прогоном `net/url` +
   `net.ParseIP`). Формально это адрес локальной сети, который валидатор обещает отклонять
   (SSRF-гаранд). Рекомендация: в `isLocalHost` использовать `netip.ParseAddr` с
   отбрасыванием `%zone`. Severity низкая: форма доступна администратору инсталляции, для
   соединения нужно знать имя интерфейса.
2. **Числовые формы IPv4.** `http://2130706433` (десятичный 127.0.0.1) и hex-формы проходят:
   `net.ParseIP` не парсит их, а `isLocalHost` проверяет только литералы. Это прямо описано в
   комментарии к `isLocalHost` как остаточный риск («DNS may answer differently…»), не баг.
3. **DNS-имена, резолвящиеся в приватные адреса.** Задокументированный остаточный риск:
   валидация намеренно не резолвит имена.
4. **Preflight не проверяет отдельные `sourceFiles`** (by design, по комментарию в коде):
   пропавший файл даёт 200 на preflight и 400 только при создании задачи. В контракт пункта 2
   не входит, отдельным тестом не закреплял.

## Что не покрыто и почему

- **`transferLimit` на реальном диске с <1 ГиБ.** Нет способа смонтировать маленькую ФС в
  unit-тесте; покрыты формула (`transferLimitForFree`, тест из `10451f2`) и проводка в
  `_config` (новый тест). Интеграционный тест с настоящим малым свободным местом невозможен.
- **Точная граница окна 24 ч в `pruneJobs`.** `pruneJobs` читает `time.Now()` без инъекции
  часов, поэтому равенство «ровно 24 ч» недетерминированно. Покрыты окна с запасом
  (`>25 ч` — вытесняется, живой ключ — защищён). Согласованность окна менеджера и store
  гарантируется компилятором: `const idempotencyWindow = store.IdempotencyWindow`.
- **`Stop` ровно в микроокне между `AddJob` и перечитыванием статуса.** Детерминированно
  воспроизвести можно только хуком внутри продакшен-кода (запрещено). Покрыты обе
  наблюдаемые стороны: stop до launch (тест из `10451f2`) и stop во время `sync/copy`
  (новый тест).
- **`t.Parallel` не использован.** Тесты пакетов мутируют общие для пакета фейки и файловый
  state store; все кейсы быстрые (<0.2 с), параллелизм не ускоряет прогон, но добавляет риск
  флапов. По условию задачи — только там, где безопасно.
- **`time.Sleep` не использован ни в одном тесте.** Менеджер-тесты гоняют хуки и фейки
  синхронно; в конкурентном тесте единственный `time.After(5s)` — watchdog барьера, он
  срабатывает только при поломке кода и не влияет на нормальный ход.

## Детерминированность

- Конкурентные тесты не полагаются на удачное расписание: manager-тест барьером в
  `fakeRC.onCall` дожидается, что оба запроса прошли проверку источника, и только потом
  отпускает их в захват dest; HTTP-тест проверяет инвариант, не зависящий от порядка
  (всегда ровно один 201 и один 409).
- Вместо ожиданий используются существующие хуки: `fakeRC.onCall` (вызывается после записи
  вызова), `engine.setError`, `engine.setStat`, `setResponse`.
- Все новые HTTP-тесты изолированы по `newTestServer(t)` (свой tempdir/store/httptest).

## Проверки

Выполнено в `cloud-ferry` с `GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache`:

```
go test -race -count=1 ./...   # три прогона подряд: PASS, PASS, PASS
go vet ./...                   # PASS
gofmt -l internal cmd          # пусто
```

Пакеты и время последнего прогона: `httpapi 8.5s`, `jobs 4.9s`, `store 9.6s`, остальные —
зелёные. Флапов между тремя прогонами не было.

## Изменённые файлы

Добавлены только тесты и этот отчёт; продакшен-код и существующие тесты не тронуты,
коммитов нет:

- `internal/jobs/contract_test.go` (3 теста)
- `internal/httpapi/contract_test.go` (7 тестов)
- `internal/store/contract_test.go` (1 тест)
- `docs/design-v3/CONTRACT-TESTS.md` (этот файл)
