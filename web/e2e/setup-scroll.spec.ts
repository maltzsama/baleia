import { expect, test } from './fixtures';

// #674: the document never scrolls (app.css), so the first-run setup page has
// to scroll its own panel. Step 2 embeds the whole connection wizard, which is
// taller than a small window; before the fix its lower fields were cut off and
// unreachable. The API is stubbed so the page can be driven on an instance that
// is already set up.
test('@issue-674 setup step 2 scrolls to the last field of a tall form', async ({ page }) => {
	await page.setViewportSize({ width: 1280, height: 560 });

	const fields = Array.from({ length: 14 }, (_, i) => ({
		key: `field_${i + 1}`,
		label: `Field ${i + 1}`,
		type: 'text',
		required: false,
		secret: false
	}));
	await page.route('**/api/v1/setup/status', (route) =>
		route.fulfill({ json: { is_configured: false, version: 'test' } })
	);
	await page.route('**/api/v1/setup/admin', (route) =>
		route.fulfill({ status: 201, json: { token: 'test-token' } })
	);
	await page.route('**/api/v1/engines', (route) => route.fulfill({ json: [] }));
	await page.route('**/api/v1/connection-providers', (route) =>
		route.fulfill({
			json: [
				{
					kind: 'testdb',
					label: 'Test Database',
					family: 'database',
					roles: ['metadata', 'direct_engine'],
					status: { state: 'supported' },
					config_schema: fields
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

	await page.getByTestId('provider-card').first().click();
	await expect(page.getByText('Step 2 of 4')).toBeVisible();

	// A real wheel event, like a person scrolling: it cannot move an element that
	// is clipped with overflow hidden, so it fails if the page does not scroll.
	await page.getByLabel('Field 1', { exact: true }).hover();
	await page.mouse.wheel(0, 3000);
	await expect(page.getByLabel('Field 14')).toBeInViewport();
	await expect(page.getByRole('button', { name: 'Skip for now' })).toBeInViewport();
});
