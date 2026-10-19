# Delta Chat бот: SecureJoin по ссылке пользователя, отпечаток, Channel и лимиты relay

Исследование по тикету [#268](https://github.com/SBKubric/sane-3x-ui/issues/268) (часть карты [#267](https://github.com/SBKubric/sane-3x-ui/issues/267)). Дата: 2026-10-19.

**Стек.** Go-клиент [`github.com/chatmail/rpc-client-go/v2` v2.56.0](https://github.com/chatmail/rpc-client-go/tree/v2.56.0) и `deltachat-rpc-server` v2.56.0. Бинарник сервера — официальный, из релиза [chatmail/core v2.56.0](https://github.com/chatmail/core/releases/tag/v2.56.0) (`deltachat-rpc-server-x86_64-linux`, sha256 `312d4753…843c`). Исходники сверены по тегу core `v2.56.0` и по relay [`chatmail/relay@305d83f`](https://github.com/chatmail/relay/tree/305d83fff38fafacfbf62931a98f4b0db87e17f9) / [`chatmail/filtermail@2c0cda2`](https://github.com/chatmail/filtermail/tree/2c0cda2216ec722248392bd87c14c0c54b66f2e0).

**Как проверяли.** Скрипт [`deltachat-bot-check/main.go`](deltachat-bot-check/main.go) запускает два процесса rpc-server:
- **bot** — один аккаунт с `bot=1`;
- **user** — аккаунты `user`, `user2`, `user3` в роли подписчиков.

Все аккаунты одноразовые, созданы через `dcaccount:nine.testrun.org`; для смены relay брали `mehl.cloud`. Скрипт прогнали три раза на чистых каталогах, результаты совпали. Лог третьего прогона: [`run-2026-10-19.log`](deltachat-bot-check/run-2026-10-19.log), адреса в нём скрыты. Исходящие IMAP/SMTP с машины не блокируются.

**Ограничение проверки.** Сторона «пользователя» — тот же chatmail core, что встроен в приложения Android, iOS и desktop, но без их UI. Поэтому всё, что касается **отображения** (кликабельность команд, вид файлов), взято из исходников клиентов, а не наблюдалось на телефоне.

Метки у каждого утверждения:
- **[relay]** — проверено на живом relay этим скриптом;
- **[src]** — по исходникам или официальной документации, со ссылкой.

## TL;DR

1. **SecureJoin по ссылке пользователя работает.**
   - Бот вызывает `CheckQr(link)`, ожидает вид `askVerifyContact`, затем `SecureJoin(link)`. Вызов сразу возвращает chatId.
   - Успех — событие `SecurejoinJoinerProgress{progress:1000}`. Если пользователь онлайн, оно приходит через ~2 с **[relay]**.
   - Писать до этого события нельзя: `Cannot send to Chat#N: key is missing` **[relay]**.
   - Если пользователь офлайн, рукопожатие ждёт и завершается через ~2 с после его выхода в сеть **[relay]**.
   - Отдельного события «неудача» нет: таймаут бот ведёт сам **[src]**.
   - Несколько незавершённых привязок одновременно не мешают друг другу **[relay]**.
2. **Идентичность — PGP-отпечаток.**
   - Отпечаток есть в самой ссылке (`CheckQr → fingerprint`). Он совпадает с `GetContactEncryptionInfo` после join **[relay]**.
   - Contact ID в базе бота стабилен, пока не меняется ключ.
   - Смена relay с тем же ключом: тот же contactId и тот же отпечаток, адрес обновляется сам **[relay]**.
   - Переустановка без бэкапа или «второго устройства» даёт новый ключ, то есть **новый контакт**. Нужна перепривязка через tg **[src]**.
3. **Channel.**
   - `CreateBroadcast` и `GetChatSecurejoinQrCode(&chatId)` дают ссылку `https://i.delta.chat/#…` **[relay]**.
   - Подписчики друг друга не видят **[relay]**.
   - Пост уходит **одним** SMTP-письмом на всех: одна единица лимита на ≤999 получателей **[relay]** + **[src]**.
   - Жёсткого предела подписчиков нет **[src]**.
   - Добавить подписчика без его действия нельзя **[relay]**.
   - **Новое:** каждая подписка стоит боту 1 + N писем, где N = min(10, число постов в канале). Ядро досылает новичку последние посты **[relay]** + **[src]**.
4. ⚠️ **`/1`…`/N` не кликабельны ни в одном клиенте.** Android и desktop требуют букву сразу после `/`, iOS — минимум 2 символа **[src]**.
   - Кликабельный вариант для всех трёх: `/u1`, `/n2`, `/help`.
   - Тап только **вставляет** команду в поле ввода, отправляет пользователь сам **[src]**.
   - `.conf` приходит файлом с исходным именем. PNG приходит картинкой (`Image`), даже если слать его как `File` **[relay]**.
5. **Эксплуатация.**
   - Лимит relay — токен-бакет на отправителя: 60/мин, burst 10 **[src]**.
   - У bot-аккаунта ядро своего ограничителя не включает **[src]**. 25 писем подряд: первые 11 ушли за 2 с, остальные получили `450 4.7.1` и дошли только через ~80 с из-за ретраев ядра **[relay]**. Бот должен сам держать темп **≤1 письмо/с с burst ≤10**.
   - После перезапуска из того же каталога аккаунтов всё на месте, первое сообщение доставлено через 0,8 с **[relay]**.
   - В простое rpc-server занимает RSS ~20–25 МБ, CPU ≈0 (≤0,01 с за 60 с) **[relay]**.

## 1. SecureJoin по ссылке-приглашению пользователя

### 1.1. Что внутри ссылки

Ссылку «Пригласить» в клиенте строит `get_securejoin_qr` ([securejoin.rs#L181-L193](https://github.com/chatmail/core/blob/v2.56.0/src/securejoin.rs#L183-L193)) **[src]**:

```
https://i.delta.chat/#<FPR>&v=3&i=<invitenumber>&s=<auth>&a=<addr>&n=<name>
```

В ссылке:
- отпечаток ключа пользователя;
- его адрес на relay;
- **секрет `s=` (auth)**.

Любой, у кого есть ссылка, может стать верифицированным контактом пользователя. Отсюда выводы для tg-бота:
- ссылку не логировать;
- сообщение с ней лучше удалять из чата tg после обработки.

Каждый показ QR создаёт новый auth-токен, старые остаются действительными до «сброса QR» в клиенте ([securejoin.rs#L132-L141](https://github.com/chatmail/core/blob/v2.56.0/src/securejoin.rs#L132-L142)). Верифицированным контакт становится, только если auth моложе 7 дней (`VERIFICATION_TIMEOUT_SECONDS`, [securejoin.rs#L47](https://github.com/chatmail/core/blob/v2.56.0/src/securejoin.rs#L47), [#L651-L654](https://github.com/chatmail/core/blob/v2.56.0/src/securejoin.rs#L651-L654)). Сам join срабатывает и со старым токеном **[src]**.

### 1.2. Вызовы и события

| Шаг | Вызов / событие | Наблюдение |
|---|---|---|
| Проверка ссылки | `rpc.CheckQr(acc, link)` → `*dc.QrAskVerifyContact{Fingerprint, ContactId, IsV3}` | `kind=askVerifyContact`, `v3=true`. Отпечаток в «человеческом» виде: группы по 4 символа, перенос строки после 20 hex. Перед сравнением убрать пробелы и `\n` **[relay]** |
| Старт | `rpc.SecureJoin(acc, link)` → chatId | возвращается сразу (0,01 с) **[relay]**. «The function returns immediately and the handshake runs in background» ([rpc.go#L644](https://github.com/chatmail/rpc-client-go/blob/v2.56.0/deltachat/rpc.go#L644)) |
| Сразу после старта | `CanSend` / `MiscSendTextMessage` | `CanSend=false`; отправка → ошибка `Cannot send to Chat#10: key is missing` **[relay]**. Писать первым до конца рукопожатия нельзя, очереди «на потом» нет |
| Прогресс бота | `EventTypeSecurejoinJoinerProgress{ContactId, Progress}` | 400 через 1,2 с, 1000 через 2,0 с **[relay]**. 400 — отправлен `vc-request-with-auth`, 1000 — получен `vc-contact-confirm` ([types.go, EventTypeSecurejoinJoinerProgress](https://github.com/chatmail/rpc-client-go/blob/v2.56.0/deltachat/types.go#L1198-L1204)) |
| У пользователя | `SecurejoinInviterProgress{progress:1000}` | 1:1 чат создан и сразу принят: `isContactRequest=false`. Бот у пользователя: `isBot=true`, `isVerified=true` **[relay]** |
| Первое сообщение | `MiscSendTextMessage` после 1000 | дошло за 0,4 с **[relay]** |

**Неудача.** События «ошибка» у SecureJoin нет. Если auth неверен или QR сброшен, инвайтер молча игнорирует запрос (`Secure-join denied (bad auth)`, [securejoin.rs#L525-L528](https://github.com/chatmail/core/blob/v2.56.0/src/securejoin.rs#L531-L535)). У бота просто никогда не придёт 1000, а строка в таблице `bobstate` останется **[src]**.

Синхронно `SecureJoin` падает только на неразбираемой ссылке. Поэтому бот:
- сам проверяет `CheckQr(...).GetKind() == "askVerifyContact"`; ссылки на группу или канал отвергаются;
- держит свой таймаут ожидания привязки.

**Несколько привязок одновременно.** В doc-комментарии клиента сказано: «Subsequent calls of `secure_join()` will abort previous, unfinished handshakes» ([rpc.go#L638-L639](https://github.com/chatmail/rpc-client-go/blob/v2.56.0/deltachat/rpc.go#L635)). Для 1:1 это **неверно**: core хранит несколько строк `bobstate` и обходит их все ([bob.rs#L248-L285](https://github.com/chatmail/core/blob/v2.56.0/src/securejoin/bob.rs#L244-L285)) **[src]**. Проверено опытом **[relay]**:
1. рукопожатие с офлайн-`user2` висит;
2. бот проходит SecureJoin с `user3`, 1000 за 5 с;
3. `user2` выходит в сеть, его рукопожатие тоже завершается.

**Повторная привязка.** Если ключ пользователя уже известен боту и в ключе есть все адреса из ссылки, core идёт коротким путём: сразу шлёт `request-with-auth` ([bob.rs#L117-L131](https://github.com/chatmail/core/blob/v2.56.0/src/securejoin/bob.rs#L116-L131)) **[src]**.

### 1.3. Пользователь офлайн

Проверка **[relay]**:
1. `user2` выдал ссылку и остановил IO (`StopIo`);
2. бот вызвал `SecureJoin`. За ~80–90 с прогресса не было, отправка давала `key is missing`;
3. после `StartIo` у `user2`: 400 через 1,2 с, 1000 через 2,3 с.

Запрос лежит в ящике пользователя на relay. Там он живёт не дольше `delete_mails_after` = 20 дней ([chatmail.ini.f#L28](https://github.com/chatmail/relay/blob/305d83fff38fafacfbf62931a98f4b0db87e17f9/chatmaild/src/chatmaild/ini/chatmail.ini.f#L28); на nine.testrun.org: «Messages are unconditionally removed latest 20 days after arriving», [info](https://nine.testrun.org/info.html)) **[src]**.

`bobstate` у бота таймаута не имеет и переживает перезапуск, потому что лежит в БД аккаунта **[src]**.

Вывод для UX привязки в tg:
- сообщить «откройте Delta Chat, бот напишет сам»;
- считать привязку незавершённой, пока не пришёл 1000;
- через N часов предложить повторить.

Для мобильного клиента, разбуженного push-уведомлением, задержка не измерялась.

## 2. Идентичность контакта

- **Стабильный ключ — отпечаток.** Отдельного поля `fingerprint` в `dc.Contact` нет ([types.go#L428-L468](https://github.com/chatmail/rpc-client-go/blob/v2.56.0/deltachat/types.go#L428-L468)). Источники:
  - **ссылка** — `CheckQr` → `QrAskVerifyContact.Fingerprint` (40 hex для v4-ключа) ещё до join;
  - **`GetContactEncryptionInfo(acc, contactId)`** — текст с блоками `Имя (addr):\n<FPR по 4 символа>` ([contact.rs#L1384-L1453](https://github.com/chatmail/core/blob/v2.56.0/src/contact.rs#L1384-L1453)). Текст локализуемый, но блок `(addr):\nHEX…` парсится стабильно. Отпечаток из него совпал с отпечатком из ссылки **[relay]**;
  - `MakeVcard` — в vCard лежит публичный ключ, отпечаток можно вычислить самим **[src]**.
- **Contact ID.** Контакт в чатах с шифрованием — key-contact (`isKeyContact=true`), один на ключ. ID стабилен в БД бота **[relay]**. Хранить в панели: `fingerprint` (первичная идентичность) + `contactId` (для отправки). Адрес не хранить как ключ: в доке клиента прямо сказано «DO NOT internally represent contacts by their email address» ([rpc.go#L1238-L1252](https://github.com/chatmail/rpc-client-go/blob/v2.56.0/deltachat/rpc.go#L1236-L1252)).
- **Смена relay** (тот же ключ). Пользователь добавил транспорт на `mehl.cloud` и сделал его основным (`AddTransportFromQr` + `configured_addr`), затем написал боту **[relay]**:
  - бот получил сообщение от **того же** `contactId=10`;
  - адрес контакта уже на новом relay;
  - отпечаток тот же.

  Документация: «Adding or removing a relay will automatically inform your chat partners» ([help#relays](https://delta.chat/en/help#relays)) **[src]**.
- **Переустановка.**
  - Без бэкапа или переноса профиля генерируется новый ключ: для бота это **новый** контакт с новым отпечатком. Старый контакт остаётся, но писать в него бессмысленно. Привязку надо повторить через tg (новая ссылка); правило карты «повторная привязка заменяет прежний контакт» это покрывает.
  - Восстановление из бэкапа или «Add Second Device» переносит ключ, контакт тот же ([help#multiple-devices](https://delta.chat/en/help#can-i-use-delta-chat-on-multiple-devices-at-the-same-time)). Ключ вручную не импортируется ([help#importkey](https://delta.chat/en/help#importkey)) **[src]**.
- **Bot-аккаунт не получает contact requests:** чат от незнакомца создаётся сразу незаблокированным ([receive_imf.rs#L1450-L1456](https://github.com/chatmail/core/blob/v2.56.0/src/receive_imf.rs#L1450-L1456)) **[src]**. Написать боту может любой, кто знает его адрес. Не привязанным бот отвечает «привяжите в Telegram» — регистрации в Delta Chat нет.

## 3. Channel (`CreateBroadcast`)

- **Создание и ссылка** **[relay]**:
  - `CreateBroadcast(acc, name)` → chatId типа `OutBroadcast`;
  - `GetChatSecurejoinQrCode(acc, &chatId)` → `https://i.delta.chat/#<FPR>&v=3&x=<grpid>&j=…&s=…&a=…&n=…&b=<name>` ([securejoin.rs#L172-L176](https://github.com/chatmail/core/blob/v2.56.0/src/securejoin.rs#L172-L176));
  - у подписчика `CheckQr` → `askJoinBroadcast`;
  - SVG для QR даёт `CreateQrSvg(link)` ([rpc.go#L1369](https://github.com/chatmail/rpc-client-go/blob/v2.56.0/deltachat/rpc.go#L1369)), PNG рисуем сами.
- **Подписка** — только действием подписчика: тап по ссылке или скан QR **[relay]**.
  - Подписчик вызывает `SecureJoin(link)` → чат `InBroadcast` с `canSend=false`. Подписка занимает 1,6–1,9 с, если бот онлайн.
  - Попытка подписчика написать: `Broadcast channel is read-only`.
  - Если бот офлайн, заявка обработается, когда он выйдет в сеть ([help#channels](https://delta.chat/en/help#channels)) **[src]**.
- **Добавить без действия подписчика нельзя:** `AddContactToChat(channel, contact)` → `Chat#12 is not a group where one can add members` **[relay]**. Для `OutBroadcast` добавление разрешено только из рукопожатия ([chat.rs#L3881-L3885](https://github.com/chatmail/core/blob/v2.56.0/src/chat.rs#L3881-L3885)) **[src]**. Привязанный в tg пользователь подписывается сам — бот может прислать ему ссылку канала в личку.
- **Подписчики друг друга не видят:** у подписчика `contactIds=[владелец, SELF]`, у бота — все подписчики **[relay]**. Письмо в канал идёт с `To: hidden-recipients:;` и симметричным шифрованием общим секретом канала ([mimefactory.rs#L2130-L2144](https://github.com/chatmail/core/blob/v2.56.0/src/mimefactory.rs#L2130-L2144)) **[src]**; документация: «Other channel subscribers will not see that you subscribed and cannot message you» ([help#channels](https://delta.chat/en/help#channels)).
- **Стоимость поста.** Один пост двум подписчикам = **одна** SMTP-отправка: `Message len=3074 was SMTP-sent to 2 recipients` **[relay]**.
  - Ядро кладёт получателей в одну SMTP-транзакцию пачками по `max_smtp_rcpt_to` ([chat.rs#L2972-L2986](https://github.com/chatmail/core/blob/v2.56.0/src/chat.rs#L2972-L2986)). Для chatmail это 999 ([constants.rs#L186-L189](https://github.com/chatmail/core/blob/v2.56.0/src/constants.rs#L186-L189)); relay сам объявляет 1000 = `smtpd_recipient_limit` postfix ([metadata.py#L140-L142](https://github.com/chatmail/relay/blob/305d83fff38fafacfbf62931a98f4b0db87e17f9/chatmaild/src/chatmaild/metadata.py#L140-L142)).
  - Лимит relay проверяется на `MAIL FROM`, то есть на транзакцию, а не на получателя ([filtermail outbound.rs#L65-L76](https://github.com/chatmail/filtermail/blob/2c0cda2216ec722248392bd87c14c0c54b66f2e0/src/outbound.rs#L65-L76)) **[src]**.
  - **Итог: пост = 1 единица лимита на каждые 999 подписчиков.**
- **Цена подписки** (новое, в карте не учтено). На каждую подписку бот отправляет **[relay]** + **[src]**:
  - `member-added` этому подписчику — одно письмо;
  - **досылку последних постов**: до `N_MSGS_TO_NEW_BROADCAST_MEMBER = 10`, каждый отдельным письмом ([chat.rs#L3976-L4015](https://github.com/chatmail/core/blob/v2.56.0/src/chat.rs#L3976-L4015), [constants.rs#L218](https://github.com/chatmail/core/blob/v2.56.0/src/constants.rs#L218));
  - если ключ подписчика боту ещё не известен — ещё `vc-pubkey` (Securejoin v3, [securejoin.rs#L509-L525](https://github.com/chatmail/core/blob/v2.56.0/src/securejoin.rs#L515-L566)).

  В опыте: 3 поста в канале → 4 письма на каждого из двух подписчиков, всего 8. Значит, массовая подписка в первый день (например, ссылка разослана 200 пользователям) стоит до 200 × 12 = 2400 писем, при 60/мин это 40 минут очереди. Варианты:
  - держать в канале мало постов;
  - не рассылать ссылку всем разом;
  - учитывать это в общей очереди отправки.
- **Предел подписчиков.** Жёсткого числа нет ни в core, ни в документации: «The practical limit depends on the used relay… For really large channels with several tens of thousands of subscribers, we recommend using a dedicated profile» ([help#how-many-subscribers](https://delta.chat/en/help#how-many-subscribers-can-a-channel-have)) **[src]**. Для наших масштабов (сотни) ограничений нет.
- **Канал привязан к ключу и БД бота.** Секрет канала и список подписчиков есть только в каталоге аккаунтов. Если каталог потерян, канал мёртв, и все должны подписаться на новый **[src]**: `SyncAction::CreateOutBroadcast { chat_name, secret }` синхронизируется только между устройствами того же профиля, [chat.rs#L5197](https://github.com/chatmail/core/blob/v2.56.0/src/chat.rs#L5197). Каталог аккаунтов — данные для бэкапа.

## 4. Команды и файлы

### 4.1. `/команды` в клиентах [src]

Ни один клиент не отправляет команду по тапу.

| Клиент | Что считается командой | Что делает тап | Источник |
|---|---|---|---|
| Android | `(?<=^\|\s)/[a-zA-Z][a-zA-Z@\d_/.-]{0,254}` — **первая буква обязательна** | вставляет `"/cmd "` в поле ввода (не отправляет); долгий тап копирует | [Linkifier.java#L14-L15](https://github.com/deltachat/deltachat-android/blob/24c0a2b64b3a0acc3502e4500c37187028317b9b/src/main/java/org/thoughtcrime/securesms/util/Linkifier.java#L14-L15), [LongClickCopySpan.java#L41-L48](https://github.com/deltachat/deltachat-android/blob/24c0a2b64b3a0acc3502e4500c37187028317b9b/src/main/java/org/thoughtcrime/securesms/util/LongClickCopySpan.java#L41-L48) |
| iOS | `(?<=\s\|^)/[a-zA-Z0-9_\-.,$+]{2,}(?=\s\|$)` — **минимум 2 символа**, после команды пробел или конец строки | `draft.text = command + " "`, **только если поле ввода пустое** | [DetectorType.swift#L39](https://github.com/deltachat/deltachat-ios/blob/5766f379129833e4111a3937c8153549db407554/deltachat-ios/Chat/Views/DetectorType.swift#L39), [ChatViewController.swift#L2486-L2491](https://github.com/deltachat/deltachat-ios/blob/5766f379129833e4111a3937c8153549db407554/deltachat-ios/Chat/ChatViewController.swift#L2486-L2491) |
| Desktop | linkify-плагин: «Only allow alphabetic characters immediately after the slash», дальше буквы, цифры, `-_.@/` | ставит черновик (`__setDraftRequest`) | [botCommand.ts](https://github.com/deltachat/deltachat-desktop/blob/2b1f9552808fde0a1419ee2de365b414d284a0e4/packages/frontend/src/utils/linkify/plugin-bot-command/botCommand.ts), [MessageParser.tsx#L146-L195](https://github.com/deltachat/deltachat-desktop/blob/2b1f9552808fde0a1419ee2de365b414d284a0e4/packages/frontend/src/components/message/MessageParser.tsx#L146-L195) |

Следствия:
- **`/1`…`/9` не кликабельны нигде.** `/12` кликабельна только на iOS.
- Формат, кликабельный во всех трёх клиентах:
  - `/` + **латинская буква** + ещё ≥1 символ из `[a-zA-Z0-9_.-]`;
  - пробел или конец строки после команды;
  - примеры: `/u1`, `/n12`, `/help`, `/my_configs`.
- **Знаки препинания сразу после команды не ставить.** iOS захватит запятую в команду (`,` входит в класс), Android и desktop её отрежут.
- Команды подсвечиваются в любых чатах, не только в чатах с ботом. Кнопок у Delta Chat нет (факт карты).

Бот видит команду как обычный текст входящего сообщения, разбор — свой. В тестовом сообщении `"/help /status /1 /u1 /user_2 /12"` текст дошёл как есть **[relay]**.

### 4.2. Файлы

Скрипт отправил три файла через `SendMsg(MessageData{File, Filename, Viewtype, Text})`. Что получил пользователь **[relay]**:

| Отправлено | Получено |
|---|---|
| `.conf`, `Viewtype` не задан | `viewType=File`, `fileName=acc22-awg.conf`, `mime=application/octet-stream`, подпись (`Text`) сохранена |
| PNG с `Viewtype=Image` | `viewType=Image`, `mime=image/png`, но **имя файла заменено** на `image_<дата>.png` |
| PNG с `Viewtype=File` | всё равно `viewType=Image`, но имя `acc22-qr-as-file.png` сохранилось |

Отображение **[src]**:
- `Image` клиенты показывают картинкой в ленте;
- `File` — плашкой документа с именем, размером и расширением ([DocumentView.java#L46-L52](https://github.com/deltachat/deltachat-android/blob/24c0a2b64b3a0acc3502e4500c37187028317b9b/src/main/java/org/thoughtcrime/securesms/components/DocumentView.java#L46-L52)). Открывается внешним приложением, например AmneziaVPN или WireGuard, через «Открыть с помощью».

Лимит размера письма на relay — 30 МБ (`max_message_size = 31457280`, [filtermail config.rs#L110-L112](https://github.com/chatmail/filtermail/blob/2c0cda2216ec722248392bd87c14c0c54b66f2e0/src/config.rs#L110-L112)). Для `.conf` и QR это не ограничение **[src]**.

## 5. Эксплуатация

### 5.1. Лимит отправки и темп

**Relay.** filtermail ведёт token bucket на отправителя **[src]**:
- `Quota::per_minute(60).allow_burst(10)`, ключ — адрес `MAIL FROM` ([outbound.rs#L45-L46](https://github.com/chatmail/filtermail/blob/2c0cda2216ec722248392bd87c14c0c54b66f2e0/src/outbound.rs#L45-L46), [config.rs#L113-L118](https://github.com/chatmail/filtermail/blob/2c0cda2216ec722248392bd87c14c0c54b66f2e0/src/config.rs#L113-L118));
- превышение → `450 4.7.1: Too much mail from <…>, rate-limited until …`;
- в лимит идут все письма бота: личные, посты канала, служебные (рукопожатия, member-added, досылка постов).

**Core.** У bot-аккаунта (`bot=1`) внутренний ограничитель отправки выключен ([smtp/send.rs#L34-L39](https://github.com/chatmail/core/blob/v2.56.0/src/smtp/send.rs#L34-L39)) **[src]**:
- ядро шлёт всё сразу и упирается в relay;
- на 4xx: «postponing retry for later» ([smtp.rs#L247-L270](https://github.com/chatmail/core/blob/v2.56.0/src/smtp.rs#L247-L270)), пауза SMTP-цикла от 30 с с ростом ([scheduler.rs#L587-L607](https://github.com/chatmail/core/blob/v2.56.0/src/scheduler.rs#L587-L607));
- после 6 попыток письмо помечается failed: `Number of retries exceeded the limit` ([smtp.rs#L346-L385](https://github.com/chatmail/core/blob/v2.56.0/src/smtp.rs#L346-L385)), событие `MsgFailed`.

**Опыт: 25 личных сообщений подряд одному пользователю** **[relay]**:
- первые 11 доставлены за 2,1 с;
- дальше `4.7.1`;
- последнее доставлено через 80–82 с (три прогона);
- потерь и `MsgFailed` не было.

Если держать темп 1/с, те же 25 ушли бы за ~15 с.

**Рекомендация:** своя очередь отправки в боте:
- ≤1 письмо/с, burst ≤10;
- приоритет личных уведомлений над постами канала;
- учитывать скрытые письма подписок (§3);
- ловить `MsgFailed` и переотправлять.

### 5.2. Перезапуск

Всё состояние лежит в каталоге аккаунтов:
- `IOTransport.AccountsDir` → переменная `DC_ACCOUNTS_PATH` ([io_transport.go](https://github.com/chatmail/rpc-client-go/blob/v2.56.0/deltachat/io_transport.go));
- внутри `accounts.toml`, `accounts.lock`, `<uuid>/dc.db` + `dc.db-blobs`;
- в БД: ключ, пароль relay, контакты, чаты, `bobstate`, секрет канала.

Проверка **[relay]**: процесс бота закрыли и открыли на том же каталоге.
- Аккаунт сразу `configured=true`.
- Чат и отпечаток контакта на месте.
- Сообщение после `StartIo` доставлено через 0,8 с.

Требования:
- каталог на persistent volume, в бэкап (содержит секреты: в git не класть);
- одновременно может работать только один процесс на каталог (`accounts.lock`);
- relay удаляет аккаунт после 90 дней неактивности (`delete_inactive_users_after = 90`, [chatmail.ini.f#L34](https://github.com/chatmail/relay/blob/305d83fff38fafacfbf62931a98f4b0db87e17f9/chatmaild/src/chatmaild/ini/chatmail.ini.f#L34)). Работающему боту это не грозит, но бот, выключенный на 3 месяца, теряет адрес **[src]**.

Аккаунт через `dcaccount:` создаётся за 0,3 с **[relay]**. Размер каталога бота после всех тестов — 0,9 МБ.

### 5.3. Ресурсы

Измерение через `/proc` после 60 с простоя, IO запущен, IMAP IDLE **[relay]**:

| Процесс | RSS | CPU за 60 с |
|---|---|---|
| rpc-server с 1 аккаунтом (бот) | 20,3–22,6 МБ | 0–1 тик (≤0,01 с) |
| rpc-server с 3 аккаунтами | 23,0–25,2 МБ | 0 |

Статический бинарник весит 27 МБ.

## 6. Что это значит для решений карты #267

- **Привязка «бот проходит SecureJoin по ссылке пользователя и пишет первым» — подтверждена.**
  - Писать можно только после `JoinerProgress 1000`. Пользователь должен хотя бы раз открыть Delta Chat в течение ≤20 дней.
  - Ссылка содержит секрет: tg-бот не логирует её и удаляет сообщение.
- **Идентичность по PGP-отпечатку — подтверждена.** Смена relay ничего не ломает; переустановка без бэкапа = новая привязка.
- **Channel для platform status — подходит.**
  - Пост стоит 1 письмо, подписчики анонимны.
  - Подписаться можно только по ссылке, добавить насильно нельзя.
  - Каждая подписка стоит до 12 писем.
- **⚠️ Противоречие с решением [#269](https://github.com/SBKubric/sane-3x-ui/issues/269#issuecomment-6255893353)** («нумерованные `/1…/N`»): такие команды не кликабельны ни в одном клиенте. Нужен буквенный префикс (`/u1…/uN` или подобный). Тап в любом случае только подставляет команду в поле ввода.
- **Темп отправки — задача бота**, а не ядра: у bot-аккаунта ядро не ограничивает отправку само.

## Как запустить скрипт

```sh
# бинарник rpc-server из релиза chatmail/core v2.56.0
cd docs/research/deltachat-bot-check
CGO_ENABLED=0 go build -o /tmp/dccheck .
/tmp/dccheck -rpc /abs/path/deltachat-rpc-server -dir /abs/tmp/dcwork [-relay nine.testrun.org] [-relay2 mehl.cloud]
```

Скрипт создаёт 4 одноразовых аккаунта (бот, user, user2, user3) и идёт по шагам 0–9:
- 0 — аккаунты;
- 1 — SecureJoin по ссылке пользователя;
- 2 — отпечаток;
- 3 — офлайн и параллельные привязки;
- 4 — файлы;
- 5 — Channel;
- 6 — burst 25;
- 7 — RSS и CPU;
- 8 — перезапуск;
- 9 — смена relay.

Каталог `-dir` содержит ключи и пароли, его не коммитить. Это отдельный Go-модуль, в `go vet ./...` панели он не попадает.
