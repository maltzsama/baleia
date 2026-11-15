import { expect, test } from './fixtures';

// #1062: an unknown route renders the first-class not-found surface instead of
// the bare SPA fallback.
test('@issue-1062 unknown routes show the branded not-found page', async ({
	page,
	expectNoA11yViolations
}) => {
	await page.goto('/this-route-does-not-exist');

	await expect(page.getByRole('heading', { level: 1, name: 'Page not found' })).toBeVisible();
	await expect(page.getByRole('link', { name: 'Go to Workbench' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Go back' })).toBeVisible();
	await expectNoA11yViolations(page);
});

// #1062: a valid deep link still loads the app on a full refresh, so the
// fallback and the client router keep working together.
test('@issue-1062 a valid nested route still loads on a full refresh', async ({
	page,
	loginAs
}) => {
	await loginAs('admin');
	await page.goto('/connections');
	await expect(page.getByRole('heading', { level: 1, name: 'Connections' })).toBeVisible();
});
