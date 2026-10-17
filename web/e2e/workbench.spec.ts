import { expect, test } from './fixtures';

// #112: the workbench owns its shortcuts and the command palette is a typed
// provider registry rather than a hard-coded menu.
test('@issue-112 the workbench opens the command palette with Ctrl+K', async ({
	page,
	loginAs
}) => {
	await loginAs('member');
	await page.goto('/sql');

	await page.keyboard.press('Control+K');
	await expect(page.getByLabel('Command palette')).toBeVisible();
	await expect(page.getByText('Run current statement')).toBeVisible();

	await page.keyboard.press('Escape');
	await expect(page.getByLabel('Command palette')).toBeHidden();
});
