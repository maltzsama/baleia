import { expect, test } from './fixtures';

// The /governance screens (#1146) over what cmd/e2e-seed published for the
// data policy e2e: member-eu and viewer-us row filters and the viewer-email
// mask on e2e_governed.sales.customers. Read-only, so the editor and BI
// gateway specs of #1145 see the same policies.

test('@issue-1146 By object lists the seeded policies of the governed catalog', async ({
	page,
	loginAs,
	expectNoA11yViolations
}) => {
	await loginAs('admin');
	await page.goto('/governance');
	const catalog = page.getByRole('combobox', { name: 'Catalog' });
	const governed = catalog.locator('option', { hasText: 'E2E Governed' });
	await governed.waitFor({ state: 'attached', timeout: 15_000 });
	await catalog.selectOption((await governed.getAttribute('value')) ?? '');

	const table = page.getByRole('region', { name: 'Policies on sales.customers' });
	await expect(table.getByText('member-eu', { exact: true })).toBeVisible({ timeout: 15_000 });
	await expect(table.getByText('viewer-us', { exact: true })).toBeVisible();
	await expect(table.getByText('viewer-email', { exact: true })).toBeVisible();
	await expect(table.getByText('Mask on email')).toBeVisible();
	await expect(page.getByText('Policies hold on the Trino path only')).toBeVisible();
	await expect(page.getByRole('region', { name: 'Active revision per engine' })).toBeVisible();
	await expectNoA11yViolations(page);
});

test('@issue-1146 By subject shows the viewer filtered to US with e-mails masked', async ({
	page,
	loginAs
}) => {
	await loginAs('admin');
	await page.goto('/governance/subjects');
	const subject = page.getByRole('combobox', { name: 'Subject', exact: true });
	const viewer = subject.locator('option', { hasText: 'E2E Viewer' });
	await viewer.waitFor({ state: 'attached', timeout: 15_000 });
	await subject.selectOption((await viewer.getAttribute('value')) ?? '');
	await page.getByRole('button', { name: 'Show' }).click();

	const row = page
		.getByRole('region', { name: 'E2E Governed' })
		.getByRole('row')
		.filter({ hasText: 'sales.customers' });
	// The expressions show in read-only CodeBlocks, named after what they hold.
	await expect(row.getByLabel('Rows sales.customers shows')).toContainText("region = 'US'");
	await expect(row.getByLabel('Mask on email of sales.customers')).toContainText("'***'");
});

test('@issue-1146 Revisions shows the published revision and a healthy engine', async ({
	page,
	loginAs,
	expectNoA11yViolations
}) => {
	await loginAs('admin');
	await page.goto('/governance/revisions');
	await expect(page.getByText('Published', { exact: true }).first()).toBeVisible({
		timeout: 15_000
	});
	const engines = page.getByRole('region', { name: 'Active revision per engine' });
	await expect(engines.getByText('Healthy')).toBeVisible();
	await expectNoA11yViolations(page);
});

test('@issue-1146 a member without data policy rights has no Revisions', async ({
	page,
	loginAs
}) => {
	await loginAs('viewer');
	await page.goto('/governance/revisions');
	await expect(page.getByText('Revisions are for global admins')).toBeVisible();
});
