import { type Page } from '@playwright/test';
import { expect, test } from '../fixtures/panel';

/** The public address field: an <a-input>, the data-testid on the input or its wrapper. */
function addressField(page: Page) {
  return page.locator('input[data-testid="sub-public-url"], [data-testid="sub-public-url"] input').first();
}

async function openSubscriptionTab(page: Page) {
  await page.goto('/panel/settings');
  // The tab's name carries its icon's label; the anchor leaves out «Subscription (Formats)».
  await page.getByRole('tab', { name: /Subscription$/ }).click();
  await expect(addressField(page)).toBeVisible();
}

async function save(page: Page) {
  await page.getByRole('button', { name: 'Save', exact: true }).click();
}

async function savedAs(page: Page, value: string) {
  await save(page);
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
  await openSubscriptionTab(page);
  await expect(addressField(page)).toHaveValue(value);
}

/** The subscription link the users page shows for the user named name. */
function userLink(page: Page, name: string) {
  return page
    .getByRole('row')
    .filter({ has: page.getByTestId(`user-name-${name}`) })
    .getByTestId('user-sub-link');
}

// The public subscription address of the Subscription settings tab (#224):
// when set, every subscription link the panel hands out goes through it.
test.describe('public subscription address', () => {
  test('is unset on a fresh panel, refuses what is not an origin, and leads the links once set', async ({
    authedPage: page,
    authedRequest: request,
  }) => {
    const name = `e2e-public-${Date.now()}`;
    const created = await (await request.post('/panel/api/users/create', { data: { name } })).json();
    expect(created.success, created.msg).toBe(true);
    const subId: string = created.obj.subId;

    try {
      await openSubscriptionTab(page);
      // A fresh panel has none: the links name the panel's own sub server.
      await expect(addressField(page)).toHaveValue('');
      await page.goto('/panel/users');
      await expect(userLink(page, name)).toHaveText(new RegExp(`/${subId}$`));
      await expect(userLink(page, name)).not.toHaveText(/sub\.e2e\.example\.com/);

      // A host without a scheme, or with a path, is refused and nothing is stored.
      await openSubscriptionTab(page);
      for (const bad of ['sub.e2e.example.com', 'https://sub.e2e.example.com/sub/']) {
        await addressField(page).fill(bad);
        await save(page);
        await expect(page.getByText(/public subscription address must be/).first()).toBeVisible();
      }
      await openSubscriptionTab(page);
      await expect(addressField(page)).toHaveValue('');

      // An origin is stored as one: lower case, no trailing slash.
      await addressField(page).fill('https://Sub.E2E.example.com/');
      await savedAs(page, 'https://sub.e2e.example.com');

      // The users page shows the link through it, on the panel's own path.
      await page.goto('/panel/users');
      const link = userLink(page, name);
      await expect(link).toHaveText(new RegExp(`^https://sub\\.e2e\\.example\\.com/[^/]+/${subId}$`));
      await expect(link).toHaveAttribute('href', new RegExp(`^https://sub\\.e2e\\.example\\.com/[^/]+/${subId}$`));

      // Cleared, the links are what they were.
      await openSubscriptionTab(page);
      await addressField(page).fill('');
      await savedAs(page, '');
      await page.goto('/panel/users');
      await expect(userLink(page, name)).toHaveText(new RegExp(`/${subId}$`));
      await expect(userLink(page, name)).not.toHaveText(/sub\.e2e\.example\.com/);
    } finally {
      await request.post(`/panel/api/users/del/${subId}`);
    }
  });
});
