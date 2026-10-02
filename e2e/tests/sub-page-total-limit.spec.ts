import { randomUUID } from 'node:crypto';
import { expect, test } from '../fixtures/panel';

/** The panel's sub server (docker-compose.yml publishes it). */
const SUB_URL = process.env.E2E_SUB_URL || 'http://127.0.0.1:2096';

const GB = 1024 ** 3;

// The total limit (#247): a subscription of VLESS 50 GB and AmneziaWG 20 GB.
// The raw answer carries the clients behind Subscription-Userinfo's sum in
// Sub-Traffic-Breakdown, for the hops' pages; the page words the total limit
// and lists each protocol, in the visitor's language.
test.describe('subscription page total limit', () => {
  test('words the total limit and lists each protocol', async ({ authedRequest, browser }) => {
    const subId = `e2equota${Date.now().toString(36)}`;
    const added = await (
      await authedRequest.post('/panel/api/inbounds/add', {
        data: {
          up: 0,
          down: 0,
          total: 0,
          remark: 'e2e-quota',
          enable: true,
          expiryTime: 0,
          listen: '',
          port: 24352,
          protocol: 'vless',
          settings: JSON.stringify({
            clients: [{ id: randomUUID(), email: `e2e-quota-${subId}`, enable: true, subId, totalGB: 50 * GB }],
            decryption: 'none',
            fallbacks: [],
          }),
          streamSettings: JSON.stringify({ network: 'tcp', security: 'none' }),
          sniffing: JSON.stringify({ enabled: false, destOverride: [] }),
        },
      })
    ).json();
    expect(added.success, added.msg).toBe(true);
    const peer = await (
      await authedRequest.post('/panel/api/awg/client/add', {
        data: { name: 'e2e-quota-awg', email: `e2e-quota-awg-${subId}`, enable: true, subId, totalGB: 20 * GB },
      })
    ).json();
    expect(peer.success, peer.msg).toBe(true);

    const en = await browser.newContext({ locale: 'en-US' });
    const ru = await browser.newContext({ locale: 'ru-RU' });
    try {
      const settings = await (await authedRequest.post('/panel/setting/all')).json();
      expect(settings.success).toBe(true);
      const pageURL = `${SUB_URL}${settings.obj.subPath}${subId}`;

      // The raw answer: the sum unchanged, the breakdown beside it.
      const raw = await en.request.get(pageURL);
      expect(raw.status()).toBe(200);
      expect(raw.headers()['subscription-userinfo']).toContain(`total=${50 * GB}`);
      const breakdown = JSON.parse(Buffer.from(raw.headers()['sub-traffic-breakdown'], 'base64').toString());
      expect(breakdown).toEqual([
        { p: 'vless', r: 'e2e-quota', u: 0, t: 50 * GB },
        { p: 'amneziawg', u: 0, t: 20 * GB },
      ]);

      const page = await en.newPage();
      await page.goto(pageURL);
      const quota = page.getByTestId('sub-quota');
      await expect(quota).toContainText('Total limit: 70.00GB (VLESS 50.00GB + AmneziaWG 20.00GB)');
      const parts = quota.getByTestId('sub-quota-part');
      await expect(parts).toHaveCount(2);
      await expect(parts.nth(0)).toHaveText(/VLESS\s*0\.00B \/ 50\.00GB/);
      await expect(parts.nth(1)).toHaveText(/AmneziaWG\s*0\.00B \/ 20\.00GB/);

      const ruPage = await ru.newPage();
      await ruPage.goto(pageURL);
      await expect(ruPage.getByTestId('sub-quota')).toContainText('Общий лимит: 70.00GB (VLESS 50.00GB + AmneziaWG 20.00GB)');
    } finally {
      await en.close();
      await ru.close();
      await authedRequest.post(`/panel/api/inbounds/del/${added.obj.id}`);
      await authedRequest.post(`/panel/api/awg/client/delByUuid/${peer.obj.uuid}`);
    }
  });
});
