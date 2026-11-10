import { expect, test, type Page } from './fixtures';

// #943: the command palette searches the whole workspace in one ranked list,
// and the workspace glossary adds the synonyms of what was typed. Tables come
// from the stored metadata, which a fresh cluster only has after the first
// pass, so the spec runs one pass of the seeded e2e_tpch catalog first.
async function syncTpchMetadata(page: Page, token: string) {
	const headers = { Authorization: `Bearer ${token}` };
	const catalogs: { id: string; name: string }[] = await (
		await page.request.get('/api/v1/catalogs', { headers })
	).json();
	const tpch = catalogs.find((catalog) => catalog.name === 'e2e_tpch');
	expect(tpch, 'seeded e2e_tpch catalog').toBeTruthy();
	const runs = `/api/v1/catalogs/${tpch!.id}/metadata/sync-runs?limit=1`;
	const sync = await page.request.post(`/api/v1/catalogs/${tpch!.id}/metadata/sync`, { headers });
	// 409: a scheduled pass is already running; waiting for it is enough.
	expect([202, 409]).toContain(sync.status());
	await expect
		.poll(async () => (await (await page.request.get(runs, { headers })).json())[0]?.status, {
			timeout: 90_000
		})
		.toBe('finished');
}

async function signInAndSync(
	page: Page,
	loginAs: (role: 'admin' | 'member' | 'viewer') => Promise<void>
) {
	let token = '';
	await page.route('**/api/v1/auth/login', async (route) => {
		const response = await route.fetch();
		if (response.ok()) token = (await response.json()).token;
		await route.fulfill({ response });
	});
	await loginAs('admin');
	await page.unroute('**/api/v1/auth/login');
	await syncTpchMetadata(page, token);
	return token;
}

async function searchPalette(page: Page, text: string) {
	await page.goto('/sql');
	await page.getByRole('button', { name: 'Commands' }).click();
	await page.getByRole('combobox', { name: 'Command palette' }).fill(text);
}

test('@issue-943 the palette finds a table by its plural, ranked, with its kind', async ({
	page,
	loginAs,
	expectNoA11yViolations
}) => {
	await signInAndSync(page, loginAs);

	await searchPalette(page, 'nations');

	const hit = page.getByRole('option').filter({ hasText: 'nation' }).first();
	await expect(hit).toBeVisible();
	await expect(hit).toContainText('Table');
	await expectNoA11yViolations(page);
});

test('@issue-943 a glossary synonym finds the table it stands for', async ({ page, loginAs }) => {
	const token = await signInAndSync(page, loginAs);

	await page.goto('/settings/search');
	await expect(page.getByRole('heading', { name: 'Search glossary', level: 1 })).toBeVisible();
	await page.getByLabel('Term').fill('Country');
	await page.getByLabel('Synonyms').fill('nation, state');
	await page.getByRole('button', { name: 'Add' }).click();
	await expect(page.getByRole('cell', { name: 'country', exact: true })).toBeVisible();

	try {
		await searchPalette(page, 'country');
		const hit = page.getByRole('option').filter({ hasText: 'nation' }).first();
		await expect(hit).toBeVisible();
		await expect(hit).toContainText('Table');
	} finally {
		// Leave the glossary as it was: specs do not depend on each other.
		const headers = { Authorization: `Bearer ${token}` };
		const workspaces: { id: string }[] = await (
			await page.request.get('/api/v1/workspaces', { headers })
		).json();
		for (const workspace of workspaces) {
			const glossary = await (
				await page.request.get(`/api/v1/workspaces/${workspace.id}/search/glossary`, { headers })
			).json();
			for (const entry of glossary.entries ?? []) {
				if (entry.term === 'country') {
					await page.request.delete(
						`/api/v1/workspaces/${workspace.id}/search/glossary/${entry.id}`,
						{ headers }
					);
				}
			}
		}
	}
});
