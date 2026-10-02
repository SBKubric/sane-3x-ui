import { type APIRequestContext, type Page } from '@playwright/test';
import { expect, test } from '../fixtures/panel';

/** The panel's sub server, where boxes join and poll (docker-compose.yml publishes it). */
const SUB_URL = process.env.E2E_SUB_URL || 'http://127.0.0.1:2096';
const HOP = 'e2e-captcha-edge';
const HOP_HOST = '198.51.100.62';
/** The captcha's address on the hop: its https sub port, the bot's path. */
const CAPTCHA_ON_HOP = /^https:\/\/198\.51\.100\.62(:\d+)?\/third-party\/[A-Za-z0-9]{24}\/captcha$/;

/** The Telegram tab with its «Subscription requests» block open, where the captcha's host is chosen. */
async function openCaptchaHost(page: Page) {
  await page.goto('/panel/settings');
  await page.getByRole('tab', { name: 'Telegram Bot' }).click();
  await page.getByRole('button', { name: /Subscription requests/ }).click();
  await expect(page.getByTestId('tg-captcha-host')).toBeVisible();
}

async function choose(page: Page, name: RegExp | string) {
  await page.getByTestId('tg-captcha-host').click();
  await page.getByRole('option', { name }).click();
}

async function save(page: Page) {
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
}

async function stored(request: APIRequestContext): Promise<string> {
  const res = await (await request.post('/panel/setting/all')).json();
  expect(res.success, res.msg).toBe(true);
  return res.obj.tgCaptchaHost;
}

async function removeHop(request: APIRequestContext) {
  const list = await (await request.get('/panel/api/chain/list')).json();
  const hop = (list.obj?.hops || []).find((h: { name: string }) => h.name === HOP);
  if (hop) {
    await request.post(`/panel/api/chain/del/${hop.id}`, { data: { force: true, skipDrain: true } });
  }
}

// The captcha's host of the Telegram settings tab (#243): where the bot opens
// its captcha Mini App — the active edge by default, the panel's own front,
// or a hop of the chain. The captcha's address on the tab follows the
// choice; a host without https gives none.
test.describe('captcha host', () => {
  test('is the active edge on a fresh panel, and moves the captcha to a hop and to the panel', async ({
    authedPage: page,
    authedRequest,
    request,
  }) => {
    await removeHop(authedRequest);
    try {
      expect(await stored(authedRequest)).toBe('');

      // An edge enters the chain the way a real box does; its sub port
      // serves https.
      const added = await (
        await authedRequest.post('/panel/api/chain/add', { data: { name: HOP, host: HOP_HOST, role: 'edge' } })
      ).json();
      expect(added.success, added.msg).toBe(true);
      const joined = await request.post(`${SUB_URL}/chain/v1/join`, { data: { token: added.obj.joinToken } });
      expect(joined.status()).toBe(200);

      await openCaptchaHost(page);
      await expect(page.getByTestId('tg-captcha-host')).toContainText('Active edge');

      // The hop: the captcha opens on it.
      await choose(page, `${HOP} (edge)`);
      await save(page);
      expect(await stored(authedRequest)).toBe(HOP);
      await expect(page.getByTestId('bot-captcha-url')).toHaveText(CAPTCHA_ON_HOP);

      // The panel: this one has no 443 front, so the captcha has no address,
      // and the bot does not fall back to the hop.
      await choose(page, /^Panel/);
      await save(page);
      expect(await stored(authedRequest)).toBe('panel');
      await expect(page.getByTestId('bot-captcha-url-none')).toBeVisible();
      await openCaptchaHost(page);
      await expect(page.getByTestId('tg-captcha-host')).toContainText('Panel');

      // A hop gone from the chain still shows as chosen, and gives no address.
      await choose(page, `${HOP} (edge)`);
      await save(page);
      await removeHop(authedRequest);
      await openCaptchaHost(page);
      await expect(page.getByTestId('tg-captcha-host')).toContainText(`${HOP} (not in the chain)`);
      await expect(page.getByTestId('bot-captcha-url-none')).toBeVisible();

      // Back to the default.
      await choose(page, 'Active edge');
      await save(page);
      expect(await stored(authedRequest)).toBe('');
    } finally {
      await removeHop(authedRequest);
    }
  });
});
