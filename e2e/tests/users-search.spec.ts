import { createInbound } from '../fixtures/inbound';
import { expect, test } from '../fixtures/panel';
import type { APIRequestContext, Locator, Page } from '@playwright/test';

// The users page search (#211, decision #186 points 5–7): one field, the
// service's fuzzy search through GET /panel/api/users/search — best first,
// typos forgiven — and the «Email» (contact email) column. Specs share one
// panel, so every name and port here is this file's own.

/** An Ant Design input by data-testid, on the <input> or its wrapper. */
function field(scope: Page | Locator, testId: string): Locator {
  return scope.locator(`input[data-testid="${testId}"], [data-testid="${testId}"] input`).first();
}

/** The names in the users table, top to bottom. */
function shownNames(page: Page): Locator {
  return page.getByTestId(/^user-name-/);
}

async function createUser(request: APIRequestContext, data: Record<string, unknown>) {
  const body = await (await request.post('/panel/api/users/create', { data })).json();
  expect(body.success, body.msg).toBe(true);
}

test.describe('users page search', () => {
  test('finds users fuzzily, best first, by name and contact email, and shows the contact email', async ({
    authedPage: page,
    authedRequest: request,
  }) => {
    await createUser(request, { name: 'e2e-srch-olga-typo-x' });
    await createUser(request, { name: 'e2e-srch-olga', contactEmail: 'olga.k@example.org' });
    await createUser(request, { name: 'e2e-srch-olga-2' });
    await createUser(request, { name: 'e2e-srch-olha' });

    // The API ranks on its own: exact, start, then the typo.
    const api = await (await request.get('/panel/api/users/search?q=E2E-SRCH-OLGA')).json();
    expect(api.success, api.msg).toBe(true);
    expect(api.obj.map((u: { name: string }) => u.name)).toEqual([
      'e2e-srch-olga',
      'e2e-srch-olga-2',
      'e2e-srch-olga-typo-x',
      'e2e-srch-olha',
    ]);

    await page.goto('/panel/users');
    await expect(page.getByRole('columnheader', { name: 'Email', exact: true })).toBeVisible();
    const olga = page.getByRole('row').filter({ has: page.getByTestId('user-name-e2e-srch-olga') });
    await expect(olga.getByTestId('user-contact-email')).toHaveText('olga.k@example.org');

    // The page shows the service's order, not the table's.
    await field(page, 'users-search').fill('e2e-srch-olga');
    await expect(shownNames(page)).toHaveText([
      'e2e-srch-olga',
      'e2e-srch-olga-2',
      'e2e-srch-olga-typo-x',
      'e2e-srch-olha',
    ]);

    // A typo in the contact email still finds its user.
    await field(page, 'users-search').fill('olga.k@exampel.org');
    await expect(shownNames(page)).toHaveText(['e2e-srch-olga']);

    // Nobody: an empty table.
    await field(page, 'users-search').fill('e2e-srch-nobody-qqq');
    await expect(shownNames(page)).toHaveCount(0);

    // Clearing the search brings every user back.
    await field(page, 'users-search').fill('');
    await expect(page.getByTestId('user-name-robot')).toBeVisible();
    await expect(page.getByTestId('user-name-e2e-srch-olha')).toBeVisible();
  });

  test('the client modal calls a client email «xray email»', async ({ authedPage: page, authedRequest: request }) => {
    const id = await createInbound(request, 'e2e-srch-label', 24411);

    await page.goto('/panel/inbounds');
    await page.getByTestId(`inbound-actions-${id}`).click();
    await page.getByRole('menuitem', { name: 'Add Client' }).click();
    await expect(page.getByRole('dialog').getByText('xray email', { exact: true })).toBeVisible();
  });
});
