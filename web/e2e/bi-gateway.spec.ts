import { readFile } from 'node:fs/promises';
import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';

// This spec shows a one-time secret: never record it in a retry trace or a
// failure screenshot.
test.use({ trace: 'off', screenshot: 'off' });

// #1103: the operator-to-client onboarding path across the UI and the API:
// the global readiness, the workspace BI switch, an authorized key with its
// one-time secret, the guided connection download and revocation. The Trino
// protocol itself (Basic auth, follows, cancel, scope and read-only) is driven
// by the JDBC e2e (make e2e-bi).
async function loginToken(page: Page, loginAs: (role: 'admin') => Promise<void>) {
	let token = '';
	await page.route('**/api/v1/auth/login', async (route) => {
		const response = await route.fetch();
		if (response.ok()) token = (await response.json()).token;
		await route.fulfill({ response });
	});
	await loginAs('admin');
	await page.unroute('**/api/v1/auth/login');
	expect(token, 'login token').toBeTruthy();
	return token;
}

// A fresh cluster only has the e2e_tpch metadata after the first scheduled
// pass; the table detail needs it. The token comes from the page's login,
// which avoids a second login against the 10/min limit (#391).
async function syncTpchMetadata(page: Page, token: string) {
	const headers = { Authorization: `Bearer ${token}` };
	const catalogs: { id: string; name: string }[] = await (
		await page.request.get('/api/v1/catalogs', { headers })
	).json();
	const tpch = catalogs.find((catalog) => catalog.name === 'e2e_tpch');
	expect(tpch, 'seeded e2e_tpch catalog').toBeTruthy();
	const runs = `/api/v1/catalogs/${tpch!.id}/metadata/sync-runs?limit=5`;
	const latest = (await (await page.request.get(runs, { headers })).json())[0];
	const previousId: string | undefined = latest?.id;
	const sync = await page.request.post(`/api/v1/catalogs/${tpch!.id}/metadata/sync`, { headers });
	// 409: a scheduled pass is already running; either way, wait for the run
	// this POST started (a new id) to finish, not the previous one.
	expect([202, 409]).toContain(sync.status());
	await expect
		.poll(
			async () => {
				const list: { id: string; status: string }[] = await (
					await page.request.get(runs, { headers })
				).json();
				const started = list.find((run) => run.id !== previousId);
				return started?.status ?? 'pending';
			},
			{ timeout: 90_000 }
		)
		.toBe('finished');
}

test('@issue-1103 BI gateway onboarding, one-time key and connection download', async ({
	page,
	loginAs
}) => {
	test.setTimeout(180_000);
	const token = await loginToken(page, loginAs);
	const headers = { Authorization: `Bearer ${token}` };

	const me = await (await page.request.get('/api/v1/auth/me', { headers })).json();
	const workspaceId = me.personal_workspace_id as string;
	expect(workspaceId, 'personal workspace').toBeTruthy();

	// Keep the test independent of a previous run: both switches on.
	const gateway = await (await page.request.get('/api/v1/admin/bi-gateway', { headers })).json();
	if (!gateway.enabled) {
		await page.request.put('/api/v1/admin/bi-gateway', { headers, data: { enabled: true } });
	}
	const workspace = await (
		await page.request.get(`/api/v1/workspaces/${workspaceId}`, { headers })
	).json();
	if (!workspace.bi_enabled) {
		await page.request.patch(`/api/v1/workspaces/${workspaceId}`, {
			headers,
			data: { bi_enabled: true }
		});
	}

	await syncTpchMetadata(page, token);

	// The global administrator reads the gateway readiness.
	await page.goto('/settings/bi-gateway');
	await expect(page.getByRole('heading', { name: 'BI gateway', level: 1 })).toBeVisible();
	await expect(page.getByText('Ready').first()).toBeVisible();
	await expect(page.getByRole('button', { name: 'Disable gateway' })).toBeVisible();

	// The workspace administrator creates a key and sees its secret once.
	await page.goto(`/settings/workspaces/${workspaceId}?tab=bi`);
	await expect(page.getByRole('heading', { name: 'BI access', level: 2 })).toBeVisible();
	const keyName = `e2e ${Date.now()}`;
	await page.getByRole('button', { name: 'Create key' }).click();
	const create = page.getByRole('dialog', { name: 'Create a BI key' });
	await create.getByLabel('Name').fill(keyName);
	await create.getByRole('button', { name: 'Create key' }).click();
	const secretModal = page.getByRole('dialog', { name: 'Copy this BI key now' });
	await expect(secretModal).toBeVisible();
	const secret = (await secretModal.locator('.break-all').innerText()).trim();
	expect(secret).toMatch(/^bal_/);
	expect(page.url()).not.toContain(secret);
	await secretModal.getByLabel('I have copied this value').check();
	await secretModal.getByRole('button', { name: 'Done' }).click();
	await expect(page.getByText(keyName)).toBeVisible();

	const keys: { id: string; name: string }[] = await (
		await page.request.get(`/api/v1/workspaces/${workspaceId}/bi-keys`, { headers })
	).json();
	const created = keys.find((key) => key.name === keyName);
	expect(created, 'created key').toBeTruthy();

	// The guided download from a Trino table: the file is credential-free.
	await page.goto(`/data?ws=${workspaceId}&catalog=e2e_tpch&ns=tiny&table=orders`);
	await page.getByRole('button', { name: 'Download Tableau connection' }).click();
	const dialog = page.getByRole('dialog', { name: 'Download the Tableau connection' });
	await expect(dialog).toBeVisible();
	await dialog.getByLabel('BI key').selectOption(created!.id);
	const downloadPromise = page.waitForEvent('download');
	await dialog.getByRole('button', { name: 'Download connection file' }).click();
	const download = await downloadPromise;
	const path = await download.path();
	expect(path, 'downloaded file').toBeTruthy();
	const content = await readFile(path!, 'utf8');
	expect(content).toContain('e2e_tpch');
	expect(content).not.toContain(secret);
	expect(content.toLowerCase()).not.toContain('password');
	await expect(dialog.getByText('Next steps')).toBeVisible();

	// Revocation removes the key from the workspace.
	const revoke = await page.request.delete(
		`/api/v1/workspaces/${workspaceId}/bi-keys/${created!.id}`,
		{ headers }
	);
	expect(revoke.status()).toBe(204);
	const after: { name: string }[] = await (
		await page.request.get(`/api/v1/workspaces/${workspaceId}/bi-keys`, { headers })
	).json();
	expect(after.find((key) => key.name === keyName)).toBeFalsy();
});
