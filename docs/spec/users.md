# Пользователи: модель, миграция, проверки, API

Спека тикета [#168](https://github.com/SBKubric/sane-3x-ui/issues/168) (родитель [#167](https://github.com/SBKubric/sane-3x-ui/issues/167), раздел «Модель»). Термины по [CONTEXT.md](../../CONTEXT.md): **пользователь**, **клиент**, **подписка**, **технический пользователь**, probe account. Совместимость с upstream — [ADR 0002](../adr/0002-additive-upstream-compatibility.md). Бот ([#169](https://github.com/SBKubric/sane-3x-ui/issues/169)) зовёт `SubUserService` напрямую, страница панели ([#170](https://github.com/SBKubric/sane-3x-ui/issues/170)) — через HTTP API §6.

## 1. Модель

Новая таблица `sub_users`, модель `database/model/sub_user.go` (`SubUser`). Имя `users` занято upstream-таблицей администраторов панели (`model.User`), а upstream-модели не трогаем.

| поле | тип | назначение |
|---|---|---|
| `sub_id` | string, PK | подписка пользователя; у технических — зарезервированный ключ |
| `name` | string, unique | имя; уникально без учёта регистра (проверяет сервис) |
| `tg_id` | int64 | Telegram пользователя; новые клиенты получают его |
| `comment` | string | |
| `created_at`, `updated_at` | int64, мс | служебные |

- **Клиент** принадлежит пользователю через subId: у xray-клиента — поле `subId` в `settings.clients` inbound'а, у AWG/WG-клиента — связка `tunnel_client_subs` ([tunnel-subscription.md](tunnel-subscription.md) §2, модель `database/model/tunnel_subscription.go`, FK на `tunnel_clients.uuid` с `ON DELETE CASCADE`). Своих строк клиентов у пользователя нет: принадлежность каждый раз читается с клиентов, поэтому клиент, записанный путём, который о пользователях не знает (сохранение inbound'а целиком, импорт, копирование клиентов), всё равно попадает к нужному пользователю.
- **Технические пользователи** хранятся под ключами `@robot` и `@monitoring`. Ключ с `@` не бывает subId'ом: проверки §4 его отвергают.
  - `robot` — без подписки. Ему принадлежат xray-клиенты с пустым subId и AWG/WG-клиенты без связки; их данные не меняются.
  - `monitoring` — probe accounts (имя с префиксом `probe-`) и всё, что несёт текущий probe subId (`monProbeSubId`). Probe subId появляется при первом ensure и сбрасывается при удалении probe set, поэтому он не ключ строки, а читается из настройки: смена subId не требует переписывать таблицу. AWG probe-пиры привязываются к probe subId при каждом ensure (`linkTunnelProbes`).
- MTProto-клиенты в пользователей не входят: у них своя таблица, в подписке их нет.

## 2. Технические пользователи

- Их нельзя удалить, включить/выключить, добавить им протокол, назначить им клиента: все операции записи `SubUserService` отвечают «technical user and cannot be changed by hand».
- Имена `robot`, `monitoring` (без учёта регистра) и любое имя с префиксом `probe-` не может занять ни пользователь, ни клиент.
- В `monitoring` вручную ничего не добавить: probe-имена и probe subId отвергают guards probe accounts и проверки §4; клиентов туда кладёт только `MonitoringService.EnsureProbeSet`.

## 3. Миграция

`SubUserService.Sync`, post-migrate hook (`database.RegisterPostMigrate`) — выполняется при каждом старте.

1. Создаёт `robot` и `monitoring`, если их нет.
2. Обходит клиентов по порядку: xray-клиенты по возрастанию id inbound'а в порядке `settings.clients`, затем AWG, затем WG по id.
3. Для каждого subId без пользователя создаёт пользователя с именем по email первого клиента; при совпадении (без учёта регистра) или зарезервированном имени — `-2`, `-3`… Пустой email — имя `user`.
4. Пропускает клиентов `robot` и `monitoring` (§1): probe subId пользователем не становится.

Существующих клиентов миграция не читает на запись: не переименовывает, subId не выдаёт, дубликаты имён старых данных оставляет как есть. Повторный запуск ничего не меняет, переименованного вручную пользователя обратно не переименовывает.

