import { expect, test } from './fixtures';

// #112: the workbench owns its shortcuts and the command palette is a typed
// provider registry rather than a hard-coded menu.
test('@issue-112 the workbench opens the command palette', async ({ page, loginAs }) => {
	await loginAs('member');
	await page.goto('/sql');

	await page.keyboard.press('ControlOrMeta+k');
	await expect(page.getByRole('combobox', { name: 'Command palette' })).toBeVisible();
	await expect(page.getByText('Run current statement')).toBeVisible();

	await page.keyboard.press('Escape');
	await expect(page.getByRole('combobox', { name: 'Command palette' })).toBeHidden();
});
