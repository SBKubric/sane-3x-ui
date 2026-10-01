import { expect, test } from '../fixtures/panel';

/**
 * A new inbound's uTLS fingerprint defaults to firefox (#230): the inbound
 * form picks it for Reality and for TLS, and what the form saves is what the
 * subscriptions hand to clients.
 */
test.describe('inbound form uTLS default', () => {
  test('a new Reality inbound gets firefox', async ({ authedPage, authedRequest }) => {
    const remark = 'e2e-utls-firefox';

    await authedPage.goto('/panel/inbounds');
    await authedPage.getByTestId('add-inbound').click();
    const dialog = authedPage.getByRole('dialog');
    await expect(dialog).toBeVisible();

    // The remark field is an antd <a-input>; the testid lands on the <input>
    // itself or on a wrapper around it (see fixtures/panel.ts).
    await dialog
      .locator('input[data-testid="inbound-remark"], [data-testid="inbound-remark"] input')
      .first()
      .fill(remark);

    // TLS first: it has the same default as Reality.
    await dialog.getByText('TLS', { exact: true }).click();
    await expect(dialog.getByTestId('tls-utls')).toContainText('firefox');

    await dialog.getByText('Reality', { exact: true }).click();
    await expect(dialog.getByTestId('reality-utls')).toContainText('firefox');

    await dialog.getByRole('button', { name: 'Create' }).click();
    await expect(dialog).toBeHidden();

    const res = await authedRequest.get('/panel/api/inbounds/list');
    expect(res.status()).toBe(200);
    const body = await res.json();
    expect(body.success).toBe(true);
    const saved = (body.obj as Array<{ id: number; remark: string; streamSettings: string }>).find(
      (ib) => ib.remark === remark,
    );
    expect(saved, `inbound "${remark}" was not saved`).toBeTruthy();
    const stream = JSON.parse(saved!.streamSettings);
    expect(stream.security).toBe('reality');
    expect(stream.realitySettings.settings.fingerprint).toBe('firefox');

    // Leave the shared panel as it was: other specs count inbounds.
    const del = await authedRequest.post(`/panel/api/inbounds/del/${saved!.id}`);
    expect((await del.json()).success).toBe(true);
  });
});
