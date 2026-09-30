import { type Page } from '@playwright/test';
import { createInbound } from '../fixtures/inbound';
import { expect, test } from '../fixtures/panel';

/** An <a-input-number>'s real input, whichever shape the antd build renders. */
function field(page: Page, testId: string) {
  return page.locator(`input[data-testid="${testId}"], [data-testid="${testId}"] input`).first();
}

/** The Telegram tab with its «Subscription requests» block open. */
async function openRequestDefaults(page: Page) {
  await page.goto('/panel/settings');
  await page.getByRole('tab', { name: 'Telegram Bot' }).click();
  await page.getByRole('button', { name: /Subscription requests/ }).click();
  await expect(page.getByTestId('sub-request-inbounds')).toBeVisible();
}

async function save(page: Page) {
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
}

// The request defaults of the Telegram settings tab (#221): what «✅ Approve»
// in the bot gives the user a request for a subscription makes.
test.describe('request defaults', () => {
  test('are every enabled inbound, 50 GB and 30 days, and keep what the admin sets', async ({
    authedPage,
    authedRequest,
  }) => {
    const remark = 'e2e-request-defaults';
    const inboundId = await createInbound(authedRequest, remark, 24501);
    try {
      await openRequestDefaults(authedPage);
      const inbounds = authedPage.getByTestId('sub-request-inbounds');
      // The select's placeholder stands for "none chosen": every enabled inbound.
      const allEnabled = inbounds.getByText('All enabled inbounds');

      // A fresh panel: no inbound chosen — every enabled one — 50 GB, 30 days.
      await expect(allEnabled).toBeVisible();
      await expect(field(authedPage, 'sub-request-traffic')).toHaveValue('50');
      await expect(field(authedPage, 'sub-request-expiry')).toHaveValue('30');

      // Choose the inbound and other limits; they are stored.
      await inbounds.click();
      await authedPage.locator('.ant-select-dropdown:visible').getByText(`${remark} (vless)`).click();
      await authedPage.keyboard.press('Escape');
      await field(authedPage, 'sub-request-traffic').fill('20');
      await field(authedPage, 'sub-request-expiry').fill('7');
      await save(authedPage);

      const stored = (await (await authedRequest.post('/panel/setting/all')).json()).obj;
      expect(stored.subRequestInbounds).toBe(String(inboundId));
      expect(stored.subRequestTrafficGB).toBe(20);
      expect(stored.subRequestExpiryDays).toBe(7);

      await openRequestDefaults(authedPage);
      await expect(inbounds).toContainText(remark);
      await expect(allEnabled).toBeHidden();
      await expect(field(authedPage, 'sub-request-traffic')).toHaveValue('20');
      await expect(field(authedPage, 'sub-request-expiry')).toHaveValue('7');

      // Back to a fresh panel's defaults.
      await inbounds.locator('.ant-select-selection__choice__remove').click();
      await field(authedPage, 'sub-request-traffic').fill('50');
      await field(authedPage, 'sub-request-expiry').fill('30');
      await save(authedPage);
      await openRequestDefaults(authedPage);
      await expect(allEnabled).toBeVisible();
      const reset = (await (await authedRequest.post('/panel/setting/all')).json()).obj;
      expect([reset.subRequestInbounds, reset.subRequestTrafficGB, reset.subRequestExpiryDays]).toEqual(['', 50, 30]);
    } finally {
      await authedRequest.post(`/panel/api/inbounds/del/${inboundId}`);
    }
  });
});
