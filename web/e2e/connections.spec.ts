import { expect, test } from './fixtures';

// #22: the onboarding flow is driven by GET /connection-providers, so the
// smoke asserts that the provider cards and the generated form come from the
// API rather than a hard-coded type table.
test('@issue-22 connection onboarding renders providers and the schema form', async ({
	page,
	loginAs,
	expectNoA11yViolations
}) => {
	await loginAs('admin');
	await page.goto('/connections/new');

	await expect(page.getByRole('heading', { name: 'New connection' })).toBeVisible();

	const card = page.getByTestId('provider-card').first();
	await expect(card).toBeVisible();
	await card.click();

	await expect(page.getByText('Step 2 of 4')).toBeVisible();
	await expect(page.getByTestId('provider-form')).toBeVisible();
	await expectNoA11yViolations(page);
});
