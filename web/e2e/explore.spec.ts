import { expect, test } from './fixtures';

// #121: pick a model, click fields, read the compiled SQL and run it. The seed
// (cmd/e2e-seed) creates e2e_orders joined many_to_one to e2e_customers over tpch.
test('@issue-121 explore compiles a model query and shares it as a link', async ({
	page,
	loginAs,
	expectNoA11yViolations
}) => {
	await loginAs('member');
	await page.goto('/explore');

	await page.getByLabel('Model', { exact: true }).selectOption({ label: 'E2E Orders' });
	await page.getByRole('button', { name: /Order status/i }).click();
	await page.getByRole('button', { name: /^Orders\b/ }).click();

	// The selection is the only state and lives in the URL.
	await expect(page).toHaveURL(/\/explore\?q=[A-Za-z0-9_-]+/);

	await page.getByRole('tab', { name: 'SQL' }).click();
	await expect(page.locator('.cm-content')).toContainText('COUNT');
	await expectNoA11yViolations(page);

	// A shared link restores the same view after a reload.
	const url = page.url();
	await page.goto('/explore');
	await page.goto(url);
	await expect(page.getByRole('tab', { name: 'SQL' })).toBeVisible();
	await page.getByRole('tab', { name: 'SQL' }).click();
	await expect(page.locator('.cm-content')).toContainText('COUNT');

	// Run it against the engine and see rows in the result grid.
	await page.getByRole('tab', { name: 'Results' }).click();
	await page.getByRole('button', { name: 'Run', exact: true }).click();
	await expect(page.getByText('order_count').first()).toBeVisible({ timeout: 60_000 });
});

test('@issue-121 explore shows the joined model under its join name', async ({ page, loginAs }) => {
	await loginAs('member');
	await page.goto('/explore');
	await page.getByLabel('Model', { exact: true }).selectOption({ label: 'E2E Orders' });

	const tree = page.getByTestId('field-tree');
	await expect(tree.getByRole('region', { name: 'E2E Customers' })).toBeVisible();
	await expect(tree.getByText('Joined')).toBeVisible();
});

test('@issue-121 the model editor lists seeded models and opens one', async ({
	page,
	loginAs,
	expectNoA11yViolations
}) => {
	await loginAs('member');
	await page.goto('/models');
	await expect(page.getByRole('link', { name: 'E2E Orders' })).toBeVisible();
	await expectNoA11yViolations(page);

	await page.getByRole('link', { name: 'E2E Orders' }).click();
	await expect(page.getByTestId('model-editor')).toBeVisible();
	await expect(page.getByTestId('join-tree')).toContainText('e2e_customers');
});

test('@issue-121 the v1 migration dialog lists the seeded v1 metric', async ({
	page,
	loginAs,
	expectNoA11yViolations
}) => {
	await loginAs('member');
	await page.goto('/models');
	await page.getByRole('button', { name: 'Migrate v1 metrics' }).click();

	const tables = page.getByRole('list', { name: 'Tables to migrate' });
	await expect(tables).toContainText('tiny.nation');
	await expect(tables).toContainText('e2e_nation_count');
	await expectNoA11yViolations(page);
});
