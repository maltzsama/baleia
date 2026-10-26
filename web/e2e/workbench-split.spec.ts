import { expect, test, type Page } from './fixtures';

// #395: ⌘/Ctrl+Enter and the Run button execute the selection, or the
// statement at the cursor; ⇧⌘/Ctrl+Shift+Enter executes the script, and the
// toolbar has no Run all button (#417).
// The editor/results split is resizable.

async function setScript(page: Page, text: string) {
	await page.locator('.cm-content').click();
	await page.keyboard.press('ControlOrMeta+a');
	await page.keyboard.press('Delete');
	await page.keyboard.insertText(text);
	await page.keyboard.press('Escape');
}

// The SQL of every execution the page starts, in order, from the API's echo
// (the client's request body is a stream Playwright does not expose).
function recordRuns(page: Page): string[] {
	const sent: string[] = [];
	page.on('response', async (response) => {
		const request = response.request();
		if (request.method() !== 'POST' || !/\/executions$/.test(new URL(request.url()).pathname))
			return;
		sent.push((await response.json()).sql);
	});
	return sent;
}

test('@issue-395 Ctrl+Enter runs the statement at the cursor or the selection', async ({
	page,
	loginAs
}) => {
	await loginAs('member');
	const sent = recordRuns(page);
	const script = 'SELECT 1 AS first;\nSELECT 2 AS second';

	// Cursor on the second line: only that statement runs, and the shortcut
	// does not insert a line.
	await setScript(page, script);
	await page.keyboard.press('ControlOrMeta+End');
	await page.keyboard.press('ControlOrMeta+Enter');
	await expect.poll(() => sent).toEqual(['SELECT 2 AS second']);
	await expect(page.locator('.cm-content')).toHaveText(script.replace('\n', ''));
	await expect(page.getByTestId('results-area').getByText('second', { exact: true })).toBeVisible({
		timeout: 30_000
	});

	// A selection runs as is.
	await page.keyboard.press('ControlOrMeta+Home');
	await page.keyboard.press('Shift+End');
	await page.keyboard.press('ControlOrMeta+Enter');
	await expect.poll(() => sent).toEqual(['SELECT 2 AS second', 'SELECT 1 AS first;']);

	// Shift adds the whole script; the button keeps saying Run.
	await expect(page.getByTestId('run-current')).toContainText('Run');
	await expect(page.getByTestId('run-all')).toHaveCount(0);
	await expect(page.getByTestId('results-area').getByText('first', { exact: true })).toBeVisible({
		timeout: 30_000
	});
	await page.keyboard.press('ControlOrMeta+Shift+Enter');
	await expect.poll(() => sent).toHaveLength(3);
	expect(sent[2]).toBe(script);
});

test('@issue-395 the results split resizes with the pointer and the keyboard', async ({
	page,
	loginAs,
	expectNoA11yViolations
}) => {
	await loginAs('member');
	await setScript(page, 'SELECT 1 AS one');
	await page.getByTestId('run-current').click();
	const results = page.getByTestId('results-area');
	await expect(results.getByText('one', { exact: true })).toBeVisible({ timeout: 30_000 });

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

	await split.focus();
	await page.keyboard.press('End');
	await expect(split).toHaveAttribute('aria-valuenow', '15');
	await expectNoA11yViolations(page);
});
