# Matrix-бот: homeserver, E2EE, DM по MXID, read-only комната, лимиты

Исследование по тикету [#275](https://github.com/SBKubric/sane-3x-ui/issues/275), часть карты [#267](https://github.com/SBKubric/sane-3x-ui/issues/267). Дата: 2026-10-20.

Главный критерий — **надёжность**: что может сломаться и как это переживается.

**Стек.**
- Бот: [`maunium.net/go/mautrix` v0.32.0](https://github.com/mautrix/go/tree/v0.32.0) (2026-10-16), E2EE через `goolm`, хранилище ключей в sqlite (`mattn/go-sqlite3`).
- Homeserver в лаборатории: [continuwuity v26.9.1](https://forgejo.ellis.link/continuwuation/continuwuity/src/tag/v26.9.1), коммит `046074bb`, образ `forgejo.ellis.link/continuwuation/continuwuity:v26.9.1`.
- Для сравнения памяти: Synapse v1.163.0 (`matrixdotorg/synapse`).

**Как проверяли.** Скрипт [`matrix-bot-check/`](matrix-bot-check/) — отдельный Go-модуль, `go vet ./...` панели его не видит.
- `run.sh` поднимает два continuwuity (`hs1.test`, `hs2.test`) за одним nginx на :443. nginx делит трафик по SNI, как в [ADR 0005](../adr/0005-only-443-on-every-hop.md). Федерация идёт через `.well-known` → `matrix.hsN.test:443`.
- Затем запускается `go run -tags goolm .`. Аккаунты: бот, бот с E2EE (`botx`), подписчики `u1`–`u3` на hs1 и `r1` на hs2 (федерация).
- Лог прогона: [`run-2026-10-20.log`](matrix-bot-check/run-2026-10-20.log). Скрипт прогнан 5 раз; последние два прогона на чистой лаборатории совпали.
- На matrix.org и других публичных серверах аккаунты **не регистрировались**. Всё о них взято из документов и измерений третьих сторон.

**Ограничение проверки.** «Пользователь» в скрипте — mautrix-go, а не Element X или FluffyChat. Как сообщения **выглядят** в клиентах (рендер HTML, плашки «не проверено», кликабельность), взято из исходников и документации, а не с телефона.

Метки:
- **[вживую]** — проверено скриптом на локальных homeserver;
- **[док]** — по исходникам или документации, со ссылкой;
- **[не подтверждено]** — первоисточник не найден.

## TL;DR и рекомендация

1. **Homeserver — свой continuwuity, не matrix.org.**
   - Клиентский API matrix.org (`matrix-client.matrix.org`) с 2024-09 режется DPI в РФ: по данным OONI, 60–90 % тестов в месяц дают аномалию **[док]**. Запасной канал на matrix.org не работает именно тогда, когда у пользователя нет VPN.
   - У matrix.org были суточные сбои (2025-09-02/03, ~24 ч) **[док]**.
   - Свой continuwuity в простое занимает ~55 МБ RAM при CPU ≈ 0 **[вживую]**. Федерация через один 443 за nginx по SNI работает **[вживую]**. Бэкап — копия каталога данных, восстановление на «новой машине» проверено **[вживую]**.
   - Держать его лучше на **отдельном хосте вне РФ**, не на `real`: тогда комната platform status переживает падение панели (подробности в §1.4).
2. **E2EE — включить, с кросс-подписью бота.**
   - Element X создаёт DM зашифрованными **[док]**. Бот без E2EE видит в таком DM только `m.room.encrypted` и не может прочитать команду пользователя **[вживую]**.
   - Spec v1.19 велит не показывать сообщения с устройств, не подписанных владельцем. Element включает это правило с октября 2026 **[док]**. Поэтому бот обязан кросс-подписать своё устройство: после подписи у пользователя `cross-signed-tofu` **[вживую]**.
   - `.conf` содержит приватный ключ. Без E2EE он лежит открытым текстом на homeserver пользователя (например, matrix.org).
   - Все сбои E2EE, которые проверялись, переживаются по рецепту из §2.3 **[вживую]**. Нельзя только одно: сбрасывать identity бота. Тогда у пользователя `cross-signed-untrusted`, то есть предупреждение о смене личности.
3. **Привязку начинает пользователь:** пишет боту в DM `/link КОД` из tg. `sender` события подтверждает его homeserver, это и есть доказательство владения MXID **[вживую]**.
   - Приглашения от бота может блокировать сам пользователь: `M_INVITE_BLOCKED` **[вживую]**. Если пользователь игнорирует бота, приглашение проходит без ошибки, но молча пропадает **[вживую]**.
4. ⚠️ **В read-only комнате подписчики видят друг друга.** `/joined_members` отдаёт всех участников **[вживую]**. Если комната `world_readable`, список отдаётся даже тем, кто в ней не состоит **[вживую]**. Скрыть участников протокол не позволяет **[док]**. Это противоречит ожиданию приватности для VPN-подписчиков; альтернативы в §4.
5. **Кабинет.**
   - `formatted_body` доходит байт-в-байт **[вживую]**. `<b> <i> <code> <pre> <a>` есть в рекомендованном спекой списке тегов **[док]**.
   - Кнопок в спеке нет **[док]**. Кликабельность `/команд` в клиентах **[не подтверждено]**.
   - `.conf` уходит как `m.file`, PNG как `m.image`, в E2EE-комнате — зашифрованным вложением **[вживую]**.
   - Медиа скачиваются только с токеном: без него 401, legacy-эндпоинт отвечает 403 **[вживую]**.
6. **Эксплуатация.**
   - У continuwuity нет лимитов на отправку от клиента: 200 сообщений за 0,5 с без единого 429, 30 комнат с приглашением — тоже без 429 **[вживую]** + **[док]**. У Synapse по умолчанию `rc_message` 0,2/с при burst 10, бота нужно освобождать от лимита **[док]**.
   - ⚠️ **Push в РФ ненадёжен.** Element X шлёт push через шлюз matrix.org в FCM/APNs, а в 2026 были сообщения о деградации FCM/APNs в РФ **[док]**. «У вас кончается подписка» может прийти только при открытии приложения.

## 1. Homeserver

### 1.1. Публичный (matrix.org и др.)

| Вопрос | Факт | Источник |
|---|---|---|
| Доступность из РФ | `matrix-client.matrix.org` (клиентский API matrix.org): аномалии в РФ 2/1650 в 2024-08, 724/1745 в 2024-09, 342/382 в 2026-01, 376/507 в 2026-10. Тип — в основном `http-failure` (DPI), на многих AS (Ростелеком и др.). Сами `matrix.org` и `app.element.io` в основном доступны (2–5 %). Записи в реестре РКН не найдено | **[док]** [OONI aggregation](https://api.ooni.io/api/v1/aggregation?probe_cc=RU&domain=matrix-client.matrix.org&axis_x=measurement_start_day) |
| Клиенты в РФ | Element X (iOS) есть в RU App Store (v26.10.4, 2026-10-19). Element Android виснет на логине, потому что сначала обращается к matrix.org | **[док]** iTunes lookup `country=ru`; [element-android#8924](https://github.com/element-hq/element-android/issues/8924) |
| Регистрация бота | Через MAS (account.matrix.org): reCAPTCHA и подтверждённый email. ToS требует 18+ и «valid, verified email», запрещает «unsolicited or bulk communications» (§7.1) и допускает ограничения «fair usage» (§7.2). Отдельных правил для ботов нет | **[док]** [ToS](https://matrix.org/legal/terms-and-conditions/), [MAS на matrix.org](https://matrix.org/blog/2025/04/morg-now-running-mas/) |
| Лимиты | Лимиты на сообщения matrix.org не публикует **[не подтверждено]**. Медиа на бесплатном тарифе: 10 МБ на файл, 100 МБ в сутки, 500 МБ за 28 дней | **[док]** [pricing](https://matrix.org/homeserver/pricing/), [terms update 2025-08](https://matrix.org/blog/2025/07/terms-update/) |
| Сбои | 2025-09-02 17:45 → 09-03 18:00 UTC — полный простой ~24 ч: RAID, затем ошибочно удалённый каталог primary-БД, восстановление 51 ТБ из S3. 2025-07-02…16 — порча индексов Postgres: в части комнат не работали отправка и приглашения. 2026-04-16 — не отправлялись сообщения ~50 мин. 2026-08-03 — проблемы с логином ~11 ч | **[док]** [post-mortem 2025-09](https://matrix.org/blog/2025/10/post-mortem/), [postgres 2025-07](https://matrix.org/blog/2025/07/postgres-corruption-postmortem/), [status RSS](https://status.matrix.org/history.rss) |
| Другие | tchncs.de: reCAPTCHA, «Massen-Einladungen sind verboten», то есть массовые приглашения запрещены. Про envs.net и mozilla.org данных нет **[не подтверждено]** | **[док]** [tchncs.de/matrix](https://tchncs.de/matrix) |

**Вывод.**
- На публичном сервере бот зависит от чужой эксплуатации и чужих лимитов. Сбой matrix.org в 2025 длился сутки.
- Главное: matrix.org режется в РФ. Запасной канал нужен ровно в тот момент, когда у пользователя нет рабочего VPN, а без VPN matrix.org из РФ в основном недоступен.

**Пользователи же могут сидеть где угодно**, в том числе на matrix.org: федерация с нашим сервером работает. Риск блокировки при этом на стороне пользователя, а не бота.

### 1.2. Свой: continuwuity, Synapse, Dendrite

| | continuwuity v26.9.1 | Synapse v1.163.0 | Dendrite |
|---|---|---|---|
| Статус | Активный, релизы раз в 2–4 недели. Это «official community continuation of conduwuit», сам conduwuit заархивирован. Есть конкурирующий форк tuwunel **[док]** [README](https://forgejo.ellis.link/continuwuation/continuwuity/src/tag/v26.9.1/README.md) | Эталонная реализация от Element **[док]** | «maintenance mode, only security fixes», последний релиз v0.15.2 (2025-08) **[док]** [README](https://github.com/element-hq/dendrite) |
| Лицензия | Apache-2.0 **[док]** | AGPL-3.0 или коммерческая **[док]** | AGPL-3.0 |
| БД | RocksDB, встроенная, один каталог **[вживую]** | PostgreSQL обязателен: «SQLite should not be used in a production server» **[док]** [installation.md](https://github.com/element-hq/synapse/blob/v1.163.0/docs/setup/installation.md) | — |
| RAM в простое | **~53–57 МиБ**, CPU 0,00 % **[вживую]**. После прогона теста ~140–210 МиБ, через минуту ~139 МиБ (`docker stats`, включая кэш). Каталог данных после теста — 79 МБ | **~120 МиБ** на SQLite без Postgres, CPU ~0,7 % **[вживую]**; плюс отдельный Postgres. Документация: «At least 1GB… to join large public rooms» **[док]** | — |
| Образ | 105 МБ | 331 МБ | — |
| Лимиты клиента | Опций `rc_*` нет **[док]** [example.toml](https://forgejo.ellis.link/continuwuation/continuwuity/src/tag/v26.9.1/conduwuit-example.toml); 429 на бёрсте не получено **[вживую]** | `rc_message` 0,2/с burst 10; `rc_invites.per_user` 0,003/с burst 5; `rc_room_creation` 0,016/с burst 10. Отключение: admin API `override_ratelimit` **[док]** [ratelimiting.py](https://github.com/element-hq/synapse/blob/v1.163.0/synapse/config/ratelimiting.py) | — |
| Регистрация | `allow_registration` + `registration_token`. Первый аккаунт создаётся по одноразовому токену из лога первого старта; статический токен из конфига начинает работать только после этого **[вживую]** | `registration_shared_secret` / admin API **[док]** | — |

**Почему continuwuity.**
- В 2–4 раза меньше памяти, один контейнер без Postgres.
- Бэкап — каталог RocksDB плюс `media/`; есть и встроенный онлайн-бэкап (`database_backup_path`) **[док]**.
- Для наших масштабов (сотни подписчиков, одна публичная комната) зрелость Synapse не нужна.

**Риски continuwuity.**
- Молодой проект после форка: conduwuit заархивирован, есть спор с tuwunel о «наследнике».
- Миграции БД между conduwuit-семейством и Synapse нет. Смена реализации означает новый сервер: пользователей перепривязывать, комнату создавать заново.
- Смягчение: зафиксировать версию образа и не включать нестабильные функции.

### 1.3. Федерация и правило «только 443» (ADR 0005)

**[вживую]:**
- Каждому серверу задан `[global.well_known] server = "matrix.hsN.test:443"`. Сервер сам отдаёт `/.well-known/matrix/server`, а nginx на 443 по SNI проксирует `hsN.test` и `matrix.hsN.test` на :8008.
- В логе nginx видны только федеративные запросы на 443: `PUT /_matrix/federation/v1/send`, `v2/invite`, `make_join`/`send_join`, `GET /_matrix/key/v2/server`, `federation/v1/media`.
- Приглашение через федерацию дошло за ~40 мс, сообщение — за ~50 мс. Порт 8448 не нужен.

**[док]:**
- Порядок поиска сервера по [спеке](https://spec.matrix.org/v1.19/server-server-api/#resolving-server-names): IP-литерал → явный порт → `/.well-known/matrix/server` → SRV → 8448.
- Сертификат должен быть выписан на делегированное имя, SNI отправляется. Ответ `.well-known` кэшируется на 24 ч.
- Для нас это значит: в nginx-фронте нужен ещё один `server_name matrix.<домен>` с терминированием TLS, как у домена панели.

### 1.4. Где держать: `real` или отдельный хост

- **Ресурсы не мешают поставить на `real`:** +55–200 МБ RAM.
- **Надёжность против `real`.**
  - Если homeserver на `real`, при падении `real` молчат и tg-бот, и Matrix-бот, и Matrix-комната platform status. Объявить «панель недоступна» в Matrix будет некому. По решению [#270](https://github.com/SBKubric/sane-3x-ui/issues/270#issuecomment-6280100109) при `PANEL_DOWN` mon-server постит в tg-канал сам; в Matrix он сможет так же, только если homeserver живёт не на `real`.
  - Публичный server_name выдаёт адрес хоста: его видит любой участник федерации. Matrix на `real` светит адрес главного сервера цепочки.
- **Рекомендация:** отдельный небольшой VPS вне РФ, например рядом с mon-server. Свой nginx на 443 и собственный домен. Бот в панели ходит к нему по клиентскому API, как обычный клиент.
- **Доступность из РФ — вывод, не проверено.** Блокировка Matrix в РФ точечная, по доменам matrix.org. Свой домен по умолчанию не режется. Если начнут резать, домен можно отдать через RU-edge (SNI passthrough), так же как страницы подписки.

### 1.5. Что ломается у homeserver и как переживается

| Сбой | Поведение | Источник |
|---|---|---|
| Рестарт homeserver | Токен бота и данные на месте, `whoami` работает сразу после рестарта | **[вживую]** |
| Перенос на новую машину | Остановили, скопировали каталог данных, подняли новый контейнер. Токен, комнаты и ключ подписи сервера на месте | **[вживую]** |
| Потеря каталога данных без бэкапа | Новый ключ подписи и пустая база: аккаунтов и комнат нет. Пользователей перепривязывать, status-комнату создавать заново, удалённые серверы какое-то время держат в кэше старый ключ | **[док]**, следствие спеки |
| Homeserver недоступен | `DefaultSyncer.OnFailedSync` повторяет sync каждые 10 с; при `M_UNKNOWN_TOKEN` останавливается. Отправка падает после `DefaultHTTPRetries` попыток, поэтому нужна своя очередь исходящих уведомлений | **[док]** [sync.go#L195-L200](https://github.com/mautrix/go/blob/v0.32.0/sync.go#L195-L200) |
| Сервер «звонит домой» | При старте continuwuity проверяет анонсы на continuwuity.org (`allow_announcements_check`); в лаборатории это давало ошибку TLS. В проде лучше выключить | **[вживую]** + **[док]** |

## 2. Библиотека бота: mautrix-go

### 2.1. Лицензия, cgo, olm

- **Лицензия.** MPL-2.0 ([LICENSE](https://github.com/mautrix/go/blob/v0.32.0/LICENSE)). Требование `go 1.26.0` совпадает с тулчейном панели **[док]**.
- **По умолчанию — libolm через cgo.** [`registerlibolm.go`](https://github.com/mautrix/go/blob/v0.32.0/crypto/registerlibolm.go) стоит под `//go:build !goolm` и собирается с `#cgo LDFLAGS: -lolm -lstdc++` **[док]**. Без тега сборка падает: `olm/olm.h: No such file or directory` **[вживую]**.
- **Чистый Go — тег `-tags goolm`.** Все E2EE-сценарии ниже прогнаны на goolm **[вживую]**.
- **libolm брать не стоит.** Он deprecated с 2024-08: CVE-2024-45191/2/3, «not resilient to timing attacks» **[док]** [blog](https://matrix.org/blog/2024/08/libolm-deprecation/).
- **Хранилище — cgo.** `cryptohelper` с путём к файлу открывает диалект `sqlite3-fk-wal`, а его регистрирует только `//go:build cgo` с `mattn/go-sqlite3` **[док]** [cryptohelper.go#L82](https://github.com/mautrix/go/blob/v0.32.0/crypto/cryptohelper/cryptohelper.go#L82), [go-util register.go](https://github.com/mautrix/go-util/blob/v0.11.0/dbutil/litestream/register.go).
  - Панель уже собирается с CGO и тем же `mattn/go-sqlite3` (через `gorm.io/driver/sqlite`), так что лишних зависимостей нет.
  - Сборка с `CGO_ENABLED=0 -tags goolm` компилируется, но тогда sqlite-хранилища нет **[вживую]**.
- **Память.** Весь процесс проверки — 6 клиентов, из них 2 с E2EE и sqlite — занимает RSS 23 МБ (VmHWM) **[вживую]**. Бот внутри панели добавит единицы МБ.

### 2.2. Хранилище ключей и токена

`cryptohelper` хранит в одном sqlite-файле:
- Olm-аккаунт: ключи устройства, one-time keys;
- megolm-сессии;
- state store;
- device ID.

**Пароль бота нужен всегда:**
- при логине (`LoginAs`) — ID устройства берётся из хранилища;
- для UIA при загрузке ключей кросс-подписи (повторной) и при удалении устройств.

**Решения для панели (рекомендация):**
- Хранилище — **отдельный файл** рядом с `x-ui.db`, а не таблицы в основной БД. Тогда `importDB` и бэкап панели не смешиваются с криптографией.
- Pickle key и **recovery key** держать в настройках или в vault orchestrator, а не только в файле.

### 2.3. Что ломается и как переживается [вживую]

| # | Сценарий | Результат |
|---|---|---|
| E1–E2 | Пользователь создаёт **зашифрованный** DM (как Element X), бот (goolm) автоматически принимает приглашение | Расшифровка работает в обе стороны. Зашифрованный `.conf` (`EncryptedFileInfo`) пользователь скачал и расшифровал. Доверие к устройству бота у пользователя — `unverified` |
| E3 | Бот: `GenerateAndVerifyWithRecoveryKey()` — ключи кросс-подписи, SSSS, recovery key, подпись своего устройства | Первая загрузка на continuwuity прошла **без UIA**. У пользователя доверие стало `cross-signed-tofu` |
| E4 | Рестарт из того же хранилища | Тот же device ID, Init за 36 мс, расшифровка в обе стороны, по-прежнему `cross-signed-tofu` |
| E5 | **Хранилище потеряно**, старые токен и устройство | `Init` падает: `olm account is not marked as shared, but there are keys on the server` ([cryptohelper.go#L251-L278](https://github.com/mautrix/go/blob/v0.32.0/crypto/cryptohelper/cryptohelper.go#L251-L278)). Сам бот не восстановится |
| E6 | Пустое хранилище + новый логин по паролю → **новое устройство** → `VerifyWithRecoveryKey(rk)` → удаление старых устройств (UIA паролем) | Новое устройство сразу `cross-signed-tofu`, предупреждений нет. Расшифровка в обе стороны |
| E7 | Хранилище **и** recovery key потеряны → новые ключи кросс-подписи (**сброс identity**) | У пользователя `cross-signed-untrusted` (мастер-ключ сменился). В Element это предупреждение «identity was reset». Расшифровка при этом работает |
| E8 | Бот **без E2EE** в зашифрованном DM пользователя | Бот получает только `m.room.encrypted`, команду прочитать не может. Свой открытый текст отправить может, пользователь его получает с пометкой «не зашифровано» |
| E9 | UTD | На живых событиях — **0** в каждом сценарии. На **истории** у свежего устройства бота — 8: это события до его появления, ключей к ним у него нет и быть не должно |

**Правила для реализации:**
1. При ошибке E5 (`not marked as shared` / `mismatching identity key`) бот сам делает новый логин в пустое хранилище. Затем `VerifyWithRecoveryKey`, затем удаляет прочие свои устройства, иначе клиенты продолжают шифровать для мёртвых устройств. В E6 удалено 2 устройства.
2. **Никогда не генерировать ключи кросс-подписи повторно**, если они уже есть на сервере (E7). Без recovery key бот должен остановиться с ошибкой и ждать админа.
3. Игнорировать события старше старта устройства (`evt.Timestamp` < времени логина). Это не UTD, а история.
4. ⚠️ `GenerateAndUploadCrossSigningKeysWithPassword` отправляет UIA со старым полем `user`; continuwuity отвечает `400 M_BAD_JSON: missing field identifier`. Нужен свой UIA-колбэк с `identifier: {type: m.id.user, user}`. Так сделано в `pwAuth` в скрипте **[вживую]**, источник: [cross_sign_ssss.go#L62-L73](https://github.com/mautrix/go/blob/v0.32.0/crypto/cross_sign_ssss.go#L62-L73).
5. Кому бот раздаёт ключи: `SendKeysMinTrust` по умолчанию `Unset`, то есть всем устройствам пользователя, включая неверифицированные. Для бота это правильно, подписчики устройства не верифицируют **[док]** [machine.go#L139](https://github.com/mautrix/go/blob/v0.32.0/crypto/machine.go#L139).

### 2.4. Нужна ли E2EE и предупреждения в клиентах

- **DM в Element X зашифрованы по умолчанию:** `isEncryped = !sessionEnterpriseService.isEncryptionDisabledByHomeserver()` **[док]** [DefaultStartDMAction.kt#L41](https://github.com/element-hq/element-x-android/blob/v26.10.0/features/startchat/impl/src/main/kotlin/io/element/android/features/startchat/impl/DefaultStartDMAction.kt#L41).
- **Spec v1.19:** клиентам «SHOULD NOT send … room keys … to non-cross-signed devices», а сообщения с таких устройств «SHOULD NOT be displayed» **[док]** [E2EE module](https://spec.matrix.org/v1.19/client-server-api/#end-to-end-encryption).
- **Element перенёс включение** с апреля на «October 2026», потому что боты и мосты не готовы **[док]** [TWIM 2026-04-10](https://matrix.org/blog/2026/04/10/this-week-in-matrix-2026-04-10/), [blog 2025-11](https://matrix.org/blog/2025/11/exclude-insecure-devices/). В Element X Android v26.10.0 флаг `OnlySignedDeviceIsolationMode` пока `defaultValue = { false }` **[док]** [FeatureFlags.kt](https://github.com/element-hq/element-x-android/blob/v26.10.0/libraries/featureflag/api/src/main/kotlin/io/element/android/libraries/featureflag/api/FeatureFlags.kt).
- **Следствие:** бот с неподписанным устройством скоро перестанет и читать, и быть видимым в зашифрованных комнатах. Кросс-подпись (E3) обязательна, если E2EE включена.
- **Вариант без E2EE** работает, только если комнату всегда создаёт бот: незашифрованный DM по приглашению.
  - Если пользователь сам открыл DM к боту, бот его не прочтёт (E8).
  - `.conf` с приватным ключом пойдёт открытым текстом на homeserver пользователя.
  - Сбоев с ключами меньше, но привязка возможна только через приглашение, а его пользователь может заблокировать (§3).
  - Поэтому **рекомендация — E2EE включить**, сбои закрыты правилами §2.3.

## 3. DM с пользователем по MXID

| Вопрос | Ответ |
|---|---|
| Бот приглашает `@user:server` | `createRoom{preset: trusted_private_chat, is_direct: true, invite: [user]}`. У пользователя приглашение с `is_direct=true` появляется в `/sync` через ~60 мс (тот же сервер) и ~40 мс (федерация) **[вживую]** |
| Подтверждает ли принятие владение MXID | Да: вступить в комнату от имени MXID может только его владелец (или админ его homeserver). Но MXID может ввести в tg кто угодно, и тогда приглашение от VPN-бота получит **чужой** человек. Это утечка факта подписки и спам |
| **Рекомендуемый поток** | tg-бот выдаёт одноразовый код и ссылку `https://matrix.to/#/@bot:<домен>`. Пользователь сам открывает DM и пишет `/link КОД`. Бот автоматически принимает приглашение (~110 мс) и получает код; `sender = @u2:hs1.test` **[вживую]** |
| Почему так надёжнее | Не зависит от фильтров приглашений. Ничего не уходит на чужой MXID. Владение доказывает homeserver отправителя: событие подписано его сервером |
| Видят ли клиенты приглашения от незнакомых | Spec v1.18 «invite permission» (`m.invite_permission_config`, MSC4155/MSC4380). По умолчанию приглашения принимаются **[док]** [spec](https://spec.matrix.org/v1.18/client-server-api/#invite-permission). Element X показывает «Принять / Отклонить / Отклонить и заблокировать» **[док]**. Прячет ли Element X приглашения от незнакомых без общих комнат — **[не подтверждено]** |
| Как работает блокировка (continuwuity) | `default_action: block` → `400 M_INVITE_BLOCKED`. `default_action: block` плюс бот в `allowed_users` всё равно блокирует: `block` важнее allow-листа. `blocked_servers: ["*"]` плюс бот в `allowed_users` пропускает. `ignored_users: [bot]` → **200 OK, но приглашение молча не доходит** **[вживую]**. Synapse поддерживает это с 1.132/1.149 **[док]** |
| Федерация | Бот на hs1 и пользователь на hs2: приглашение, вступление, сообщения и медиа работают через 443 **[вживую]** |
| Стабильный ID и смена homeserver | Идентификатор — MXID целиком, `@local:server`. Переезда аккаунта между серверами в Matrix нет: новый сервер — новый MXID и новая привязка через tg, что совпадает с правилом карты «повторная привязка заменяет прежнюю». Отпечаток ключа с MXID не связан |

## 4. Канал platform status как read-only комната

**[вживую]:**
- `createRoom{preset: public_chat, visibility: public, room_alias_name: status, power_level_content_override: {users: {bot: 100}, events_default: 50, events: {m.reaction: 0}}}`, `history_visibility: world_readable`, `guest_access: forbidden`.
- Подписчики вступают по alias, удалённый пользователь — через федерацию (~40 мс). Ссылка: `https://matrix.to/#/%23status:<домен>`.
- Подписчик пишет → `403 M_FORBIDDEN`. Пост бота — одно событие независимо от N; на удалённый сервер уходит одна транзакция `PUT /federation/v1/send` на сервер, а не на участника (видно в логе nginx).
- Реакции в примере разрешены (`m.reaction: 0`). Для канала статуса их лучше закрыть (50), иначе это канал для флуда.

⚠️ **Приватность участников:**
- Подписчик `u2` через `/joined_members` видит всех: `@u1:hs1.test, @u2:hs1.test, @bot:hs1.test, @r1:hs2.test` **[вживую]**.
- Тот, кто **не вступал** (`u3`), читает `/messages` (8 событий) и `/members` (4 участника), потому что комната `world_readable` **[вживую]**.
- Скрыть список участников спека не позволяет. `history_visibility` (`world_readable / shared / invited / joined`) управляет только чтением истории **[док]** [spec](https://spec.matrix.org/v1.19/client-server-api/#room-history-visibility).

**Альтернативы (решать владельцу):**
1. **Принять как есть.** Комната `shared`, а не `world_readable`, чтобы не-участники хотя бы не видели список. В описании комнаты предупредить, что участники видны. Подписчик может вступить с отдельного анонимного аккаунта.
2. **Не вступать, а смотреть.** Комната `world_readable`, подписчики её не вступают, а открывают превью. Push и непрочитанных при этом нет. Предпросмотр без вступления есть в Element Web; в Element X — **[не подтверждено]**, судя по исходникам, только в каталоге комнат.
3. **Рассылать статус в личные DM.** Участников не видно, но N сообщений вместо одного, и это противоречит решению карты: «о статусе платформы бот никогда не пишет в личку».
4. **Только Telegram-канал** плюс страница статуса на showcase. В Matrix — лишь личные уведомления.

## 5. Кабинет: разметка, команды, файлы

**HTML.**
- `formatted_body` с `<b> <i> <code> <pre><code> <a href> <br>` дошёл байт-в-байт: сервер не фильтрует, рендер решает клиент **[вживую]**.
- Рекомендованный список тегов спеки включает `b i u s code pre a br p ul ol li blockquote h1–h6 table details summary span` и другие **[док]** [spec m.room.message](https://spec.matrix.org/v1.19/client-server-api/#mroommessage-msgtypes).
- Набор Telegram-HTML кабинета (`<b>,<i>,<code>,<pre>,<a>`) переносится без потерь.

**Команды и кнопки.**
- Кнопок в спеке нет **[док]**.
- Опросы (MSC3381, `m.poll`) реализованы в Element X (модуль `features/poll`), но в текст спеки не внесены (`spec-pr-missing`) **[док]**.
- Кликабельны ли `/команды` в Element X и FluffyChat — **[не подтверждено]**. Безопаснее считать, что нет: пользователь печатает команду сам.
- Короткие команды (`/u1`, `/help`) и ссылки `matrix.to` кликабельны как обычные ссылки.
- Реакции-эмодзи как «кнопки» — распространённый у ботов приём; в клиентах не проверено.

**Файлы и медиа [вживую]:**
- `.conf` уходит через `UploadBytesWithName` → `m.file` с `filename`; пользователь скачал через `/_matrix/client/v1/media/download` (authenticated media), содержимое совпало.
- PNG уходит как `m.image` с `w/h`. Удалённый пользователь скачал его через свой homeserver (федеративное authenticated media) за 54 мс.
- В E2EE-комнате вложение шифруется (`attachment.NewEncryptedFile` → `File: EncryptedFileInfo`), пользователь расшифровал.
- Без токена: `GET /_matrix/client/v1/media/download` → **401**, legacy `/_matrix/media/v3/download` → **403**. Ссылку на файл нельзя отдать «наружу»: только как событие в комнате.
- Это соответствует спеке v1.11+ (authenticated media, MSC3916): matrix.org заморозил неаутентифицированные медиа 2024-09-04 **[док]** [blog](https://matrix.org/blog/2024/06/26/sunsetting-unauthenticated-media/).
- Лимит размера в continuwuity: `max_request_size` 20 МБ **[док]**. Наши файлы — килобайты.

## 6. Эксплуатация

**Лимиты отправки.**
- **continuwuity:** 200 сообщений подряд за ~0,5 с, `429` — 0; 30 пар `createRoom` + `invite` за ~0,4 с, `429` — 0 **[вживую]**. Клиентских ограничителей в конфиге нет **[док]**.
- **Synapse:** `rc_message` 0,2/с burst 10 **[док]**. Рассылка на 500 человек по умолчанию заняла бы ~40 мин, поэтому боту ставят `override_ratelimit` 0/0.
- **matrix.org** публичных цифр не даёт; ToS запрещает «bulk communications».

**Темп рассылки.** Даже без лимитов шлём 5–10 сообщений/с и учитываем `429` с `Retry-After`: mautrix делает это сам при `DefaultHTTPRetries > 0` **[док]** [client.go#L923](https://github.com/mautrix/go/blob/v0.32.0/client.go#L923). Пользователям на чужих серверах (matrix.org) события идут через федерацию; входящие лимиты на их стороне нам неизвестны **[не подтверждено]**.

**Ресурсы.** continuwuity: ~55 МБ RAM в простое, CPU ≈ 0 **[вживую]**. Бот: единицы МБ поверх панели **[вживую]**, с оговоркой из §2.1.

**Push у пользователей в РФ:**
- **Element X Android:** Firebase-pusher `https://matrix.org/_matrix/push/v1/notify` (sygnal matrix.org → FCM). UnifiedPush по умолчанию идёт через `matrix.gateway.unifiedpush.org`. Оба провайдера вкомпилированы **[док]** [FirebaseConfig.kt](https://github.com/element-hq/element-x-android/blob/v26.10.0/libraries/pushproviders/firebase/src/main/kotlin/io/element/android/libraries/pushproviders/firebase/FirebaseConfig.kt), [UnifiedPushConfig.kt](https://github.com/element-hq/element-x-android/blob/v26.10.0/libraries/pushproviders/unifiedpush/src/main/kotlin/io/element/android/libraries/pushproviders/unifiedpush/UnifiedPushConfig.kt).
- **Element X iOS:** шлюз `https://matrix.org` → APNs **[док]**.
- **Наш homeserver** ходит к шлюзу matrix.org из-за рубежа; это не блокируется.
- **Узкое место — FCM/APNs внутри РФ:**
  - OONI: `fcm.googleapis.com` — аномалии ~1–2 % **[док]**;
  - сообщения СМИ о сбоях push в РФ в июне 2026 — **[не подтверждено]** ([anti-malware.ru](https://www.anti-malware.ru/news/2026-06-19-111332/50420)).
- **Фонового поллинга** в Element X Android нет: только FCM и UnifiedPush **[док]**.
- **Итог:** уведомление «кончается подписка» в Matrix — **best effort**. Если push не дошёл, пользователь увидит его при открытии приложения. Основным каналом остаётся Telegram.
- Свой ntfy для UnifiedPush возможен, но это ещё один сервис и отдельное приложение у пользователя.

## Что не проверено

- Как Element X, Element Web и FluffyChat **рендерят** HTML кабинета, кликабельны ли `/команды`, как выглядят плашки «не зашифровано» и «identity reset». Нужна ручная проверка на телефоне.
- Скрывает ли Element X приглашения от незнакомых и можно ли в нём смотреть `world_readable` комнату без вступления.
- Лимиты matrix.org и других публичных серверов на входящую федерацию.
- Реальная доставка push в РФ (FCM/APNs/UnifiedPush) и доступность нашего домена из РФ без VPN.
- Поведение под нагрузкой (сотни DM) и долгий аптайм continuwuity.
