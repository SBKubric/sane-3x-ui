import { expect, test } from '../fixtures/panel';

/** The panel's sub server (docker-compose.yml publishes it). */
const SUB_URL = process.env.E2E_SUB_URL || 'http://127.0.0.1:2096';

// Tunnel subscription (docs/spec/tunnel-subscription.md §6, §8): an AWG peer
// linked to a subscription is served by /tun/<subId> and shown on the page of
// /sub/<subId> — the link the bot answers every user with (#167 Q7).
test.describe('tunnel subscription', () => {
  test('a linked AWG peer is on /tun and on the subscription page', async ({ authedRequest, page, request }) => {
    const subId = `e2etun${Date.now().toString(36)}`;
    const added = await (
      await authedRequest.post('/panel/api/awg/client/add', {
        data: { name: 'e2e-tun-phone', email: 'e2e-tun-phone', enable: true, subId },
      })
    ).json();
    expect(added.success, added.msg).toBe(true);
    expect(added.obj.subId).toBe(subId);

    // The route: JSON, one AmneziaWG config, whatever the browser accepts.
    const tun = await request.get(`${SUB_URL}/tun/${subId}`, { headers: { Accept: 'text/html' } });
    expect(tun.status()).toBe(200);
    expect(tun.headers()['content-type']).toContain('application/json');
    expect(tun.headers()['subscription-userinfo']).toBeTruthy();
    const items = await tun.json();
    expect(items).toHaveLength(1);
    expect(items[0]).toMatchObject({ kind: 'awg', name: 'e2e-tun-phone', filename: 'e2e-tun-phone', enable: true });
    expect(items[0].conf).toContain('[Interface]');

    // An unknown subscription is an empty list, not an error.
    const unknown = await request.get(`${SUB_URL}/tun/e2e-nobody-${subId}`);
    expect(unknown.status()).toBe(200);
    expect(await unknown.json()).toEqual([]);
    expect(unknown.headers()['subscription-userinfo']).toBeUndefined();

    // The page of the subscription shows the peer's config.
    const settings = await (await authedRequest.post('/panel/setting/all')).json();
    expect(settings.success).toBe(true);
    await page.goto(`${SUB_URL}${settings.obj.subPath}${subId}`);
    const tunnels = page.getByTestId('sub-tunnels');
    await expect(tunnels).toBeVisible();
    const card = tunnels.getByTestId('sub-tunnel');
    await expect(card).toHaveCount(1);
    await expect(card).toContainText('e2e-tun-phone');
    await expect(card).toContainText('AmneziaWG');
    await expect(card.getByTestId('sub-tunnel-conf')).toContainText('[Interface]');
    await expect(card.getByRole('button', { name: /e2e-tun-phone\.conf/ })).toBeVisible();

    const download = page.waitForEvent('download');
    await card.getByRole('button', { name: /e2e-tun-phone\.conf/ }).click();
    expect((await download).suggestedFilename()).toBe('e2e-tun-phone.conf');

    // Unlinking takes it off the subscription at once.
    const unlinked = await (
      await authedRequest.post(`/panel/api/awg/client/updateByUuid/${added.obj.uuid}`, {
        data: { ...added.obj, subId: '' },
      })
    ).json();
    expect(unlinked.success, unlinked.msg).toBe(true);
    expect(await (await request.get(`${SUB_URL}/tun/${subId}`)).json()).toEqual([]);

    await authedRequest.post(`/panel/api/awg/client/delByUuid/${added.obj.uuid}`);
  });
});
