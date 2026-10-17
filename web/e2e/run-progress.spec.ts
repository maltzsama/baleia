import { expect, test } from './fixtures';

// #111: a failed statement explains the skipped tail and offers a safe re-run.
test('@issue-111 a failed statement explains the skipped tail', async ({ page, loginAs }) => {
	await loginAs('member');

	const catalog = page.getByTestId('catalog-select');
	await catalog
		.locator('option')
		.nth(1)
		.waitFor({ state: 'attached', timeout: 15_000 })
		.catch(() => {});
	if ((await catalog.locator('option').count()) <= 1) {
		test.skip(true, 'No execution target seeded');
	}
	await catalog.selectOption({ index: 1 });

	await page.locator('.cm-content').click();
	await page.keyboard.press('Control+A');
	await page.keyboard.type('SELECT 1; SELECT * FROM baleia_missing_table; SELECT 3');
	await page.getByRole('button', { name: /^Run/ }).click();

	await expect(page.getByText(/Statement 2 failed/)).toBeVisible({ timeout: 30_000 });
	await expect(page.getByRole('button', { name: 'Re-run from statement 3' })).toBeVisible();
});
