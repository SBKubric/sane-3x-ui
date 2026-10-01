# 3AX-UI proxy front

Панель управления Xray (форк 3x-ui) с режимом «прокси-фронт»: одноразовые серверы, которые принимают клиентский трафик и пробрасывают его по цепочке на скрытый реальный сервер.

## Language

**Real server**:
Сервер с панелью, inbound'ами, клиентами и историей трафика; его адрес никогда не попадает в клиентские конфиги.
_Avoid_: upstream, hidden server, panel box

**Proxy front**:
Одноразовый сервер-звено, который принимает трафик от клиентов или от внешнего звена и relay'ит его на next hop; при блокировке выбрасывается и заменяется.
_Avoid_: proxy box, relay server, relay панель, front

**Relay**:
Процесс на proxy front (xray dokodemo-door), который на уровне L4 пробрасывает relayed ports на next hop без терминации TLS.
_Avoid_: forwarder, tunnel

**Relayed port**:
Публичный порт real server, который relay открывает у себя и пробрасывает один-в-один. Складывается из xray inbound ports и extra ports.

**Xray inbound port**:
Relayed port, который панель берёт из своего xray-конфига по правилам пропуска и кладёт в документ цепочки.

**Relay manifest**:
Историческое: санированная выписка из xray-конфига real server, которую владелец вставлял в setup page (ADR 0001). В цепочке заменена документом цепочки; правила отбора портов живут в пакете `chainports`.
_Avoid_: panel config, exported config.json, panel-xray.json

**Join page** (страница входа):
Одноразовая страница на proxy front по секретной ссылке, в которую владелец вводит адрес next hop и join token; исчезает, как только вход принят. Преемница setup page из ADR 0001.
_Avoid_: setup page, bootstrap page, onboarding, wizard

**Extra port**:
Relayed port, который real server обслуживает вне xray (AmneziaWG, WireGuard, MTProto) и который поэтому нельзя узнать из xray-конфига.
_Avoid_: host port, additional port

**Host override**:
Глобальная настройка панели, подменяющая адрес real server на адрес active edge во всех выдаваемых конфигах и ссылках подписки; в конфигах xray вместо адреса edge — VPN-имя, если оно задано.
_Avoid_: proxy override, address substitution

**VPN-имя** (VPN name):
DNS-имя (`vpnName`, например `vpn.example.com`), A-запись которого панель через API DNSExit держит на IPv4 active edge; при включённом host override ссылки VLESS называют его вместо адреса edge. Ссылки подписки и `Endpoint` AWG остаются по адресу.
_Avoid_: домен VPN, vpn domain, публичный адрес (это публичный адрес подписок)

**Tunnel subscription**:
Публичный маршрут подписки, отдающий по subId клиентские конфиги AmneziaWG и WireGuard той же подписки; дополняет xray-подписку, не меняя её.
_Avoid_: AWG subscription, conf feed, tunnel feed

## Users

**Пользователь** (user):
Человек, которому выдан доступ: одно имя, одна подписка, один или несколько клиентов.
_Avoid_: subscriber, account, admin (администратор панели — не пользователь)

**Клиент** (client):
Учётная запись в одном inbound'е; принадлежит ровно одному пользователю.
_Avoid_: user, account

**Подписка** (subscription):
Ссылка `/sub/<subId>` пользователя.
_Avoid_: sub link, subscription id (subId — ключ подписки, а не она сама)

**Публичный адрес подписок** (public subscription address):
Домен, через который панель выдаёт ссылки подписок (`subPublicURL`, например `https://sub.example.com`): origin всех ссылок — бот, страница подписки, `Profile-Web-Page-Url`, страницы панели, рассылка ссылок; пути подписок — свои. Пусто — ссылки как без него. Адреса внутри конфигов не меняет.
_Avoid_: subURI, reverse proxy URI (это полный URI с путём), sub domain (`subDomain` — адрес sub-сервера)

**Витрина подписок** (subscription showcase):
Отдельный сервер под публичным адресом подписок, отдающий только пути подписок: пробует edge по очереди, остальное — заглушка. Не VPN-вход.
_Avoid_: sub proxy, edge

