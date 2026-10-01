import { type APIRequestContext, type Page } from '@playwright/test';
import { expect, test } from '../fixtures/panel';

/** The panel's sub server, where boxes join and poll (docker-compose.yml publishes it). */
const SUB_URL = process.env.E2E_SUB_URL || 'http://127.0.0.1:2096';
const HOP = 'e2e-trusted-edge';

/** The trusted addresses field: an <a-input>, the data-testid on the input or its wrapper. */
function trustedField(page: Page) {
  return page.locator('input[data-testid="front-trusted-addrs"], [data-testid="front-trusted-addrs"] input').first();
}

async function openSubscriptionTab(page: Page) {
  await page.goto('/panel/settings');
  await page.getByRole('tab', { name: /Subscription$/ }).click();
  await expect(trustedField(page)).toBeVisible();
}

async function save(page: Page) {
  await page.getByRole('button', { name: 'Save', exact: true }).click();
}

async function savedAs(page: Page, value: string) {
  await save(page);
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
  await openSubscriptionTab(page);
  await expect(trustedField(page)).toHaveValue(value);
}

async function removeHop(request: APIRequestContext) {
  const list = await (await request.get('/panel/api/chain/list')).json();
  const hop = (list.obj?.hops || []).find((h: { name: string }) => h.name === HOP);
  if (hop) {
    await request.post(`/panel/api/chain/del/${hop.id}`, { data: { force: true, skipDrain: true } });
  }
}

/** The document the hop gets on its next poll, as a box asks for it. */
async function documentOf(request: APIRequestContext, secret: string) {
  const res = await request.get(`${SUB_URL}/chain/v1/document`, { headers: { Authorization: `Bearer ${secret}` } });
  expect(res.status()).toBe(200);
  return res.json();
}

// The front's trusted addresses of the Subscription settings tab (#228): the
// subscription showcase calls the edges from one address, so every hop's
// front must neither limit nor ban it. The list reaches the hops in their
// chain document, with a new revision.
test.describe('front trusted addresses', () => {
  test('are unset on a fresh panel, refuse a name, and reach the hops with a new revision', async ({
    authedPage: page,
    authedRequest,
    request,
  }) => {
    await removeHop(authedRequest);
    try {
      // An edge enters the chain the way a real box does.
      const added = await (
        await authedRequest.post('/panel/api/chain/add', { data: { name: HOP, host: '198.51.100.61', role: 'edge' } })
      ).json();
      expect(added.success, added.msg).toBe(true);
      const joined = await request.post(`${SUB_URL}/chain/v1/join`, { data: { token: added.obj.joinToken } });
      expect(joined.status()).toBe(200);
      const { secret, document: first } = await joined.json();
      expect(first.frontTrustedAddrs).toBeUndefined();

      await openSubscriptionTab(page);
      await expect(trustedField(page)).toHaveValue('');

      // A name is refused, and nothing is stored.
      await trustedField(page).fill('showcase.e2e.example.com');
      await save(page);
      await expect(page.getByText(/front trusted address/).first()).toBeVisible();
      await openSubscriptionTab(page);
      await expect(trustedField(page)).toHaveValue('');

      // A list is stored normalised.
      await trustedField(page).fill('203.0.113.5, 2001:DB8::/48 203.0.113.5');
      await savedAs(page, '203.0.113.5,2001:db8::/48');

      // The hop's next document names it, under a new revision.
      const trusted = await documentOf(request, secret);
      expect(trusted.frontTrustedAddrs).toEqual(['203.0.113.5', '2001:db8::/48']);
      expect(trusted.revision).toBeGreaterThan(first.revision);

      // Cleared, the document has none again.
      await openSubscriptionTab(page);
      await trustedField(page).fill('');
      await savedAs(page, '');
      const cleared = await documentOf(request, secret);
      expect(cleared.frontTrustedAddrs).toBeUndefined();
      expect(cleared.revision).toBeGreaterThan(trusted.revision);
    } finally {
      await removeHop(authedRequest);
    }
  });
});
