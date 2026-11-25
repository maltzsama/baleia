import { expect, test } from './fixtures';
import type { Page } from '@playwright/test';

// Data policies (ADR 0028) hold in the editor: cmd/e2e-seed published a row
// filter that gives the member the EU customers, and a row filter and an
// e-mail mask that give the viewer the US customers with e-mails masked.

const sql = 'SELECT id, region, email FROM e2e_governed.sales.customers ORDER BY id';

async function runGoverned(page: Page) {
	const catalog = page.getByTestId('catalog-select');
	const governed = catalog.locator('option', { hasText: 'E2E Governed' });
	await governed.waitFor({ state: 'attached', timeout: 15_000 });
	await catalog.selectOption((await governed.getAttribute('value')) ?? '');
	await page.locator('.cm-content').click();
	await page.keyboard.press('ControlOrMeta+a');
	await page.keyboard.press('Delete');
	await page.keyboard.insertText(sql);
	await page.keyboard.press('Escape');
	await page.getByTestId('run-current').click();
	const results = page.getByTestId('results-area');
	await expect(results.getByText('Finished', { exact: true })).toBeVisible({ timeout: 60_000 });
	await expect(results.getByText('2 rows', { exact: true })).toBeVisible();
	return results;
}

test('@issue-1145 the member reads only the EU customers, with their e-mails', async ({
	page,
	loginAs
}) => {
	await loginAs('member');
	const results = await runGoverned(page);
	await expect(results.getByText('ana@example.eu', { exact: true })).toBeVisible();
	await expect(results.getByText('bo@example.eu', { exact: true })).toBeVisible();
	await expect(results.getByText('US', { exact: true })).toHaveCount(0);
	await expect(results.getByText('***', { exact: true })).toHaveCount(0);
});

test('@issue-1145 the viewer reads only the US customers, with e-mails masked', async ({
	page,
	loginAs
}) => {
	await loginAs('viewer');
	const results = await runGoverned(page);
	await expect(results.getByText('***', { exact: true })).toHaveCount(2);
	await expect(results.getByText('EU', { exact: true })).toHaveCount(0);
	await expect(results.getByText(/@example\./)).toHaveCount(0);
});