**Доверенный адрес фронта** (front trusted address):
IP или сеть CIDR из настройки `frontTrustedAddrs` — хост, который ходит на HTTP-сторону фронтов одним адресом за многих клиентов, прежде всего витрина подписок. Фронт каждого звена (через документ цепочки) и панели не ограничивает и не банит его, как соседей по цепочке.
_Avoid_: whitelist, allowlist (доступ он не открывает — только снимает лимиты и бан), exempt (исключения — шире: ещё соседи и mon-server)

**Технический пользователь** (technical user):
`robot` (клиенты без подписки) и `monitoring` (probe accounts); его нельзя удалить или переименовать, его имя нельзя занять.
_Avoid_: system user, service user

**xray email**:
Технический ключ xray-клиента (поле `email`); в панели и в боте подписан именно так и ищется наравне с именем пользователя. Имена полей в API и JSON остаются `email`.
_Avoid_: email (это не почта)

**Почта пользователя** (contact email):
Необязательный адрес почты пользователя для связи (`sub_users.contact_email`). С xray email не связана: тот генерируется.
_Avoid_: email клиента, xray email

**Telegram-аккаунт** (Telegram account):
Тот, кто писал боту или нажимал его кнопки: `tg_id`, @ник, имя, когда последний раз виден (`tg_accounts`). Бывает без пользователя; с пользователем связан один к одному через `sub_users.tg_id` — это главный tgId, клиенты получают его копию.
_Avoid_: пользователь Telegram, chat id (у группы или канала он тоже есть, а аккаунт — человек)

**Ссылка-приглашение** (invite link):
Одноразовая ссылка `t.me/<бот>?start=<токен>` на 7 дней, которой админ привязывает Telegram человека, ещё не писавшего боту: кто нажмёт по ней Start, того Telegram-аккаунт становится Telegram пользователя (`tg_invites`). «Перевыпустить» делает прежнюю недействительной.
_Avoid_: инвайт, токен (токен — только её часть)

**Поиск пользователей** (user search):
Одно поле, нечёткий поиск `SubUserService.Search` — общий для бота и страницы «Пользователи»: точное совпадение → начало → подстрока → опечатки, до 20 результатов. Tg_id — только точно.
_Avoid_: фильтр, find (`Find` — точный поиск одного пользователя)

**Заявка** (request):
Просьба о подписке, которую Telegram-аккаунт без пользователя оставляет в боте после капчи, с комментарием или без (`sub_requests`); у аккаунта одна ждущая, через 14 дней без решения она просрочена, после отказа новая — через 7 дней. Решает админ.
_Avoid_: заказ, заявление, registration request (это mon-client)

**Параметры по заявке** (request defaults):
Что получает пользователь, созданный по «✅ Одобрить»: inbound'ы (по умолчанию — все включённые), трафик на протокол (50 ГБ) и срок от первого подключения (30 дней). Задаются в настройках панели, вкладка Telegram.
_Avoid_: шаблон пользователя, тариф

**Капча заявки** (request captcha):
Проверка ALTCHA перед заявкой, только бота: страница `https://<active edge>/third-party/<secret>/captcha`, открытая как Telegram Mini App. Привязана только к tg_id из подписанной ботом `initData`, к подписке — никак. Решение проверяет панель; прохождение запоминается (`tg_captcha`), пока админ его не сбросит или не заблокирует аккаунт.
_Avoid_: recaptcha, антибот

**Путь бота** (bot path, third-party path):
`/third-party/<secret>/` на фронте 443 — где бот отдаёт свой Mini App. Секрет — в настройках панели, «Перевыпустить» на вкладке Telegram. Звенья узнают путь из документа цепочки и передают его внутрь; на `real` фронт ведёт его на порт панели. Не путь подписки.
_Avoid_: subPath бота, путь капчи под подпиской

**Рассылка ссылок** (link broadcast):
Бот присылает пользователям с Telegram новую ссылку на подписку, её QR и «Моя подписка», а админу — отчёт с теми, до кого не дошло. Сама — только после «Да» админа, когда ссылки изменились (активное edge, путь подписок, фронт, subId); вручную — с экрана «Сервер», из карточки пользователя и со страницы «Пользователи». Журнал — `sub_link_broadcasts`.
_Avoid_: рассылка (без «ссылок» — это и уведомления), mailing, notify

**Экран** (screen):
Одно сообщение бота на диалог админа, которое правится на месте: меню, список или карточка. `/start` присылает новый экран и удаляет старый.
_Avoid_: меню-сообщение, клавиатура