Тот же `Sync` выполняется в начале каждого вызова `SubUserService`. Так соблюдается инвариант «у каждого subId есть пользователь» для клиентов, добавленных через панель или API с новым subId: пользователь с именем первого клиента появляется до того, как его кто-то увидит. Отдельного hunk'а для автосоздания в путях добавления нет.

## 4. Проверки

Одна функция — `SubUserService.ValidateClientWrites(ClientWrite…)`. Правила действуют только на новые записи:

- **Имя клиента уникально по всем протоколам** — среди email'ов xray-клиентов и туннельных клиентов, без учёта регистра, в том числе внутри одного запроса. Проверяется только новое имя: клиент со старым дублирующимся именем редактируется, пока имя не меняют. Ошибка называет занявшего: `client name "ivan-awg" is already taken: AmneziaWG client of user petr`.
- **Имена технических пользователей** не бывают именами клиентов: `name "robot" is reserved for a technical user`.
- **subId принадлежит одному пользователю**: ключ технического пользователя не subId (`subId "@robot" is reserved…`); probe subId — `monitoring` (`subId "…" belongs to technical user monitoring`); запись, которая называет пользователя (бот, API пользователей), не может взять чужой subId: `subId "s1" belongs to user petr`. Запись с существующим subId без имени пользователя — так панель добавляет протокол в существующую подписку — разрешена: клиент становится клиентом этого пользователя.

Где вызывается:

| путь | место |
|---|---|
| добавление xray-клиента (`AddInboundClient`, в т. ч. бот, копирование клиентов) | внутри форкового guard'а `rejectNewProbeClients` (`monitoring_probe.go`) — в `inbound.go` hunk'а для add нет |
| обновление xray-клиента (`UpdateInboundClient`) | один hunk в `inbound.go` рядом с форковыми probe-guard'ами |
| AWG/WG API `client/add`, `client/update/:id`, `client/updateByUuid/:uuid` | hunk'и tunnel subscription §4 в `tunnel_controller.go`, помощники в `tunnel_controller_sub.go` (`ValidateTunnelClientWrite`) |
| `SubUserService` | до первой записи (`planClients`) |

Сохранение inbound'а целиком (`AddInbound`/`UpdateInbound`) проверок пользователей не проходит — как и в родителе; принадлежность таких клиентов подхватывает `Sync`.

## 5. `SubUserService`

`web/service/sub_user*.go`. Пользователь адресуется ключом: subId, у технических — `@robot`/`@monitoring`.

| метод | что делает |
|---|---|
| `List()` | все пользователи, технические первыми, затем по имени |
| `Get(key)` | пользователь с клиентами |
| `Find(q)` | по имени пользователя, subId или имени клиента; результат один, иначе ошибка (не найден / имя клиента у двух пользователей) |
| `Inbounds()` | inbound'ы, где пользователю можно создать клиента: vless, vmess, trojan, shadowsocks, amneziawg |
| `Create(SubUserCreate)` | пользователь и клиент в каждом из `inboundIds`; subId генерируется (16 символов), если не задан |
| `AddProtocol(key, inboundId, linkExisting)` | клиент ещё в одном inbound'е; параметры копируются с xray-клиента пользователя, иначе с AWG-клиента |
| `RemoveProtocol(key, inboundId)` | удаляет клиента пользователя в inbound'е; после последнего пользователь остаётся со своим subId |
| `SetEnable(key, enable)` | включает/выключает всех клиентов пользователя |
| `Delete(key)` | удаляет клиентов и пользователя |
| `Assign(key, clientName)` | отдаёт клиента `robot` пользователю: xray-клиенту пишет subId, AWG/WG-клиента привязывает; имя не меняется |
| `ValidateClientWrites`, `ValidateTunnelClientWrite` | проверки §4 |

