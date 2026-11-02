import { expect, test, type Page } from './fixtures';

// #545: workload admission is wired end to end. The seed pins the Trino
// engine's interactive queue and enables BALEIA_WORKLOAD_ENABLED, so a run
// must be admitted by the per-engine admission workflow before it executes.
// The synthetic tpch connector generates rows in memory, so there is no query
// slow enough to hold a slot reliably; the queued state and its position are
// covered by the reducer and admission-workflow tests instead.

async function setScript(page: Page, text: string) {
	await page.locator('.cm-content').click();
	await page.keyboard.press('ControlOrMeta+a');
	await page.keyboard.press('Delete');
	await page.keyboard.insertText(text);
	await page.keyboard.press('Escape');
}

test('@issue-545 a run is admitted and completes with workload enabled', async ({
	page,
	loginAs
}) => {
	await loginAs('member');

	await setScript(page, 'SELECT 42 AS answer');
	await page.getByTestId('run-current').click();

	// The run requests a slot, is admitted, and finishes. If the admission
	// workflow did not admit it, this would hang (the 24 h safety timer).
	await expect(page.getByTestId('results-area').getByText('42', { exact: true })).toBeVisible({
		timeout: 30_000
	});
});
