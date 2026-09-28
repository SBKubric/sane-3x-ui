import { randomUUID } from 'node:crypto';
import { createInbound } from '../fixtures/inbound';
import { expect, test } from '../fixtures/panel';
import type { APIRequestContext, Locator, Page } from '@playwright/test';

// The users page (#170, docs/spec/users.md §9): what the bot does to users,
// done through the panel — every action goes through /panel/api/users.
// Specs share one panel, so every name and port here is this file's own.

type Client = { name: string; inboundId: number; enable: boolean; subId: string };
type User = { subId: string; name: string; clients: Client[] };

/** An Ant Design input by data-testid: the attribute lands on the <input>
 *  itself or on the component's wrapper, depending on the component. */
function field(scope: Page | Locator, testId: string): Locator {
  return scope.locator(`input[data-testid="${testId}"], [data-testid="${testId}"] input`).first();
}

/** The table row of the user named name. */
function userRow(page: Page, name: string): Locator {
  return page.getByRole('row').filter({ has: page.getByTestId(`user-name-${name}`) });
}

/** Adds an xray client through the inbounds API; subId '' leaves it robot's. */
async function addClient(request: APIRequestContext, inboundId: number, email: string, subId = '') {
  const res = await request.post('/panel/api/inbounds/addClient', {
    form: {
      id: String(inboundId),
      settings: JSON.stringify({ clients: [{ id: randomUUID(), email, subId, enable: false }] }),
    },
  });
  const body = await res.json();
  expect(body.success, body.msg).toBe(true);
}

async function findUser(request: APIRequestContext, name: string): Promise<User> {
  const body = await (await request.get(`/panel/api/users/find?q=${encodeURIComponent(name)}`)).json();
  expect(body.success, body.msg).toBe(true);
  return body.obj as User;
}

