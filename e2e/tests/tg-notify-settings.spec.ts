import { type Page } from '@playwright/test';
import { expect, test } from '../fixtures/panel';

/**
 * The notification channel field is an `<a-input>`; depending on the antd
 * build the data-testid lands on the real `<input>` or on a wrapper around it.
 */
function channelField(page: Page) {
  return page.locator('input[data-testid="tg-notify-chat-id"], [data-testid="tg-notify-chat-id"] input').first();
}

async function openTelegramTab(page: Page) {
  await page.goto('/panel/settings');
  await page.getByRole('tab', { name: 'Telegram Bot' }).click();
}

async function save(page: Page) {
  await page.getByRole('button', { name: 'Save', exact: true }).click();
}

// The notification channel of the Telegram settings tab (#195).
test.describe('telegram notification channel', () => {
  test('is unset on a fresh panel, refuses a malformed channel, keeps a valid one, and tests it', async ({
    authedPage,
  }) => {
    await openTelegramTab(authedPage);

    // A fresh panel has no channel, so the bot sends no notifications, and
    // the tab says so.
    await expect(channelField(authedPage)).toHaveValue('');
    await expect(authedPage.getByTestId('tg-notify-unset')).toBeVisible();

    // A name without the @ is neither a chat id nor a username: the save is
    // refused and nothing is stored.
    await channelField(authedPage).fill('e2e_channel');
    await save(authedPage);
    await expect(authedPage.getByText(/notification channel must be a chat id/)).toBeVisible();
    await openTelegramTab(authedPage);
    await expect(channelField(authedPage)).toHaveValue('');

    // An @username is stored, and the warning goes away.
    await channelField(authedPage).fill('@e2e_channel');
    await expect(authedPage.getByTestId('tg-notify-unset')).toBeHidden();
    await save(authedPage);
    await openTelegramTab(authedPage);
    await expect(channelField(authedPage)).toHaveValue('@e2e_channel');
    await expect(authedPage.getByTestId('tg-notify-unset')).toBeHidden();

    // «Send test» reports why nothing was sent: this panel has no bot running.
    await authedPage.getByTestId('tg-notify-test').click();
    await expect(authedPage.getByTestId('tg-notify-result')).toContainText('not running');

    // Back to a fresh panel's state.
    await channelField(authedPage).fill('');
    await save(authedPage);
    await openTelegramTab(authedPage);
    await expect(channelField(authedPage)).toHaveValue('');
    await expect(authedPage.getByTestId('tg-notify-unset')).toBeVisible();
  });
});