- **Имя клиента** — `<пользователь>-<remark inbound'а>` (`SubUserClientName`): буквы, цифры и `. _ @` сохраняются, любая другая последовательность символов — один `-`, края `-`/`.` срезаются. Пустой remark заменяется протоколом.
- **Лимиты** (`totalGB` в байтах, `expiryTime` в мс, отрицательный — отсчёт с первого подключения, `limitIp`, `reset`) у каждого клиента свои: 50 ГБ на VLESS и 50 ГБ на AWG дают до 100 ГБ. В представлении пользователя `total` — сумма лимитов (0, если хоть один безлимитный), `expiryTime` — общий срок (0, если сроки разные), как `Subscription-Userinfo` у `/sub`.
- **Атомарность.** Всё проверяется до первой записи (inbound существует и поддерживается, у пользователя там ещё нет клиента, имена свободны). Затем строка пользователя, затем клиенты по порядку: xray — через `AddInboundClient`, AWG — `AwgService.AddClient` + связка. Если клиент не создался, уже созданные откатываются в обратном порядке: settings xray-inbound'а восстанавливаются из снимка, строки трафика и IP удаляются, xray-пользователь снимается с работающего xray; AWG-пир удаляется (связка уходит каскадом); строка пользователя удаляется.
- **AWG-клиент с тем же именем уже есть:**
  - без связки — ошибка `SubUserConflict{code: "awg_linkable", client}`; бот или страница спрашивают оператора и повторяют запрос с `linkExisting: true`, тогда существующий клиент привязывается вместо создания нового;
  - привязан к другому пользователю — ошибка проверки §4, ничего не создаётся.