## Chain

**Chain** (цепочка):
Связный список proxy front'ов между клиентами и real server: каждое звено relay'ит на следующее, подписки идут той же дорогой. На панель — одна цепочка.
_Avoid_: multi-hop, relay chain, route

**Hop** (звено):
Один proxy front в составе цепочки.
_Avoid_: node, link, box

**Next hop** (следующее звено):
Тот, на кого звено relay'ит трафик и у кого берёт подписки и документ цепочки: inner front или real server.
_Avoid_: upstream, target host

**Edge front** (внешнее звено):
Звено, которое видят клиенты; кандидат на host override.
_Avoid_: entry node, public front, exit

**Inner front** (внутреннее звено):
Звено, которое знают только соседние звенья; в клиентские конфиги не попадает.
_Avoid_: middle hop, intermediate, transit

**Active edge** (активное edge):
Edge front, на который сейчас указывает host override.
_Avoid_: current front, primary

**Standby edge** (запасное edge):
Edge front, уже вошедший в цепочку, но не активный; ждёт переключения host override.
_Avoid_: spare, backup, reserve

**Chain registry** (реестр цепочки):
Список звеньев на панели с ролями, порядком, next hop и active edge; описывает цепочку, но не управляет боксами напрямую.
_Avoid_: topology, node list, inventory

**Chain document** (документ цепочки):
Версионированная выписка из реестра, которую звенья передают наружу с усечением: звено видит себя, всё снаружи от себя и relayed ports, но ничего глубже.
_Avoid_: chain manifest, config bundle

**Wave** (волна):
Прокатывание новой ревизии документа цепочки изнутри наружу: каждое звено спрашивает свой next hop и применяет свою часть.
_Avoid_: push, sync, broadcast, propagation

**Join token** (join-токен):
Одноразовый секрет, выданный панелью новому звену; по нему next hop узнаёт звено и впускает его в цепочку.
_Avoid_: enrollment key, pairing code, invite

**Hop secret** (секрет звена):
Долгоживущий секрет, который панель выдаёт звену при входе; next hop сверяет его по хэшу из документа цепочки и только по нему отдаёт волну. У каждого звена свой; изъятое edge раскрывает только свой.
_Avoid_: chain secret, shared secret, API key, chain token

**Front 443** (фронт 443):
nginx на каждой коробке цепочки — единственный открытый TCP-порт: читает SNI и либо отдаёт поток сырым дальше, либо терминирует TLS на HTTP-стороне.
_Avoid_: nginx mode, reverse proxy, gateway

**Front report** (отчёт о фронте):
Что звено при каждом опросе сообщает о своём фронте 443 — режим (`off`/`only443`) и где его sub-сервер ждут внешние соседи; панель переносит это в реестр и бампает ревизию, отчёты внешних звеньев едут внутрь с их подтверждениями.
_Avoid_: mode ping, front status

**HTTP side** (HTTP-сторона):
Часть фронта 443, которая терминирует TLS — запросы по IP без SNI и по собственному домену панели — и раздаёт подписки, API панели (только вход и `panel/api`), `/chain/v1` и `/mon/v1` по путям; в `only443` — под лимитами по адресу клиента.
_Avoid_: web front, public API, site

**Промах** (miss):
Запрос к HTTP-стороне, по которому видно пробника: неопубликованный путь под секретным префиксом (его отвечает заглушка), отказ upstream'а под опубликованным путём или превышение лимита. Снаружи неотличим от обычного ответа; пишется в лог промахов nginx, по которому fail2ban банит адрес. Соседи по цепочке и mon-server в исключениях — их не лимитируют и не банят.
_Avoid_: 404, probe hit, bad request

**Neighbour target** (target-сосед):
Чужой сайт в той же сети, что и адрес edge front; его TLS изображает Reality-inbound, и туда же уходит TLS с незнакомым SNI. Свой у каждого edge; панель ставит Reality-inbound'ам target-сосед active edge. В реестре — пара `realityTarget` (host:port) и `realityServerName` (имя сервера; пусто — хост из target).
_Avoid_: dest, camouflage site, SNI domain

**Chain-following inbound** (inbound за цепочкой):
Reality-inbound, у которого панель при смене active edge переписывает target и serverNames на target-сосед нового active edge. Помечается флагом `followChain` в форме inbound'а; при `/proxy off` остаётся на target-соседе последнего active edge.
_Avoid_: managed inbound, auto inbound

