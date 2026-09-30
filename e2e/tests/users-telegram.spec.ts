import { randomUUID } from 'node:crypto';
import { createInbound } from '../fixtures/inbound';
import { expect, test } from '../fixtures/panel';
import type { APIRequestContext, Locator, Page } from '@playwright/test';

// The user's Telegram (#186, docs/spec/users.md §11): a user whose clients
// disagree on the Telegram ID shows «⚠️ Telegram: conflict» on the users page;
// its Telegram modal lists the ids with who else has each, assigns one nobody
// else owns, and unlinks Telegram from the user and all its clients.
// Specs share one panel, so every name, id and port here is this file's own.

type Client = { name: string; tgId: number };
type User = { subId: string; name: string; tgId: number; tgConflict: boolean; clients: Client[] };

function userRow(page: Page, name: string): Locator {
  return page.getByRole('row').filter({ has: page.getByTestId(`user-name-${name}`) });
}

/** Adds an xray client carrying a Telegram ID through the inbounds API. */
async function addClient(request: APIRequestContext, inboundId: number, email: string, subId: string, tgId: number) {
  const res = await request.post('/panel/api/inbounds/addClient', {
    form: {
      id: String(inboundId),
      settings: JSON.stringify({ clients: [{ id: randomUUID(), email, subId, tgId, enable: false }] }),
    },
  });
  const body = await res.json();
  expect(body.success, body.msg).toBe(true);
}

async function getUser(request: APIRequestContext, subId: string): Promise<User> {
  const body = await (await request.get(`/panel/api/users/get/${encodeURIComponent(subId)}`)).json();
  expect(body.success, body.msg).toBe(true);
  return body.obj as User;
}

test.describe('users page: Telegram', () => {
  test('a conflict: see it, assign an id nobody else owns, then unlink', async ({
    authedPage: page,
    authedRequest: request,
  }) => {
    const id = await createInbound(request, 'e2e-tg-nl', 24331);
    // holder owns 918002 before ivan's clients arrive with it.
    const holder = await (
      await request.post('/panel/api/users/create', { data: { name: 'e2e-tg-holder', tgId: 918002, inboundIds: [id] } })
    ).json();
    expect(holder.success, holder.msg).toBe(true);
    await addClient(request, id, 'e2e-tg-ivan-a', 'e2e-tg-ivan', 918001);
    await addClient(request, id, 'e2e-tg-ivan-b', 'e2e-tg-ivan', 918002);

    await page.goto('/panel/users');
    const ivan = userRow(page, 'e2e-tg-ivan-a');
    await expect(ivan.getByTestId('user-tg-conflict')).toBeVisible();
    await expect(userRow(page, 'e2e-tg-holder').getByTestId('user-tg-conflict')).toHaveCount(0);

    // The modal: both ids, 918002 is holder's and cannot be assigned.
    await ivan.getByTestId('user-tg-conflict').click();
    await expect(page.getByTestId('users-tg-candidate-918001')).toBeVisible();
    await expect(page.getByTestId('users-tg-candidate-918002')).toBeVisible();
    await expect(page.getByRole('dialog').getByText('e2e-tg-holder', { exact: true })).toBeVisible();
    await expect(page.getByTestId('users-tg-assign-918002')).toHaveCount(0);

    // Assign 918001: the user and both clients carry it, the conflict is gone.
    await page.getByTestId('users-tg-assign-918001').click();
    await expect(page.getByTestId('users-tg-current')).toContainText('918001');
    await expect(ivan.getByTestId('user-tg-conflict')).toHaveCount(0);
    const assigned = await getUser(request, 'e2e-tg-ivan');
    expect(assigned.tgId).toBe(918001);
    expect(assigned.tgConflict).toBe(false);
    expect(assigned.clients.map(c => c.tgId)).toEqual([918001, 918001]);

    // Unlink asks first, then the user and its clients carry none.
    await page.getByTestId('users-tg-unlink').click();
    await page.getByRole('button', { name: 'Unlink Telegram', exact: true }).last().click();
    await expect(page.getByTestId('users-tg-current')).not.toContainText('918001');
    const unlinked = await getUser(request, 'e2e-tg-ivan');
    expect(unlinked.tgId).toBe(0);
    expect(unlinked.clients.map(c => c.tgId)).toEqual([0, 0]);
    await page.getByTestId('users-tg-close').click();
    await expect(ivan.getByTestId('user-telegram')).toHaveCount(0);
  });
});