- **Пустой inbound.** Панель не оставляет xray-inbound без клиентов (`no client remained in Inbound` в upstream). `RemoveProtocol` и `Delete` проверяют это заранее и отказывают с именем inbound'а, ничего не удалив.
- Если нужен перезапуск xray (клиент не встал через API у включённого inbound'а), сервис сам ставит флаг `SetToNeedRestart`.
- Все записи сериализованы мьютексом (`subUserMu`); проверки §4 его не берут, поэтому операции сервиса проходят через guarded-пути панели.

## 6. HTTP API

`web/controller/sub_user_controller.go`, группа `/panel/api/users` (одна строка в `APIController.initRouter`), за сессией панели: без неё — 404, как у остального `/panel/api`. Ответ — обычный конверт `{success, msg, obj}`; при отказе `msg` — сообщение сервиса, при `awg_linkable` в `obj` лежит `{code, client}`. Тела разбираются строго: неизвестное поле — отказ.

| метод и путь | тело | ответ `obj` |
|---|---|---|
| `GET /list` | — | `[]SubUserView` |
| `GET /inbounds` | — | `[]{id, remark, protocol, enable}` |
| `GET /get/:subId` | — | `SubUserView` |
| `GET /find?q=` | — | `SubUserView` |
| `POST /create` | `{name, subId?, tgId, comment, inboundIds[], totalGB, expiryTime, limitIp, reset, linkExisting}` | `SubUserView` |
| `POST /addProtocol/:subId` | `{inboundId, linkExisting}` | `SubUserView` |
| `POST /removeProtocol/:subId` | `{inboundId}` | `SubUserView` |
| `POST /enable/:subId` | `{enable}` | `SubUserView` |
| `POST /del/:subId` | — | — |
| `POST /assign/:subId` | `{client}` — имя клиента `robot` | `SubUserView` |

`SubUserView` — `{subId, name, tgId, comment, createdAt, updatedAt, technical, enable, up, down, allTime, total, expiryTime, clients[]}`; `enable` — включён хоть один клиент. Клиент — `{kind: xray|awg|wg, inboundId, inboundRemark, protocol, name, key, subId, enable, up, down, allTime, totalGB, expiryTime, limitIp, tgId, reset, lastOnline}`; `key` — идентификатор клиента в его собственном API (id/password/email/auth у xray, uuid у туннеля).

## 7. Связь с tunnel subscription

Из [tunnel-subscription.md](tunnel-subscription.md) здесь реализованы модель связки (§2), её половина сервиса (`Set`/`Clear`/`SubIdsByUUIDs`, §3) и `subId` в AWG/WG API (§4). Маршрута `/tun/<subId>`, `Userinfo`, TTL-кэша, настроек, UI панели и proxy front (§3 остальное, §5–§8) пока нет: пока их нет, AWG-клиент пользователя принадлежит его подписке в панели и боте, но в `/sub` и `/tun` не отдаётся.

## 8. Тесты

- `database/sub_user_test.go`, `database/tunnel_subscription_test.go` — уникальность имени и subId в схеме, каскад связки.
- `web/service/sub_user_migrate_test.go` — миграция: коллизии, пропуск probe subId, `robot`/`monitoring`, идемпотентность, клиенты не меняются.
- `web/service/sub_user_validate_test.go` — каждое правило §4, в том числе через `InboundService`.
- `web/service/sub_user_ops_test.go` — создание с несколькими протоколами, откат, add/remove protocol (последний клиент оставляет пользователя), каскад вкл/выкл, удаление, назначение из `robot`, привязка AWG без связки, поиск, технические пользователи.
- `web/service/monitoring_users_test.go` — AWG probe-пиры привязаны к probe subId и принадлежат `monitoring`.
- `web/controller/tunnel_controller_sub_test.go`, `web/controller/sub_user_controller_test.go` — AWG/WG API с subId и API пользователей.
- `e2e/tests/users-api.spec.ts` — API пользователей через request context Playwright.
- `web/controller/sub_user_page_test.go` — маршрут страницы §9; сама страница — в `web/template_test.go` (разбор, выполнение, ключи переводов).
- `e2e/tests/users-page.spec.ts` — страница §9 через UI: создание с двумя inbound'ами, ➖/➕ протокол, выключение, назначение из `robot`, удаление, отказ на последнем клиенте xray-inbound'а, подсказка и отказ проверки в модалке клиента.

## 9. Страница панели

Тикет [#170](https://github.com/SBKubric/sane-3x-ui/issues/170). Страница `/panel/users` (`web/html/users.html`, обработчик в `web/controller/sub_user_page.go`, одна строка в `XUIController.initRouter`), пункт меню «Пользователи» после «Мониторинга» (один hunk в `aSidebar.html`). Своих данных у страницы нет: всё читается и меняется через API §6.

- **Таблица:** имя; теги протоколов — по одному на inbound, где у пользователя есть клиенты (`remark · протокол`, `×N`, если клиентов там несколько); трафик — использовано / лимит из `SubUserView` (`up + down` / `total`, 0 — «без лимита»); срок — общий срок клиентов или «у клиентов разный» (`expiryTime` представления 0 и для «бессрочно», и для «разный», поэтому страница смотрит на клиентов); ссылка подписки `subURI + subId` с копированием и QR; переключатель вкл/выкл.
- **Технические пользователи** помечены тегом; у них нет переключателя, удаления, ➕/➖ и ссылки. Кнопка клиентов открывает список клиентов пользователя; у `robot` у каждого клиента кнопка «Назначить пользователю», `monitoring` только для чтения.
- **Создание:** имя, галочки inbound'ов из `GET /inbounds` (включённые отмечены сразу), лимит трафика в ГБ, срок (дата), лимит IP, Telegram ChatID, комментарий — одни на всех клиентов. Ответ `awg_linkable` превращается в вопрос «Привязать существующего AmneziaWG-клиента?»; «Привязать» повторяет запрос с `linkExisting: true`. То же в ➕.
- **➕** предлагает только inbound'ы, где у пользователя ещё нет клиента. **➖** и удаление спрашивают подтверждение. Отказ сервиса показывается его текстом (например, «inbound … would be left without clients»).
- **Модалка клиента на Inbounds:** под полем subId подсказка «Пользователь: <имя>» (`component/aSubUserHint.html`; hunk в `form/client.html` и подключение в `inbounds.html`). Владелец subId берётся из `GET /list`: subId пользователя и subId его клиентов (так probe subId показывает `monitoring`); пустой subId — `robot`, незнакомый — «новый» (после сохранения `Sync` создаст пользователя по имени клиента). Ошибки проверок §4 при сохранении приходят обычным путём панели: `jsonMsg` → toast «Something went wrong (<текст проверки>)».

## 10. Бот

[#169](https://github.com/SBKubric/sane-3x-ui/issues/169), только админы; `web/service/tgbot_users*.go` зовут `SubUserService` напрямую. В upstream-файле `tgbot.go` — четыре точки вставки: перехват кнопок (`answerUsersCallback`) и текста (`answerUsersText`), строка «👥 Пользователи» в главном меню и «➕ Добавить протокол» в карточке xray-клиента (и ещё две от #183: перехват `answerProbeCallback` и ветка probe в начале `searchClient`); ссылка подписки строится общим `subscriptionURLs(subId)` (host override, фронт, активное edge — как у ссылок клиента).

- **Создание.** «Add Client» открывает клавиатуру inbound'ов `Inbounds()` с галочками ✅/⬜, включённые отмечены; «Далее» → имя → сводка с лимитами (трафик, срок в днях после первого подключения, лимит IP, комментарий — прежние шаги, общие для всех протоколов) → «Создать» = `Create`. `awg_linkable` → вопрос «Привязать существующего AWG-клиента … к подписке?» и повтор с `linkExisting`. Ответ — карточка пользователя со ссылкой `/sub/<subId>`.
- **«👥 Пользователи».** Поиск `Find` по имени, subId или имени клиента; кнопки `robot` и `monitoring`. Карточка: ссылка подписки, суммы, клиенты по протоколам со статусом и трафиком. Кнопки: ➕ (только inbound'ы, которых у пользователя нет; `AddProtocol`), ➖ (с подтверждением; отказ сервиса показывается как есть), вкл/выкл, удалить (с подтверждением). У `robot` — постраничный список клиентов с «Назначить пользователю» (`Assign`), у `monitoring` — только просмотр.
- **Карточка probe account** ([#183](https://github.com/SBKubric/sane-3x-ui/issues/183), `web/service/tgbot_probe_card.go`) — только для чтения: трафик (↑, ↓, всего), вкл/выкл, online и последнее подключение (онлайн — как у всех протоколов: включён и виден в пределах `onlineWindow`), последний IP (у xray — последняя запись журнала IP, у AWG-пира — `lastIp` туннеля), inbound и пользователь `monitoring`. Кнопки — только «🔄 Обновить» и «⬅️ Назад» к `monitoring`. Открывается:
  - из карточки `monitoring` — кнопка «📡 Probe accounts», постраничный список по 20;
  - поиском в «👥 Пользователи» по имени probe и командой `/usage <имя>`;
  - из «All clients» inbound'а: probe там по-прежнему в списке, кнопки upstream-карточки (`client_get_usage`/`client_refresh`/`client_cancel`) на probe открывают эту карточку. Из отчётов об online probe скрыты, как и раньше.
- **Отказ на сервере.** Перехват `answerProbeCallback` стоит в `answerCallback` перед перехватом пользователей. Любая кнопка, у которой первый аргумент — имя probe (без учёта регистра), отклоняется: toast «… is a probe account: read only» и на месте сообщения — карточка probe. Исключения — сама карточка, её список и ссылки подписки (`client_sub_links`, `client_individual_links`, `client_qr_links`), они ничего не меняют. Список запрещён по умолчанию: новая кнопка upstream на probe тоже получит отказ. Guard в `UpdateInboundClient` уже не даёт менять вкл/выкл, лимиты, срок, лимит IP и tgId probe. Сброс трафика (`ResetClientTrafficByEmail`) и очистку IP (`ClearClientIps`) он не останавливает: их отклоняет только бот.
- Кнопки адресуют пользователя его ключом (subId, `@robot`), а не состоянием чата: старая карточка не действует на другого пользователя. Состояние чата — только черновик создания и ожидаемый текст.
- Тесты: `web/service/tgbot_users_test.go` — клавиатура галочек (отметки, исключения, длина callback data ≤ 64 байт), создание через сервис (лимиты, откат, `awg_linkable`), кнопки карточки по состоянию, ➕/➖ с отказом, удаление, поиск, назначение из `robot`, тексты ru и наличие ключей во всех языках. `web/service/tgbot_probe_card_test.go`: карточки xray-probe и AWG-пира с двумя кнопками; отказ на каждую изменяющую кнопку; сквозной путь через `answerCallback` с поддельным Bot API (`telego.WithAPICaller`), после которого probe не изменён; открытие из `/usage`, поиска, карточки `monitoring` (с листанием) и списка клиентов inbound'а; ключи `[tgbot.probe]` во всех языках.
