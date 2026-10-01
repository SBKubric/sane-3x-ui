import { type Page } from '@playwright/test';
import { expect, test } from '../fixtures/panel';

/** An <a-input> or <a-input-number> field: the data-testid on the input or its wrapper. */
function field(page: Page, testId: string) {
  return page.locator(`input[data-testid="${testId}"], [data-testid="${testId}"] input`).first();
}

async function openSubscriptionTab(page: Page) {
  await page.goto('/panel/settings');
  // The tab's name carries its icon's label; the anchor leaves out «Subscription (Formats)».
  await page.getByRole('tab', { name: /Subscription$/ }).click();
  await expect(field(page, 'vpn-name')).toBeVisible();
}

async function save(page: Page) {
  await page.getByRole('button', { name: 'Save', exact: true }).click();
}

async function saved(page: Page) {
  await save(page);
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
  await openSubscriptionTab(page);
}

// The VPN name settings of the Subscription tab (#225): the name, the DNSExit
// API key (a secret: shown masked, never given back), the record's TTL and
// the domain's expiry date.
test.describe('VPN name settings', () => {
  test('are empty on a fresh panel, refuse what is not a name or a date, and keep the key secret', async ({
    authedPage: page,
    authedRequest: request,
  }) => {
    try {
      await openSubscriptionTab(page);
      await expect(field(page, 'vpn-name')).toHaveValue('');
      await expect(field(page, 'dnsexit-api-key')).toHaveValue('');
      await expect(field(page, 'vpn-name-ttl')).toHaveValue('5');
      await expect(field(page, 'domain-expiry')).toHaveValue('');

      // An address is not a name, a date must be YYYY-MM-DD: refused, nothing stored.
      await field(page, 'vpn-name').fill('203.0.113.7');
      await save(page);
      await expect(page.getByText(/VPN name must be/).first()).toBeVisible();
      await openSubscriptionTab(page);
      await field(page, 'domain-expiry').fill('01.03.2027');
      await save(page);
      await expect(page.getByText(/domain expiry date must be YYYY-MM-DD/).first()).toBeVisible();
      await openSubscriptionTab(page);
      await expect(field(page, 'vpn-name')).toHaveValue('');
      await expect(field(page, 'domain-expiry')).toHaveValue('');

      // Stored tidy; the key comes back as the mask, in the form and the API.
      await field(page, 'vpn-name').fill('VPN.E2E.example.com.');
      await field(page, 'dnsexit-api-key').fill('e2e-secret-key');
      await field(page, 'vpn-name-ttl').fill('7');
      await field(page, 'domain-expiry').fill('2027-03-01');
      await saved(page);
      await expect(field(page, 'vpn-name')).toHaveValue('vpn.e2e.example.com');
      await expect(field(page, 'dnsexit-api-key')).toHaveValue('********');
      await expect(field(page, 'vpn-name-ttl')).toHaveValue('7');
      await expect(field(page, 'domain-expiry')).toHaveValue('2027-03-01');
      const all = await (await request.post('/panel/setting/all')).json();
      expect(all.success, all.msg).toBe(true);
      expect(all.obj.dnsExitApiKey).toBe('********');
      expect(JSON.stringify(all.obj)).not.toContain('e2e-secret-key');
      expect(all.obj.vpnName).toBe('vpn.e2e.example.com');

      // Another save of the form keeps the key behind the mask.
      await field(page, 'vpn-name-ttl').fill('8');
      await saved(page);
      await expect(field(page, 'dnsexit-api-key')).toHaveValue('********');
      await expect(field(page, 'vpn-name-ttl')).toHaveValue('8');
    } finally {
      // Cleared, as a fresh panel has them.
      await openSubscriptionTab(page);
      await field(page, 'vpn-name').fill('');
      await field(page, 'dnsexit-api-key').fill('');
      await field(page, 'vpn-name-ttl').fill('5');
      await field(page, 'domain-expiry').fill('');
      await saved(page);
      await expect(field(page, 'dnsexit-api-key')).toHaveValue('');
      await expect(field(page, 'vpn-name')).toHaveValue('');
    }
  });
});
