import { createHmac } from 'node:crypto';
import { expect, test } from '../fixtures/panel';
import {
  botSent,
  pressButton,
  sendText,
  startTelegramBot,
  TG_BOT_TOKEN,
  TG_NOTIFY_CHANNEL,
  TG_SUB_URL,
  type Sent,
} from '../fixtures/tg-panel';
import type { APIRequestContext } from '@playwright/test';

// A request for a subscription (#220, docs/spec/users.md §12), against
// panel-tg, whose bot runs on fakebot (fixtures/tg-panel.ts): someone with
// no subscription presses «Leave a request» and gets the captcha step; the
// captcha page — here the panel's own sub server, which serves it as the
// active edge does — runs as the Mini App with Telegram's initData, the
// widget solves the challenge, the panel verifies it and the bot asks for
// the comment in the person's chat; the comment sends the request, which
// the notification channel hears of. The same solution again, and a forged
// initData, are refused.

const applicant = { id: 5550077, username: 'e2e_applicant' };

/** Telegram's initData for the person, signed with the bot's token as core.telegram.org/bots/webapps describes. */
function initDataOf(user: { id: number; username: string }, token: string): string {
  const fields: Record<string, string> = {
    auth_date: String(Math.floor(Date.now() / 1000)),
    query_id: 'AAE2E',
    user: JSON.stringify({ id: user.id, first_name: 'E2E', username: user.username }),
  };
  const check = Object.keys(fields)
    .sort()
    .map((k) => `${k}=${fields[k]}`)
    .join('\n');
  const secret = createHmac('sha256', 'WebAppData').update(token).digest();
  const hash = createHmac('sha256', secret).update(check).digest('hex');
  return new URLSearchParams({ ...fields, hash }).toString();
}

/** The last message the bot sent or edited in the person's chat that has text. */
async function lastTo(request: APIRequestContext, id: number, part: string): Promise<Sent | undefined> {
  return (await botSent(request)).filter((s) => Number(s.chat_id) === id && s.text.includes(part)).pop();
}

/** The captcha page on panel-tg's sub server, under its subscription path (random on a fresh panel). */
async function captchaBase(request: APIRequestContext): Promise<string> {
  const all = await (await request.post('/panel/setting/all')).json();
  expect(all.success, all.msg).toBe(true);
  return `${TG_SUB_URL}${all.obj.subPath}captcha`;
}

function buttonData(sent: Sent, label: string): string {
  for (const row of sent.reply_markup?.inline_keyboard || []) {
    for (const b of row) if (b.text.includes(label) && b.callback_data) return b.callback_data;
  }
  throw new Error(`no button ${label} in ${JSON.stringify(sent.reply_markup)}`);
}

test.describe('requests: the captcha and the applicant', () => {
  test('leave a request through the captcha Mini App, a comment, and the channel', async ({ page, authedRequest: request }) => {
    await startTelegramBot(request);

    // No subscription: «Leave a request» leads to the captcha step.
    await sendText(request, applicant, '/start');
    await expect.poll(async () => !!(await lastTo(request, applicant.id, 'no subscription')), { timeout: 15_000 }).toBe(true);
    const home = (await lastTo(request, applicant.id, 'no subscription'))!;
    await pressButton(request, applicant, home.message_id!, buttonData(home, 'Leave a request'));
    await expect.poll(async () => !!(await lastTo(request, applicant.id, 'Check')), { timeout: 15_000 }).toBe(true);

    // The Mini App: the page with the person's initData in the fragment.
    const captcha = await captchaBase(request);
    const initData = initDataOf(applicant, TG_BOT_TOKEN);
    await page.goto(`${captcha}#tgWebAppData=${encodeURIComponent(initData)}`);
    const verified = page.waitForRequest((r) => r.url() === `${captcha}/verify` && r.method() === 'POST');
    await page.getByRole('checkbox').click();
    await expect(page.getByTestId('captcha-status')).toContainText('Проверка пройдена', { timeout: 30_000 });

    // The bot asks for the comment in the person's chat.
    await expect.poll(async () => !!(await lastTo(request, applicant.id, 'Comment')), { timeout: 15_000 }).toBe(true);

    // The same solution again, and a forged initData: refused.
    const post = JSON.parse((await verified).postData() || '{}');
    expect(post.initData).toBe(initData);
    const again = await request.post(`${captcha}/verify`, { data: post });
    expect(again.status()).toBe(400);
    const forged = await request.post(`${captcha}/verify`, {
      data: { ...post, initData: initDataOf(applicant, '654321:' + 'x'.repeat(35)) },
    });
    expect(forged.status()).toBe(403);

    // The comment sends the request; the channel hears of it.
    await sendText(request, applicant, 'from the e2e run');
    await expect
      .poll(async () => !!(await lastTo(request, applicant.id, 'Request sent')), { timeout: 15_000 })
      .toBe(true);
    const channel = (await botSent(request)).filter((s) => s.chat_id === TG_NOTIFY_CHANNEL).map((s) => s.text);
    expect(channel.some((t) => t.includes('New subscription request from @e2e_applicant') && t.includes('from the e2e run'))).toBe(
      true,
    );
  });

  test('the page opened outside Telegram says so and shows no widget', async ({ page, authedRequest: request }) => {
    await page.goto(await captchaBase(request));
    await expect(page.getByTestId('captcha-status')).toContainText('«Пройти проверку»');
    await expect(page.getByTestId('captcha-widget')).toBeHidden();
  });
});
