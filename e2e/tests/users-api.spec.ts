import { randomUUID } from 'node:crypto';
import { createInbound } from '../fixtures/inbound';
import { expect, test } from '../fixtures/panel';

// API-only feature (docs/spec/users.md §6) — walked through Playwright's
// request context per docs/agents/testing.md; the users page is #170.
test.describe('users API', () => {
  test('unauthenticated list is a 404', async ({ request }) => {
    const res = await request.get('/panel/api/users/list');
    expect(res.status()).toBe(404);
  });

  test('the technical users exist from the start', async ({ authedRequest }) => {
    const body = await (await authedRequest.get('/panel/api/users/list')).json();
    expect(body.success).toBe(true);
    const technical = body.obj.filter((u: { technical: boolean }) => u.technical).map((u: { name: string }) => u.name);
    expect(technical.sort()).toEqual(['monitoring', 'robot']);
  });

  test('a user with two protocols: create, add, switch off, remove, delete', async ({ authedRequest }) => {
    const nl = await createInbound(authedRequest, 'e2e-users-nl', 24301);
    const de = await createInbound(authedRequest, 'e2e-users-de', 24302);
    // The panel never leaves an inbound without clients, so each gets a
    // bystander the user's removals can leave behind.
    for (const [id, email] of [[nl, 'e2e-bystander-nl'], [de, 'e2e-bystander-de']] as const) {
      const added = await authedRequest.post('/panel/api/inbounds/addClient', {
        form: {
          id: String(id),
          settings: JSON.stringify({ clients: [{ id: randomUUID(), email, enable: false }] }),
        },
      });
      expect((await added.json()).success).toBe(true);
    }

    const created = await (
      await authedRequest.post('/panel/api/users/create', {
        data: { name: 'e2e-ivan', inboundIds: [nl], totalGB: 1 << 30 },
      })
    ).json();
    expect(created.success, created.msg).toBe(true);
    const subId: string = created.obj.subId;
    expect(created.obj.clients.map((c: { name: string }) => c.name)).toEqual(['e2e-ivan-e2e-users-nl']);

    // The name is the user's now, whatever the case.
    const clash = await (await authedRequest.post('/panel/api/users/create', { data: { name: 'E2E-Ivan' } })).json();
    expect(clash.success).toBe(false);
    expect(clash.msg).toContain('e2e-ivan');

    const added = await (
      await authedRequest.post(`/panel/api/users/addProtocol/${subId}`, { data: { inboundId: de } })
    ).json();
    expect(added.success, added.msg).toBe(true);
    expect(added.obj.clients).toHaveLength(2);
    for (const c of added.obj.clients) {
      expect(c.subId).toBe(subId);
      expect(c.totalGB).toBe(1 << 30);
    }

    const off = await (await authedRequest.post(`/panel/api/users/enable/${subId}`, { data: { enable: false } })).json();
    expect(off.success, off.msg).toBe(true);
    expect(off.obj.clients.every((c: { enable: boolean }) => !c.enable)).toBe(true);

    const found = await (await authedRequest.get('/panel/api/users/find?q=e2e-ivan-e2e-users-de')).json();
    expect(found.success, found.msg).toBe(true);
    expect(found.obj.subId).toBe(subId);

    for (const inboundId of [nl, de]) {
      const removed = await (
        await authedRequest.post(`/panel/api/users/removeProtocol/${subId}`, { data: { inboundId } })
      ).json();
      expect(removed.success, removed.msg).toBe(true);
    }
    // No clients left, the user and its subscription stay.
    const kept = await (await authedRequest.get(`/panel/api/users/get/${subId}`)).json();
    expect(kept.success, kept.msg).toBe(true);
    expect(kept.obj.clients).toHaveLength(0);

    const deleted = await (await authedRequest.post(`/panel/api/users/del/${subId}`)).json();
    expect(deleted.success, deleted.msg).toBe(true);
    const gone = await (await authedRequest.get(`/panel/api/users/get/${subId}`)).json();
    expect(gone.success).toBe(false);
  });
});
