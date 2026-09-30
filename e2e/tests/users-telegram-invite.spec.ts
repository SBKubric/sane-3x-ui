import { createInbound } from '../fixtures/inbound';
import { expect, test } from '../fixtures/panel';
import { botSent, pressStart, startTelegramBot, TG_BOT_USERNAME, TG_NOTIFY_CHANNEL } from '../fixtures/tg-panel';
import type { APIRequestContext, Locator, Page } from '@playwright/test';

// Binding a user's Telegram (#219, docs/spec/users.md §11), against panel-tg,
// whose bot runs on fakebot (fixtures/tg-panel.ts): the users page makes a
// user's invite link with its copy button and QR and reissues it; a person
// pressing Start with the old link gets nothing, with the new one gets linked
// — the user and its client carry the id, the person sees the subscription,
// the notification channel hears it. Then another user takes the person's
// @nick, which the bot has seen by now: refused as ivan's, moved after a
// confirmation; a nick the bot never saw asks for an invite link.

type User = { subId: string; name: string; tgId: number; clients: { tgId: number }[] };

const petrov = { id: 5550001, username: 'e2e_petrov' };

function userRow(page: Page, name: string): Locator {
  return page.getByRole('row').filter({ has: page.getByTestId(`user-name-${name}`) });
}

/** The entry field is an `<a-input>`: the testid lands on the input or a wrapper. */
function entryField(page: Page): Locator {
  return page.locator('input[data-testid="users-tg-entry"], [data-testid="users-tg-entry"] input').first();
}

async function createUser(request: APIRequestContext, name: string, inboundId: number): Promise<User> {
  const body = await (await request.post('/panel/api/users/create', { data: { name, inboundIds: [inboundId] } })).json();
  expect(body.success, body.msg).toBe(true);
  return body.obj as User;
}

async function getUser(request: APIRequestContext, subId: string): Promise<User> {
  const body = await (await request.get(`/panel/api/users/get/${encodeURIComponent(subId)}`)).json();
  expect(body.success, body.msg).toBe(true);
  return body.obj as User;
}

test.describe('users page: Telegram invite link', () => {
  test('the link and its QR, a reissue, Start with it, then @nick and move', async ({
    authedPage: page,
    authedRequest: request,
  }) => {
    await startTelegramBot(request);
    const inboundId = await createInbound(request, 'e2e-inv-nl', 24401);
    const ivan = await createUser(request, 'e2e-inv-ivan', inboundId);

    // No Telegram yet: the row says so and opens the Telegram modal.
    await page.goto('/panel/users');
    const row = userRow(page, 'e2e-inv-ivan');
    await row.getByTestId('user-tg-unlinked').click();
    await expect(page.getByTestId('users-tg-unlinked')).toBeVisible();

    // The invite link: the bot's deep link with a token, a copy button, a QR.
    await page.getByTestId('users-tg-invite').click();
    const link = page.getByTestId('users-tg-invite-link');
    await expect(link).toHaveText(new RegExp(`^https://t\\.me/${TG_BOT_USERNAME}\\?start=[A-Za-z0-9_-]{43}$`));
    const oldToken = (await link.innerText()).split('start=')[1];
    await expect(page.getByTestId('users-tg-invite-copy')).toBeVisible();
    const qr = page.getByTestId('users-tg-invite-qr');
    await expect(qr).toBeVisible();
    // Drawn: a canvas of the QR's size, dark modules on white.
    await expect
      .poll(() =>
        qr.evaluate((canvas: HTMLCanvasElement) => {
          const data = canvas.getContext('2d')!.getImageData(0, 0, canvas.width, canvas.height).data;
          let dark = 0;
          for (let i = 0; i < data.length; i += 4) if (data[i] < 128) dark++;
          return canvas.width >= 100 && dark > 1000;
        }),
      )
      .toBe(true);

    // Reissue: another link; the old one links nobody.
    await page.getByTestId('users-tg-invite-reissue').click();
    await expect(link).not.toHaveText(new RegExp(oldToken));
    const token = (await link.innerText()).split('start=')[1];
    expect(token).toMatch(/^[A-Za-z0-9_-]{43}$/);

    await pressStart(request, petrov, oldToken);
    await expect
      .poll(async () => (await botSent(request)).some((s) => s.chat_id === petrov.id && s.text.includes('used up or was replaced')))
      .toBe(true);
    expect((await getUser(request, ivan.subId)).tgId).toBe(0);

    await pressStart(request, petrov, token);
    await expect.poll(async () => (await getUser(request, ivan.subId)).tgId, { timeout: 15_000 }).toBe(petrov.id);
    expect((await getUser(request, ivan.subId)).clients.map((c) => c.tgId)).toEqual([petrov.id]);
    const sent = await botSent(request);
    expect(sent.some((s) => s.chat_id === petrov.id && s.text.includes('My subscription</b> · e2e-inv-ivan'))).toBe(true);
    expect(sent.some((s) => s.chat_id === TG_NOTIFY_CHANNEL && s.text === '🔗 e2e-inv-ivan linked Telegram @e2e_petrov')).toBe(
      true,
    );

    // The page shows ivan linked now.
    await page.goto('/panel/users');
    await expect(row.getByTestId('user-tg-unlinked')).toHaveCount(0);
    await expect(row.getByTestId('user-telegram')).toBeVisible();

    // anna by @nick: one the bot never saw asks for an invite link; petrov's
    // is ivan's, and moves here after a confirmation.
    const anna = await createUser(request, 'e2e-inv-anna', inboundId);
    await page.goto('/panel/users');
    await userRow(page, 'e2e-inv-anna').getByTestId('user-tg-unlinked').click();
    await page.getByTestId('users-tg-enter').click();
    await entryField(page).fill('@e2e_nobody');
    await page.getByTestId('users-tg-entry-ok').click();
    await expect(page.getByTestId('users-tg-nick-unknown')).toContainText('has not written to the bot yet');

    await entryField(page).fill('@E2E_Petrov');
    await page.getByTestId('users-tg-entry-ok').click();
    const dialog = page.getByRole('dialog').filter({ hasText: 'e2e-inv-ivan' }).last();
    await expect(dialog).toContainText(`Telegram ${petrov.id} is linked to user e2e-inv-ivan`);
    await dialog.getByRole('button', { name: 'Move here' }).click();
    await expect(page.getByTestId('users-tg-current')).toContainText(String(petrov.id));
    expect((await getUser(request, anna.subId)).tgId).toBe(petrov.id);
    const moved = await getUser(request, ivan.subId);
    expect(moved.tgId).toBe(0);
    expect(moved.clients.map((c) => c.tgId)).toEqual([0]);
  });
});
