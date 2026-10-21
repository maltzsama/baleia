import { expect, test } from './fixtures';

// #395: the editor/results split is resizable, and Run executes only the
// statement at the cursor while Run all executes the script.
test('@issue-395 Run executes one statement and the results split resizes', async ({
	page,
	loginAs,
	expectNoA11yViolations
}) => {
	await loginAs('member');
	const editor = page.locator('.cm-content');
	await editor.click();
	await page.keyboard.press('ControlOrMeta+a');
	await page.keyboard.press('Delete');
	await page.keyboard.insertText('SELECT 1 AS first;\nSELECT 2 AS second');
	// Cursor on the second line: Run executes only that statement.
	await page.keyboard.press('ControlOrMeta+End');
	await page.keyboard.press('Escape');

	await page.getByTestId('run-current').click();
	const results = page.getByTestId('results-area');
	await expect(results.getByText('second', { exact: true })).toBeVisible({ timeout: 30_000 });
	await expect(results.getByText('first', { exact: true })).toHaveCount(0);

	// Drag the separator up: the results get taller.
	const split = page.getByTestId('results-split');
	const before = (await results.boundingBox())!.height;
	const handle = (await split.boundingBox())!;
	await page.mouse.move(handle.x + handle.width / 2, handle.y + handle.height / 2);
	await page.mouse.down();
	await page.mouse.move(handle.x + handle.width / 2, handle.y - 150, { steps: 5 });
	await page.mouse.up();
	await expect
		.poll(async () => (await results.boundingBox())!.height)
		.toBeGreaterThan(before + 100);

	// Keyboard: End shrinks it to the minimum; the value is exposed.
	await split.focus();
	await page.keyboard.press('End');
	await expect(split).toHaveAttribute('aria-valuenow', '15');
	await expectNoA11yViolations(page);

	// Run all executes the whole script.
	await page.getByTestId('run-all').click();
	await expect(page.getByText('2 statements')).toBeVisible({ timeout: 30_000 });
});