## Monitoring

**mon-server**:
Единственный внешний сервис мониторинга на отдельном сервере: ведёт реестр mon-clients, получает у real server конфиги probe accounts и текущий host override, раздаёт mon-clients их targets, считает состояние каждого target и сообщает real server переходы и статистику.
_Avoid_: monitoring hub, collector, watchdog server

**mon-client**:
Коробка в целевом регионе, которой mon-server назначает набор targets; для каждого поднимает туннель (xray-core, awg) и раз в минуту шлёт mon-server tunnel probe через туннель и heartbeat мимо него.
_Avoid_: agent, probe node, sensor

**Target**:
Пара «inbound real server × path», которую проверяет один mon-client через probe account этого inbound'а. Единица состояния UP/DOWN и статистики.
_Avoid_: check, monitor, endpoint

**Path**:
Через какой адрес target достигает real server: `direct` (настоящий адрес real server), `edge:<name>` или `inner:<name>` (конкретное звено цепочки по имени), а пока у цепочки нет пробируемых звеньев — `proxy` (адрес из host override). Других значений нет.
_Avoid_: mode, route

**Probe account**:
Служебный клиент с именем `probe-…`, который панель заводит по запросу mon-server; все probe accounts панели живут под общим subId. В клиентском xray-inbound'е — один на inbound, общий для всех mon-clients и path. В туннельном сервере (AWG) — отдельный пир на каждую пару «mon-client × path», потому что у пира один endpoint и одна сессия и общий пир делят конкурирующие пробы. Отличается от пользовательских префиксом имени, не считается пользователем и чистится панелью по таймауту, когда mon-server перестаёт его подтверждать.
_Avoid_: monitoring client, service user, test client

**Tunnel probe**:
Ежеминутный запрос mon-client к mon-server, отправленный внутрь туннеля target'а; его успех означает, что inbound работает для реальных клиентов по этому path.
_Avoid_: ping, healthcheck

**Heartbeat**:
Ежеминутный запрос mon-client к mon-server мимо туннеля; несёт результаты tunnel probes и диагностику, а в ответ получает номер актуальной ревизии конфига. Отсутствие heartbeat означает, что мёртв сам mon-client, а не туннель.
_Avoid_: keepalive, ping

**Сверка состояния** (state resync):
Событие, которым mon-server подтверждает текущее состояние target'а без перехода; панель просит о нём, когда у неё нет состояния target'а, и применяет его молча — без ленты событий и без Telegram.
_Avoid_: resend, replay, full sync

**Stale**:
Состояние target в панели, когда mon-server не присылал статистику дольше порога; отличается от DOWN тем, что молчит мониторинг, а не inbound.
_Avoid_: unknown, expired

**Registration request**:
Заявка mon-client на вход в реестр mon-server: подаётся без секрета, живёт пять минут в состоянии pending и превращается в запись реестра только после одобрения администратором в админке mon-server.
_Avoid_: enrollment, join request, handshake

**Pairing code**:
Короткий код, который mon-client печатает в свой лог и прикладывает к registration request; администратор сверяет его в админке, чтобы одобрить именно свою коробку.
_Avoid_: PIN, OTP, verification token

**Client token**:
Постоянный секрет mon-client, выданный mon-server при одобрении registration request; им подписаны heartbeat, tunnel probe и запрос конфига. Отзыв токена выкидывает mon-client из реестра до новой заявки.
_Avoid_: API key, bearer, credential

**Config revision**:
Хэш конфига, который mon-server собрал для конкретного mon-client (его targets, paths и параметры проб); возвращается в ответе на heartbeat, и его смена — единственный сигнал mon-client перечитать конфиг.
_Avoid_: version, generation, panel revision

**Unverified cycle**:
Цикл проб mon-client, за который heartbeat так и не был подтверждён mon-server; его провалы не считаются, потому что адресат tunnel probe — сам mon-server, и его недоступность нельзя отличить от падения туннеля.
_Avoid_: offline cycle, buffered cycle

**Admin UI**:
Веб-интерфейс mon-server за логином и паролем: одобрение registration requests, реестр mon-clients и все настройки mon-server; состояние targets он не показывает — это страница Monitoring панели.
_Avoid_: dashboard, console, mon-server panel
