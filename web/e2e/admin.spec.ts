import { expect, test } from './fixtures';

// #385: the admin sees the bootstrap connection and a catalog to query, and
// deep links to routes that are also build directories load the app.
test('@issue-385 the admin sees connections, a catalog and the profile', async ({
	page,
	loginAs,
	expectNoA11yViolations
}) => {
	await loginAs('admin');
	await expect(page.getByTestId('catalog-select')).toContainText('e2e_tpch');

	// A full load of /connections, not a client-side navigation.
	await page.goto('/connections');
	await expect(page.getByRole('heading', { name: 'Connections', level: 1 })).toBeVisible();
	await expect(page.getByText('trino-default').first()).toBeVisible();

	await page.goto('/settings/profile');
	await expect(page.getByLabel('Email')).toBeEnabled();
	await expect(page.getByRole('button', { name: /change password/i })).toBeVisible();
	await expectNoA11yViolations(page);
});
