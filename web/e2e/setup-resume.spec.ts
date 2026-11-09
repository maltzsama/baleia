import { expect, test } from './fixtures';

// #676: each setup step has its own URL, so a refresh stays on the step. The
// Go server redirects the exact path /setup to / once the instance is
// configured (right after the admin is created), which used to turn a refresh
// during step 2 into a trip to the SQL editor. Only "Skip for now" skips. The
// API is stubbed so the flow can be driven on an instance that is already set up.
test('@issue-676 a refresh stays on the setup step; only Skip moves on', async ({ page }) => {
	await page.route('**/api/v1/setup/status', (route) =>
		route.fulfill({ json: { is_configured: false, version: 'test' } })
	);
	await page.route('**/api/v1/setup/admin', (route) =>
		route.fulfill({ status: 201, json: { token: 'test-token' } })
	);
	// The session the admin got is restored from the cookie after a refresh.
	await page.route('**/api/v1/auth/refresh', (route) =>
		route.fulfill({
			json: { token: 'test-token', expires_at: new Date(Date.now() + 3_600_000).toISOString() }
		})
	);
	await page.route('**/api/v1/engines', (route) => route.fulfill({ json: [] }));
	await page.route('**/api/v1/connection-categories', (route) => route.fulfill({ json: [] }));
	await page.route('**/api/v1/connection-providers', (route) =>
		route.fulfill({
			json: [
				{
					kind: 'testdb',
					label: 'Test Database',
					family: 'database',
					roles: ['metadata', 'direct_engine'],
					status: { state: 'supported' },
					config_schema: [
						{ key: 'host', label: 'Host', type: 'text', required: true, secret: false }
					]
				}
			]
		})
	);

	await page.goto('/setup');
	await page.getByLabel('First name').fill('Ada');
	await page.getByLabel('Email').fill('ada@example.com');
	await page.getByLabel('Password', { exact: true }).fill('correct-horse');
	await page.getByLabel('Confirm password').fill('correct-horse');
	await page.getByRole('button', { name: 'Continue' }).click();

	await expect(page).toHaveURL(/\/setup\/connect$/);
	await expect(page.getByText(/step 2 of 3/i)).toBeVisible();

	await page.reload();
	await expect(page).toHaveURL(/\/setup\/connect$/);
	await expect(page.getByText(/step 2 of 3/i)).toBeVisible();

	await page.getByRole('button', { name: 'Skip for now' }).click();
	await expect(page).toHaveURL(/\/setup\/done$/);
	await expect(page.getByText('Baleia is ready')).toBeVisible();

	await page.reload();
	await expect(page).toHaveURL(/\/setup\/done$/);
	await expect(page.getByText('Baleia is ready')).toBeVisible();
});
