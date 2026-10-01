import { randomUUID } from 'node:crypto';
import { expect, test } from '../fixtures/panel';

/** The panel's sub server (docker-compose.yml publishes it). */
const SUB_URL = process.env.E2E_SUB_URL || 'http://127.0.0.1:2096';

// The subscription page (#235), at phone width: the warning to check the app
// list comes first, the app cards and the configurations carry protocol
// labels from one dictionary, and the copy buttons copy the link and its
// JSON config.
test.describe('subscription page template', () => {
  test.use({ viewport: { width: 390, height: 844 }, locale: 'en-US' });

  test('warns first, labels apps and configurations, copies link and JSON', async ({ authedRequest, page, context }, testInfo) => {
    const subId = `e2epage${Date.now().toString(36)}`;
    const added = await (
      await authedRequest.post('/panel/api/inbounds/add', {
        data: {
          up: 0,
          down: 0,
          total: 0,
          remark: 'e2e-sub-page',
          enable: true,
          expiryTime: 0,
          listen: '',
          port: 24351,
          protocol: 'vless',
          settings: JSON.stringify({
            clients: [{ id: randomUUID(), email: `e2e-sub-page-${subId}`, enable: true, subId }],
            decryption: 'none',
            fallbacks: [],
          }),
          streamSettings: JSON.stringify({
            network: 'xhttp',
            security: 'none',
            xhttpSettings: { path: '/e2e-page', host: '', mode: 'auto' },
          }),
          sniffing: JSON.stringify({ enabled: false, destOverride: [] }),
        },
      })
    ).json();
    expect(added.success, added.msg).toBe(true);
    const peer = await (
      await authedRequest.post('/panel/api/awg/client/add', {
        data: { name: `e2e-page-${subId}`.slice(0, 15), email: `e2e-page-${subId}`, enable: true, subId },
      })
    ).json();
    expect(peer.success, peer.msg).toBe(true);

    try {
      const settings = await (await authedRequest.post('/panel/setting/all')).json();
      expect(settings.success).toBe(true);
      const pageURL = `${SUB_URL}${settings.obj.subPath}${subId}`;
      const configs = await (await page.request.get(`${pageURL}?format=json`)).json();
      expect(configs).toHaveLength(1);

      await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: SUB_URL });
      await page.goto(pageURL);

      // The warning is the first block and the page's heading.
      const warning = page.getByTestId('sub-warning');
      await expect(page.locator('main > *').first()).toHaveAttribute('data-testid', 'sub-warning');
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('First, check that your app is in the list below');
      await expect(warning.getByRole('heading', { level: 1 })).toBeInViewport();
      await expect(warning).toContainText('AmneziaVPN');

      // Labels: the configurations carry theirs, and the app cards the same
      // words for the configurations they work with.
      const link = page.getByTestId('sub-link');
      await expect(link).toHaveCount(1);
      await expect(link.getByTestId('sub-label')).toHaveText('VLESS + XHTTP');
      const tunnel = page.getByTestId('sub-tunnel');
      await expect(tunnel).toHaveCount(1);
      await expect(tunnel.getByTestId('sub-label')).toHaveText(/^AWG [123]$/);
      await expect(page.getByTestId('sub-app').first()).toBeVisible();
      await expect(page.getByTestId('sub-app').filter({ has: page.getByTestId('sub-app-label').getByText('VLESS + XHTTP', { exact: true }) })).not.toHaveCount(0);
      const appsTop = (await page.getByTestId('sub-app').first().boundingBox())!.y;
      const warningBottom = (await warning.boundingBox())!;
      expect(appsTop).toBeGreaterThan(warningBottom.y + warningBottom.height - 1);
      await expect(page.getByTestId('sub-do-not-share')).toBeVisible();

      // Copy link, then copy JSON.
      const linkText = (await link.locator('[data-role="link"]').textContent())!.trim();
      expect(linkText).toContain('type=xhttp');
      await link.getByTestId('sub-copy-link').click();
      await expect(link.getByTestId('sub-copy-link')).toHaveText('Copied');
      expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(linkText);

      await link.getByTestId('sub-copy-json').click();
      await expect(link.getByTestId('sub-copy-json')).toHaveText('Copied');
      const copied = JSON.parse(await page.evaluate(() => navigator.clipboard.readText()));
      expect(copied).toEqual(configs[0]);

      // Screenshots for the review, light and dark, at phone width.
      await page.emulateMedia({ colorScheme: 'light' });
      await page.screenshot({ path: testInfo.outputPath('sub-page-light.png'), fullPage: true });
      await page.emulateMedia({ colorScheme: 'dark' });
      await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(14, 17, 20)');
      await page.screenshot({ path: testInfo.outputPath('sub-page-dark.png'), fullPage: true });
    } finally {
      await authedRequest.post(`/panel/api/inbounds/del/${added.obj.id}`);
      await authedRequest.post(`/panel/api/awg/client/delByUuid/${peer.obj.uuid}`);
    }
  });

  test('speaks Russian to a Russian browser', async ({ authedRequest, browser }) => {
    const subId = `e2epageru${Date.now().toString(36)}`;
    const peer = await (
      await authedRequest.post('/panel/api/awg/client/add', {
        data: { name: 'e2e-page-ru', email: `e2e-page-ru-${subId}`, enable: true, subId },
      })
    ).json();
    expect(peer.success, peer.msg).toBe(true);
    const ru = await browser.newContext({ locale: 'ru-RU' });
    try {
      const settings = await (await authedRequest.post('/panel/setting/all')).json();
      const page = await ru.newPage();
      await page.goto(`${SUB_URL}${settings.obj.subPath}${subId}`);
      await expect(page.getByRole('heading', { level: 1 })).toHaveText('Сначала проверьте, что ваше приложение есть в списке ниже');
      await expect(page.locator('html')).toHaveAttribute('lang', 'ru-RU');
    } finally {
      await ru.close();
      await authedRequest.post(`/panel/api/awg/client/delByUuid/${peer.obj.uuid}`);
    }
  });
});
