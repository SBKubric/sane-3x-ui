import { randomUUID } from 'node:crypto';
import { type APIRequestContext, type Page } from '@playwright/test';
import { expect, test } from '../fixtures/panel';

/** The panel's sub server (docker-compose.yml publishes it). */
const SUB_URL = process.env.E2E_SUB_URL || 'http://127.0.0.1:2096';

/** The rule set of #263. */
const ROUTING = {
  name: 'RU',
  domainStrategy: 'IPIfNonMatch',
  directSites: ['geosite:category-ru', 'domain:ozon.ru', 'domain:ozone.ru', 'domain:o3t.ru', 'domain:push.apple.com', 'domain:ess.apple.com'],
  directIp: ['geoip:ru', 'geoip:private', '17.0.0.0/8', '185.73.192.0/22'],
  directProtocols: ['bittorrent'],
  proxySites: ['domain:acomics.ru'],
};

const HAPP = 'Happ/3.13.0';
const V2RAYTUN = 'v2raytun/android';
const V2RAYNG = 'v2rayNG/1.9.30';
const INCY = 'INCY/3.8.8/ios';

/** The client routing field: an <a-textarea>, the data-testid on the textarea or its wrapper. */
function routingField(page: Page) {
  return page.locator('textarea[data-testid="sub-client-routing"], [data-testid="sub-client-routing"] textarea').first();
}

/** The Subscription tab with its «Information» block open, where the client routing is. */
async function openClientRouting(page: Page) {
  await page.goto('/panel/settings');
  // The tab's name carries its icon's label; the anchor leaves out «Subscription (Formats)».
  await page.getByRole('tab', { name: /Subscription$/ }).click();
  await page.getByRole('button', { name: /Information/ }).click();
  await expect(routingField(page)).toBeVisible();
}

async function save(page: Page) {
  await page.getByRole('button', { name: 'Save', exact: true }).click();
}

async function stored(request: APIRequestContext): Promise<string> {
  const res = await (await request.post('/panel/setting/all')).json();
  expect(res.success, res.msg).toBe(true);
  return res.obj.subClientRouting;
}

async function fetchAs(request: APIRequestContext, url: string, userAgent: string) {
  const res = await request.get(url, { headers: { 'User-Agent': userAgent } });
  expect(res.status()).toBe(200);
  return { headers: res.headers(), body: await res.text() };
}

// The client routing of the Subscription settings tab (#263): saved, Happ
// and Incy get the routing profile and v2RayTun its routing object in the routing
// header of the subscription, other apps nothing, and the subscription
// itself is the same for all of them — without a restart of the panel.
test.describe('client routing', () => {
  test('hands Happ, Incy and v2RayTun the routing in their formats, and nothing to v2rayNG', async ({
    authedPage: page,
    authedRequest,
    request,
  }) => {
    const subId = `e2eroute${Date.now().toString(36)}`;
    const added = await (
      await authedRequest.post('/panel/api/inbounds/add', {
        data: {
          up: 0,
          down: 0,
          total: 0,
          remark: 'e2e-client-routing',
          enable: true,
          expiryTime: 0,
          listen: '',
          port: 24363,
          protocol: 'vless',
          settings: JSON.stringify({
            clients: [{ id: randomUUID(), email: `e2e-client-routing-${subId}`, enable: true, subId }],
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
      // Off on a fresh panel: no app gets a routing of ours.
      expect(await stored(authedRequest)).toBe('');
      const subPath: string = (await (await authedRequest.post('/panel/setting/all')).json()).obj.subPath;
      const url = `${SUB_URL}${subPath}${subId}`;
      const before = await fetchAs(request, url, HAPP);
      expect(before.headers['routing'] || '').not.toContain('happ://routing/onadd/');

      // A routing the apps could not use is refused, and nothing is stored.
      await openClientRouting(page);
      await routingField(page).fill(JSON.stringify({ ...ROUTING, domainStrategy: 'Sometimes' }));
      await save(page);
      await expect(page.getByText(/client routing: domainStrategy/).first()).toBeVisible();
      expect(await stored(authedRequest)).toBe('');

      // The rule set of #263, stored with every field.
      await openClientRouting(page);
      await routingField(page).fill(JSON.stringify(ROUTING));
      await save(page);
      await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
      expect(JSON.parse(await stored(authedRequest))).toEqual({ ...ROUTING, proxyIp: [], blockSites: [], blockIp: [], geositeUrl: '', geoipUrl: '' });

      // Happ: its profile, switched on, the same on every update.
      const happ = await fetchAs(request, url, HAPP);
      expect(happ.headers['routing']).toMatch(/^happ:\/\/routing\/onadd\//);
      expect(happ.headers['routing-enable']).toBe('true');
      expect(happ.headers['vary']).toContain('User-Agent');
      const profile = JSON.parse(Buffer.from(happ.headers['routing'].replace('happ://routing/onadd/', ''), 'base64').toString());
      expect(profile).toMatchObject({
        Name: 'RU',
        GlobalProxy: 'true',
        RouteOrder: 'proxy-block-direct',
        DomainStrategy: 'IPIfNonMatch',
        DirectSites: ROUTING.directSites,
        DirectIp: ROUTING.directIp,
        ProxySites: ['domain:acomics.ru'],
      });
      expect(profile.LastUpdated).toMatch(/^\d{10}$/);
      expect((await fetchAs(request, url, HAPP)).headers['routing']).toBe(happ.headers['routing']);
      // Incy reads Happ's profile from the same header: the very same value.
      expect((await fetchAs(request, url, INCY)).headers['routing']).toBe(happ.headers['routing']);

      // v2RayTun: its object, the VPN's site first, bittorrent direct.
      const tun = await fetchAs(request, url, V2RAYTUN);
      const object = JSON.parse(Buffer.from(tun.headers['routing'], 'base64').toString());
      expect(object).toMatchObject({ name: 'RU', domainStrategy: 'IPIfNonMatch', domainMatcher: 'hybrid', balancers: [] });
      expect(object.rules.map((r: { outboundTag: string }) => r.outboundTag)).toEqual(['proxy', 'direct', 'direct', 'direct']);
      expect(object.rules[0].domain).toEqual(['domain:acomics.ru']);
      expect(object.rules[3].protocol).toEqual(['bittorrent']);

      // v2rayNG: nothing; and the subscription itself is the same for all.
      const ng = await fetchAs(request, url, V2RAYNG);
      expect(ng.headers['routing']).toBeUndefined();
      expect(ng.body).not.toBe('');
      expect(happ.body).toBe(ng.body);
      expect(tun.body).toBe(ng.body);

      // The whole form saved again, the routing unchanged: Happ's
      // LastUpdated stays, or every Happ would download its geo files again.
      const all = await (await authedRequest.post('/panel/setting/all')).json();
      const resaved = await (await authedRequest.post('/panel/setting/update', { data: all.obj })).json();
      expect(resaved.success, resaved.msg).toBe(true);
      expect((await fetchAs(request, url, HAPP)).headers['routing']).toBe(happ.headers['routing']);
    } finally {
      const all = await (await authedRequest.post('/panel/setting/all')).json();
      await authedRequest.post('/panel/setting/update', { data: { ...all.obj, subClientRouting: '' } });
      await authedRequest.post(`/panel/api/inbounds/del/${added.obj.id}`);
    }
  });
});
