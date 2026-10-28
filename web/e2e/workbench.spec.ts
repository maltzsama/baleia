import { expect, test } from './fixtures';

// #112: the workbench owns its shortcuts and the command palette is a typed
// provider registry rather than a hard-coded menu.
test('@issue-112 the workbench opens the command palette', async ({ page, loginAs }) => {
	await loginAs('member');
	await page.goto('/sql');

	await page.getByRole('button', { name: 'Commands' }).click();
	await expect(page.getByRole('textbox', { name: 'Command palette' })).toBeVisible();
	await expect(page.getByText('Run current statement')).toBeVisible();

	await page.keyboard.press('Escape');
	await expect(page.getByRole('textbox', { name: 'Command palette' })).toBeHidden();
});
