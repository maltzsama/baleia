import { expect, test } from './fixtures';

// #601: the per-engine resource-group handover is exposed in Settings ›
// Workload. Saving with the switch on publishes the generated resource groups
// to Trino's db manager tables in the same transaction (#635), and Trino's
// resource group owns the queue; turning it off restores Baleia's admission
// queue. The seed pins BALEIA_WORKLOAD_ENABLED.
//
// The synthetic tpch connector cannot reliably produce a queued run, so this
// spec asserts the switch and its round-trip (select engine, toggle, save,
// reload) rather than a queued query; publishing, the revisions and the
// refusals are covered by the PutEngineQueues and store tests.

test('@issue-601 the Trino resource-groups switch persists across a save', async ({
	page,
	loginAs
}) => {
	await loginAs('admin');

	// The page loads the engines itself; pick the Trino one from that response,
	// since only a Trino engine has the switch (#635).
	const enginesLoaded = page.waitForResponse(
		(res) => new URL(res.url()).pathname === '/api/v1/engines' && res.request().method() === 'GET'
	);
	await page.goto('/settings/workload');
	const engines = (await (await enginesLoaded).json()) as { id: string; kind: string }[];
	const trino = engines.find((candidate) => candidate.kind === 'trino');
	expect(trino, 'the seed has a Trino engine').toBeTruthy();

	const engine = page.getByRole('combobox', { name: /engine/i });
	await expect(engine).toBeVisible();
	await engine.selectOption(trino!.id);

	const checkbox = page.getByRole('checkbox', { name: /resource groups/i });
	await expect(checkbox).toBeVisible();
	await checkbox.click();
	const wanted = await checkbox.isChecked();

	// The save publishes the resource groups when the switch is on; a failed
	// publish would fail the save and leave the switch where it was.
	const saved = page.waitForResponse(
		(res) =>
			new URL(res.url()).pathname === `/api/v1/engines/${trino!.id}/queues` &&
			res.request().method() === 'PUT'
	);
	await page.getByRole('button', { name: /save capacities/i }).click();
	expect((await saved).status()).toBe(200);

	// After the save the switch stays where the admin left it.
	if (wanted) await expect(checkbox).toBeChecked();
	else await expect(checkbox).not.toBeChecked();
});
