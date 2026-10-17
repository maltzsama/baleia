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

	const target = page.getByTestId('target-select');
	await target
		.locator('option')
		.nth(1)
		.waitFor({ state: 'attached', timeout: 15_000 })
		.catch(() => {});
	if ((await target.locator('option').count()) <= 1) {
		test.skip(true, 'No execution target seeded; run make k8s-seed-tpch or add a binding');
	}
	await target.selectOption({ index: 1 });
	await page.getByRole('button', { name: /run/i }).click();
	await expect(page.getByText('Complete')).toBeVisible({ timeout: 30_000 });
	await expect(page.getByText('1 rows')).toBeVisible();
});
