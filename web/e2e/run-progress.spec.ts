import { expect, test } from './fixtures';

// #111: a failed statement explains the skipped tail and offers a safe re-run.
test('@issue-111 a failed statement explains the skipped tail', async ({ page, loginAs }) => {
	await loginAs('member');

	// cmd/e2e-seed grants e2e_tpch to every e2e workspace; other catalogs
	// (e2e_governed, with data policies) may come first.
	const catalog = page.getByTestId('catalog-select');
	const tpch = catalog.locator('option', { hasText: 'e2e_tpch' });
	await tpch.waitFor({ state: 'attached', timeout: 15_000 });
	await catalog.selectOption((await tpch.getAttribute('value')) ?? '');

	await page.locator('.cm-content').click();
	await page.keyboard.press('Control+A');
	await page.keyboard.type('SELECT 1; SELECT * FROM baleia_missing_table; SELECT 3');
	// The script runs as a whole with Shift (#417); Run alone runs the current statement.
	await page.keyboard.press('ControlOrMeta+Shift+Enter');

	await expect(page.getByText(/Statement 2 failed/)).toBeVisible({ timeout: 30_000 });
	await expect(page.getByRole('button', { name: 'Re-run from statement 3' })).toBeVisible();
});
