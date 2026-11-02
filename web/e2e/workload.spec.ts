import { expect, test, type Page } from './fixtures';

// #545: workload admission. The seed pins the default Trino engine's
// interactive queue to one slot, so a heavy first run holds it and a second
// run visibly waits before it starts.

async function setScript(page: Page, text: string) {
	await page.locator('.cm-content').click();
	await page.keyboard.press('ControlOrMeta+a');
	await page.keyboard.press('Delete');
	await page.keyboard.insertText(text);
	await page.keyboard.press('Escape');
}

// A full lineitem scan: slow enough that the second run waits, bounded enough
// to finish within the suite timeout.
const SLOW =
	'SELECT count(*) FROM e2e_tpch.sf1.lineitem l JOIN e2e_tpch.sf1.orders o ON l.l_orderkey = o.o_orderkey';

test('@issue-545 a second run queues at position 1 and starts after the first', async ({
	page,
	loginAs
}) => {
	await loginAs('member');

	await setScript(page, SLOW);
	await page.getByTestId('run-current').click();

	await setScript(page, 'SELECT 42 AS answer');
	await page.getByTestId('run-current').click();

	// The second run waits for the single interactive slot.
	await expect(page.getByText(/Queued · position 1/)).toBeVisible({ timeout: 15_000 });

	// Once the first run releases the slot, the second starts and finishes.
	await expect(page.getByTestId('results-area').getByText('42', { exact: true })).toBeVisible({
		timeout: 180_000
	});
});

test('@issue-545 cancel while queued', async ({ page, loginAs }) => {
	await loginAs('member');

	await setScript(page, SLOW);
	await page.getByTestId('run-current').click();

	await setScript(page, 'SELECT 7 AS lucky');
	await page.getByTestId('run-current').click();

	await expect(page.getByText(/Queued · position 1/)).toBeVisible({ timeout: 15_000 });
	await page.getByRole('button', { name: 'Cancel' }).click();
	await expect(page.getByText(/Queued · position/)).toHaveCount(0);
});
