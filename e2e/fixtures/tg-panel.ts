import { expect, type APIRequestContext } from '@playwright/test';

/**
 * The `panel-tg` service of docker-compose.yml: a panel of its own whose
 * Telegram bot runs against `fakebot` (fixtures/fake-telegram.mjs) instead of
 * Telegram (#219). Only users-telegram-invite.spec.ts talks to it, through the
 * `telegram-bot` project of playwright.config.ts, whose baseURL it is.
 *
 * Its settings are this panel's alone: the shared panel of every other spec
 * never runs a bot, so turning this one on races nobody.
 */
export const TG_PANEL_URL = process.env.E2E_TG_BASE_URL || 'http://127.0.0.1:2054';

/** fakebot as the spec reaches it from the host. */
export const FAKEBOT_URL = process.env.E2E_FAKEBOT_URL || 'http://127.0.0.1:8081';

/** fakebot as panel-tg reaches it inside the compose network. */
const FAKEBOT_IN_COMPOSE = 'http://fakebot:8081';

/** The notification channel the bot posts the admins' news to. */
export const TG_NOTIFY_CHANNEL = '@e2e_notify';

/** panel-tg's sub server as the spec reaches it from the host (#220). */
export const TG_SUB_URL = process.env.E2E_TG_SUB_URL || 'http://127.0.0.1:2097';

/** The bot's token startTelegramBot sets: it signs the Mini App's initData. */
export const TG_BOT_TOKEN = '123456:' + 'e'.repeat(35);

/** The bot's @username, as fakebot answers getMe. */
export const TG_BOT_USERNAME = 'e2e_invite_bot';

export type Button = { text: string; callback_data?: string; url?: string; web_app?: { url: string } };

export type Sent = {
  method: string;
  chat_id: string | number;
  text: string;
  message_id?: number;
  reply_markup?: { inline_keyboard?: Button[][] };
};

/** What the bot has sent so far, through fakebot. */
export async function botSent(request: APIRequestContext): Promise<Sent[]> {
  const body = await (await request.get(`${FAKEBOT_URL}/control/sent`)).json();
  return body.result as Sent[];
}

/**
 * Turns the bot on, as an admin would in the settings: a token, fakebot as
 * the API server, the notification channel; then restarts the panel and waits
 * until it serves again and its bot polls fakebot for updates.
 */
export async function startTelegramBot(request: APIRequestContext): Promise<void> {
  const all = await (await request.post('/panel/setting/all')).json();
  expect(all.success, all.msg).toBe(true);
  if (all.obj.tgBotEnable && all.obj.tgBotAPIServer === FAKEBOT_IN_COMPOSE) return; // a retry: it is on
  const settings = {
    ...all.obj,
    tgBotEnable: true,
    tgBotToken: TG_BOT_TOKEN,
    tgBotAPIServer: FAKEBOT_IN_COMPOSE,
    tgBotChatId: '4242001',
    tgNotifyChatId: TG_NOTIFY_CHANNEL,
  };
  const saved = await (await request.post('/panel/setting/update', { data: settings })).json();
  expect(saved.success, saved.msg).toBe(true);
  const restarted = await (await request.post('/panel/setting/restartPanel')).json();
  expect(restarted.success, restarted.msg).toBe(true);

  // The restart comes three seconds after the answer; then the panel serves
  // again and the new bot sets its commands.
  await new Promise((resolve) => setTimeout(resolve, 4000));
  await expect
    .poll(
      async () => {
        try {
          return (await request.get('/')).status();
        } catch {
          return 0;
        }
      },
      { timeout: 30_000 },
    )
    .toBe(200);
  await expect
    .poll(async () => (await botSent(request)).some((s) => s.method === 'setMyCommands'), { timeout: 30_000 })
    .toBe(true);
}

/** A person pressing Start in the bot's chat with the deep link's token. */
export async function pressStart(request: APIRequestContext, from: { id: number; username: string }, token: string) {
  const text = `/start ${token}`;
  const res = await request.post(`${FAKEBOT_URL}/control/updates`, {
    data: {
      message: {
        message_id: 1,
        date: Math.floor(Date.now() / 1000),
        chat: { id: from.id, type: 'private' },
        from: { id: from.id, is_bot: false, first_name: 'E2E', username: from.username },
        text,
        entities: [{ type: 'bot_command', offset: 0, length: '/start'.length }],
      },
    },
  });
  expect(res.ok()).toBe(true);
}

/** A person's text message in their private chat with the bot; a leading /command is marked as one. */
export async function sendText(request: APIRequestContext, from: { id: number; username: string }, text: string) {
  const command = text.match(/^\/\w+/);
  const res = await request.post(`${FAKEBOT_URL}/control/updates`, {
    data: {
      message: {
        message_id: Math.floor(Math.random() * 1e6) + 100,
        date: Math.floor(Date.now() / 1000),
        chat: { id: from.id, type: 'private' },
        from: { id: from.id, is_bot: false, first_name: 'E2E', username: from.username },
        text,
        ...(command ? { entities: [{ type: 'bot_command', offset: 0, length: command[0].length }] } : {}),
      },
    },
  });
  expect(res.ok()).toBe(true);
}

/** A person's press on a button of the bot's message. */
export async function pressButton(
  request: APIRequestContext,
  from: { id: number; username: string },
  messageId: number,
  data: string,
) {
  const res = await request.post(`${FAKEBOT_URL}/control/updates`, {
    data: {
      callback_query: {
        id: String(Math.floor(Math.random() * 1e9)),
        from: { id: from.id, is_bot: false, first_name: 'E2E', username: from.username },
        chat_instance: '1',
        data,
        message: { message_id: messageId, date: Math.floor(Date.now() / 1000), chat: { id: from.id, type: 'private' } },
      },
    },
  });
  expect(res.ok()).toBe(true);
}
