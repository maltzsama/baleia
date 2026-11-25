import { expect, test } from './fixtures';

test('@harness login — a member signs in and lands on the workbench', async ({
	page,
	loginAs,
	expectNoA11yViolations
}) => {
	await loginAs('member');
	await expect(page).toHaveURL(/\/sql/);
	await expectNoA11yViolations(page);
});

test('@harness run — a member runs SELECT 1', async ({ page, loginAs }) => {
	await loginAs('member');

	// cmd/e2e-seed grants the e2e_tpch target to every e2e workspace.
	const catalog = page.getByTestId('catalog-select');
	const tpch = catalog.locator('option', { hasText: 'e2e_tpch' });
	await tpch.waitFor({ state: 'attached', timeout: 15_000 });
	await catalog.selectOption((await tpch.getAttribute('value')) ?? '');
	await page.locator('.cm-content').click();
	await page.keyboard.press('ControlOrMeta+a');
	await page.keyboard.press('Delete');
	await page.keyboard.insertText('SELECT 1');
	await page.keyboard.press('Escape');
	await page.getByTestId('run-current').click();
	const results = page.getByTestId('results-area');
	await expect(results.getByText('Finished', { exact: true })).toBeVisible({ timeout: 30_000 });
	await expect(results.getByText('1 row', { exact: true })).toBeVisible();
});
