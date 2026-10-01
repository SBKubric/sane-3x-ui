import { createInbound } from '../fixtures/inbound';
import { expect, test } from '../fixtures/panel';
import { botSent, startTelegramBot } from '../fixtures/tg-panel';

// The link broadcast (#222, docs/spec/users.md §13), against panel-tg,
// whose bot runs on fakebot (fixtures/tg-panel.ts): «Send links» on the
// users page asks first, then the bot sends the user with Telegram their
// subscription link — the QR as a photo, the link and how to update it in
// the caption, «📱 My subscription» — and the admin the report. The user
// without Telegram gets nothing and is counted in the answer.

const person = { id: 5550222, username: 'e2e_link_person' };
const ADMIN_CHAT = '4242001'; // tgBotChatId of startTelegramBot

test.describe('users page: send the subscription links', () => {
  test('asks, sends the link with its QR to the user with Telegram, reports to the admin', async ({
    authedPage: page,
    authedRequest: request,
  }) => {
    await startTelegramBot(request);
    const inboundId = await createInbound(request, 'e2e-slk-nl', 24421);
    const created = await (
      await request.post('/panel/api/users/create', { data: { name: 'e2e-slk-ivan', tgId: person.id, inboundIds: [inboundId] } })
    ).json();
    expect(created.success, created.msg).toBe(true);
    const subId: string = created.obj.subId;
    const off = await (await request.post('/panel/api/users/create', { data: { name: 'e2e-slk-anna', inboundIds: [inboundId] } })).json();
    expect(off.success, off.msg).toBe(true);
    // The inbound is disabled, so are its clients: enable both users.
    for (const sub of [subId, off.obj.subId]) {
      const enabled = await (await request.post(`/panel/api/users/enable/${encodeURIComponent(sub)}`, { data: { enable: true } })).json();
      expect(enabled.success, enabled.msg).toBe(true);
    }

    await page.goto('/panel/users');
    await page.getByTestId('users-broadcast').click();
    const dialog = page.getByRole('dialog').filter({ hasText: 'Send every enabled user with Telegram the subscription link?' });
    await expect(dialog).toBeVisible();
    const before = (await botSent(request)).length;
    await dialog.getByRole('button', { name: 'Send links' }).click();
    await expect(page.getByText(/Broadcast #\d+ started: \d+ users, \d+ without Telegram/)).toBeVisible();

    // The person's message: the QR as a photo, the link in the caption, «My subscription».
    await expect
      .poll(async () => (await botSent(request)).slice(before).filter((s) => String(s.chat_id) === String(person.id)).length, {
        timeout: 15_000,
      })
      .toBe(1);
    const sent = (await botSent(request)).slice(before);
    const photo = sent.find((s) => String(s.chat_id) === String(person.id))!;
    expect(photo.method).toBe('sendPhoto');
    expect(photo.text).toContain('Your subscription link has changed');
    expect(photo.text).toMatch(new RegExp(`<code>https?://[^<]+/${subId}</code>`));
    expect(JSON.stringify(photo.reply_markup)).toContain('📱 My subscription');

    // The report to the admin.
    await expect
      .poll(async () =>
        (await botSent(request))
          .slice(before)
          .some((s) => String(s.chat_id) === ADMIN_CHAT && /Link broadcast #\d+<\/b> is over/.test(s.text) && s.text.includes('e2e-slk-anna')),
      )
      .toBe(true);
  });
});
