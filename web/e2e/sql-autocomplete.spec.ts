import { expect, test, type Page } from './fixtures';

// #383: the workbench autocomplete follows the statement context and the tab's
// catalog. The seed (cmd/e2e-seed) syncs the tpch catalog's metadata.
async function typeSql(page: Page, text: string) {
	const editor = page.locator('.cm-content');
	await editor.click();
	await page.keyboard.press('ControlOrMeta+a');
	await page.keyboard.press('Delete');
	await page.keyboard.type(text);
}

const options = (page: Page) =>
	page.getByRole('listbox', { name: 'Completions' }).getByRole('option');

test('@issue-383 autocomplete follows the statement context', async ({
	page,
	loginAs,
	expectNoA11yViolations
}) => {
	await loginAs('member');

	// Start of a statement: statement keywords, not functions.
	await typeSql(page, 's');
	await expect(options(page).first()).toHaveAccessibleName('SELECT');
	await expect(options(page).filter({ hasText: /^SHA1/ })).toHaveCount(0);

	// A schema lists only its tables.
	await typeSql(page, 'SELECT * FROM tiny.');
	await expect(page.getByRole('option', { name: 'nation tiny', exact: true })).toHaveCount(1);
	await expect(options(page).filter({ hasText: /^ABS/ })).toHaveCount(0);

	// An alias offers only that table's columns, also while typing.
	await typeSql(page, 'SELECT * FROM tiny.nation n WHERE n.na');
	await expect(options(page)).toHaveCount(2);
	await expect(options(page).nth(0)).toHaveAccessibleName('nationkey bigint');
	await expect(options(page).nth(1)).toHaveAccessibleName('name varchar(25)');
	await expect(page.locator('.cm-completionInfo')).toContainText('nation');
	await expectNoA11yViolations(page);

	// Accepting a column inserts it at the cursor.
	await page.keyboard.press('ArrowDown');
	await page.keyboard.press('Enter');
	await expect(page.locator('.cm-content')).toContainText('WHERE n.name');

	// Nothing inside a string.
	await typeSql(page, "SELECT * FROM tiny.nation WHERE name = 'na");
	await page.waitForTimeout(500);
	await expect(page.locator('.cm-tooltip-autocomplete')).toHaveCount(0);
});
