import { randomUUID } from 'node:crypto';
import { expect, test } from '../fixtures/panel';

/** The panel's sub server (docker-compose.yml publishes it). */
const SUB_URL = process.env.E2E_SUB_URL || 'http://127.0.0.1:2096';

// The subscription page offers the client JSON config of every link beside
// the link (#231), embedded in the page: a fresh panel has the JSON
// subscription off, and the button works all the same, with no link to the
// JSON path on the page.
test.describe('subscription page JSON', () => {
  test('copies the JSON config of a link with the JSON subscription off', async ({ authedRequest, page, context }) => {
    const subId = `e2ejson${Date.now().toString(36)}`;
    const port = 24311;
    const added = await (
      await authedRequest.post('/panel/api/inbounds/add', {
        data: {
          up: 0,
          down: 0,
          total: 0,
          remark: 'e2e-sub-json',
          // Only enabled inbounds are in a subscription.
          enable: true,
          expiryTime: 0,
          listen: '',
          port,
          protocol: 'vless',
          settings: JSON.stringify({
            clients: [{ id: randomUUID(), email: `e2e-sub-json-${subId}`, enable: true, subId }],
            decryption: 'none',
            fallbacks: [],
          }),
          streamSettings: JSON.stringify({ network: 'tcp', security: 'none', tcpSettings: { header: { type: 'none' } } }),
          sniffing: JSON.stringify({ enabled: false, destOverride: [] }),
        },
      })
    ).json();
    expect(added.success, added.msg).toBe(true);

    try {
      const settings = await (await authedRequest.post('/panel/setting/all')).json();
      expect(settings.success).toBe(true);
      expect(settings.obj.subJsonEnable).toBe(false);
      const jsonPath: string = settings.obj.subJsonPath;
      // The JSON subscription is off: its path answers nothing.
      expect((await page.request.get(`${SUB_URL}${jsonPath}${subId}`)).status()).toBe(404);

      // The configs the page must carry, from the subscription path itself.
      const list = await page.request.get(`${SUB_URL}${settings.obj.subPath}${subId}?format=json`);
      expect(list.status()).toBe(200);
      const configs = await list.json();
      expect(configs).toHaveLength(1);

      await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: SUB_URL });
      const pageResponse = await page.goto(`${SUB_URL}${settings.obj.subPath}${subId}`);
      expect(await pageResponse!.text()).not.toContain(`${jsonPath}${subId}`);

      const link = page.getByTestId('sub-link');
      await expect(link).toHaveCount(1);
      await expect(link).toContainText(`vless://`);
      await expect(link.getByTestId('sub-copy-link')).toBeVisible();
      await link.getByTestId('sub-copy-json').click();
      await expect(page.getByText('Copied')).toBeVisible();

      const copied = await page.evaluate(() => navigator.clipboard.readText());
      const config = JSON.parse(copied);
      expect(config).toEqual(configs[0]);
      const proxy = config.outbounds.find((o: { tag: string }) => o.tag === 'proxy');
      expect(proxy.protocol).toBe('vless');
      expect(proxy.settings.vnext[0].port).toBe(port);
    } finally {
      await authedRequest.post(`/panel/api/inbounds/del/${added.obj.id}`);
    }
  });
});