test.describe('users page', () => {
  test('the sidebar opens the page, and the technical users are marked', async ({ authedPage }) => {
    // The sider and the mobile drawer render the same menu; the first is on screen.
    await authedPage.getByRole('menuitem', { name: 'Users' }).first().click();

    await expect(authedPage).toHaveURL(/\/panel\/users$/);
    await expect(authedPage.getByTestId('users-title')).toHaveText('Users');
    for (const name of ['robot', 'monitoring']) {
      const row = userRow(authedPage, name);
      await expect(row.getByTestId('user-technical')).toBeVisible();
      // Nobody switches, deletes or subscribes a technical user by hand.
      await expect(row.getByRole('switch')).toHaveCount(0);
      await expect(row.getByTestId('user-delete')).toHaveCount(0);
    }
  });

  test('a user: create with two inbounds, remove and add a protocol, switch off, assign from robot, delete', async ({
    authedPage: page,
    authedRequest: request,
  }) => {
    const nl = await createInbound(request, 'e2e-page-nl', 24311);
    const de = await createInbound(request, 'e2e-page-de', 24312);
    const fr = await createInbound(request, 'e2e-page-fr', 24313);
    // The panel never empties an xray inbound, so each keeps a bystander.
    await addClient(request, nl, 'e2e-page-bystander-nl', 'e2e-page-bystander-sub');
    await addClient(request, de, 'e2e-page-bystander-de', 'e2e-page-bystander-sub');
    await addClient(request, fr, 'e2e-page-bystander-fr', 'e2e-page-bystander-sub');
    // A client without a subscription belongs to robot until it is assigned.
    await addClient(request, fr, 'e2e-page-orphan');

    await page.goto('/panel/users');

    // Create: a name, two inbounds, a limit.
    await page.getByTestId('users-create').click();
    await field(page, 'users-create-name').fill('e2e-page-anna');
    await page.getByRole('checkbox', { name: 'e2e-page-nl (vless)' }).check();
    await page.getByRole('checkbox', { name: 'e2e-page-de (vless)' }).check();
    await field(page, 'users-create-total').fill('1');
    await page.getByTestId('users-create-ok').click();

    const anna = userRow(page, 'e2e-page-anna');
    await expect(anna.getByTestId(`user-proto-${nl}`)).toBeVisible();
    await expect(anna.getByTestId(`user-proto-${de}`)).toBeVisible();
    // 1 GB on each of two clients: the user's limit is their sum.
    await expect(anna.getByTestId('user-traffic')).toHaveText('0 B / 2.00 GB');
    const { subId } = await findUser(request, 'e2e-page-anna');
    await expect(anna.getByTestId('user-sub-link')).toContainText(subId);

    // The subscription's QR code.
    await anna.getByTestId('user-sub-qr').click();
    await expect(page.getByTestId('users-qr-canvas')).toBeVisible();
    await page.getByTestId('users-qr-close').click();

    // ➖ asks first, then the client in de is gone.
    await anna.getByTestId(`user-proto-remove-${de}`).click();
    await page.getByRole('button', { name: 'Remove', exact: true }).click();
    await expect(anna.getByTestId(`user-proto-${de}`)).toHaveCount(0);

    // ➕ offers only the inbounds the user is not in yet.
    await anna.getByTestId('user-proto-add').click();
    await expect(page.getByTestId(`users-add-inbound-${nl}`)).toHaveCount(0);
    await page.getByTestId(`users-add-inbound-${de}`).click();
    await expect(anna.getByTestId(`user-proto-${de}`)).toBeVisible();

    // The switch turns every client of the user off.
    await anna.getByRole('switch').click();
    // The switch follows the user, not the click: Ant Design drops
    // aria-checked when off, once the list is read back.
    await expect(anna.getByRole('switch')).not.toHaveAttribute('aria-checked', 'true');
    const off = await findUser(request, 'e2e-page-anna');
    expect(off.clients).toHaveLength(2);
    expect(off.clients.every(c => !c.enable)).toBe(true);

    // robot's client goes to anna, and keeps its name.
    const robot = userRow(page, 'robot');
    await robot.getByTestId('user-clients').click();
    await page.getByTestId('client-assign-e2e-page-orphan').click();
    await page.getByTestId('users-assign-select').click();
    await page.getByRole('option', { name: 'e2e-page-anna' }).click();
    await page.getByTestId('users-assign-ok').click();
    await expect(anna.getByTestId(`user-proto-${fr}`)).toBeVisible();
    await page.getByTestId('users-clients-close').click();
    const assigned = await findUser(request, 'e2e-page-orphan');
    expect(assigned.subId).toBe(subId);

    // Delete asks first, then the user and all its clients are gone.
    await anna.getByTestId('user-delete').click();
    await page.getByRole('button', { name: 'Delete', exact: true }).click();
    await expect(page.getByTestId('user-name-e2e-page-anna')).toHaveCount(0);
    const gone = await (await request.get(`/panel/api/users/get/${subId}`)).json();
    expect(gone.success).toBe(false);
  });

  test('removing the last client of an xray inbound is refused, and the page says why', async ({
    authedPage: page,
    authedRequest: request,
  }) => {
    const solo = await createInbound(request, 'e2e-page-solo', 24314);
    const created = await (
      await request.post('/panel/api/users/create', { data: { name: 'e2e-page-solo-user', inboundIds: [solo] } })
    ).json();
    expect(created.success, created.msg).toBe(true);

    await page.goto('/panel/users');
    const row = userRow(page, 'e2e-page-solo-user');
    await row.getByTestId(`user-proto-remove-${solo}`).click();
    await page.getByRole('button', { name: 'Remove', exact: true }).click();

    await expect(page.getByText('inbound e2e-page-solo would be left without clients')).toBeVisible();
    await expect(row.getByTestId(`user-proto-${solo}`)).toBeVisible();
  });

  test('the client modal names the user of a subId, and a refused save says why', async ({
    authedPage: page,
    authedRequest: request,
  }) => {
    const id = await createInbound(request, 'e2e-page-hint', 24315);
    await addClient(request, id, 'e2e-page-hint-bystander', 'e2e-page-hint-sub');
    const created = await (
      await request.post('/panel/api/users/create', { data: { name: 'e2e-page-olga', inboundIds: [id] } })
    ).json();
    expect(created.success, created.msg).toBe(true);
    const subId: string = created.obj.subId;

    await page.goto('/panel/inbounds');
    await page.getByTestId(`inbound-actions-${id}`).click();
    await page.getByRole('menuitem', { name: 'Add Client' }).click();

    // A fresh client gets a random subId: nobody's yet.
    const hint = page.getByTestId('client-sub-user');
    await expect(hint).toHaveText('User: new');
    await field(page, 'client-subid').fill(subId);
    await expect(hint).toHaveText('User: e2e-page-olga');

    // A technical key is never a subId: the save is refused, and the toast
    // carries the reason.
    await field(page, 'client-subid').fill('@robot');
    await expect(hint).toHaveText('User: new');
    await page.getByRole('button', { name: 'Add Client' }).click();
    await expect(page.getByText('subId "@robot" is reserved for a technical user')).toBeVisible();
  });
});
