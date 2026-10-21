import { expect, test, type Page } from './fixtures';

// #383: the workbench autocomplete follows the statement context and the tab's
// catalog. Completions read the stored metadata, which a fresh cluster only
// has after the first scheduled pass, so the spec runs one pass of the seeded
// e2e_tpch catalog (cmd/e2e-seed) as the signed-in admin first. The token is
// read from the page's own login response: a second login counts against the
// 10/min limit (#391), and a second refresh races the page's and revokes the
// session as token reuse.
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

async function typeSql(page: Page, text: string) {
	const editor = page.locator('.cm-content');
	await editor.click();
	await page.keyboard.press('ControlOrMeta+a');
	await page.keyboard.press('Delete');
	await page.keyboard.type(text);
}

const options = (page: Page) =>
	page.getByRole('listbox', { name: 'Completions' }).getByRole('option');

test('@issue-383 autocomplete follows the statement context', async ({
	page,
	loginAs,
	expectNoA11yViolations
}) => {
	// The admin's workspace is granted e2e_tpch too (cmd/e2e-seed).
	let token = '';
	await page.route('**/api/v1/auth/login', async (route) => {
		const response = await route.fetch();
		if (response.ok()) token = (await response.json()).token;
		await route.fulfill({ response });
	});
	await loginAs('admin');
	await page.unroute('**/api/v1/auth/login');
	await syncTpchMetadata(page, token);

	// Start of a statement: statement keywords, not functions.
	await typeSql(page, 's');
	await expect(options(page).first()).toHaveAccessibleName('SELECT');
	await expect(options(page).filter({ hasText: /^SHA1/ })).toHaveCount(0);

	// A schema lists only its tables.
	await typeSql(page, 'SELECT * FROM tiny.');
	await expect(page.getByRole('option', { name: 'nation tiny', exact: true })).toHaveCount(1);
	await expect(options(page).filter({ hasText: /^ABS/ })).toHaveCount(0);

	// An alias offers only that table's columns, also while typing.
	await typeSql(page, 'SELECT * FROM tiny.nation n WHERE n.na');
	await expect(options(page)).toHaveCount(2);
	await expect(options(page).nth(0)).toHaveAccessibleName('nationkey bigint');
	await expect(options(page).nth(1)).toHaveAccessibleName('name varchar(25)');
	await expect(page.locator('.cm-completionInfo')).toContainText('nation');
	await expectNoA11yViolations(page);

	// Accepting a column inserts it at the cursor.
	await page.keyboard.press('ArrowDown');
	await page.keyboard.press('Enter');
	await expect(page.locator('.cm-content')).toContainText('WHERE n.name');

	// Nothing inside a string.
	await typeSql(page, "SELECT * FROM tiny.nation WHERE name = 'na");
	await page.waitForTimeout(500);
	await expect(page.locator('.cm-tooltip-autocomplete')).toHaveCount(0);
});
