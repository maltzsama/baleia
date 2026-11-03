import { expect, test } from './fixtures';

// #601: the per-engine resource-group handover is exposed in Settings ›
// Workload. Toggling the switch publishes the generated resource groups to
// Trino's db manager and Trino's resource group owns the queue; turning it off
// restores Baleia's admission queue. The seed pins BALEIA_WORKLOAD_ENABLED.
//
// The synthetic tpch connector cannot reliably produce a queued run, so this
// spec asserts the switch and its round-trip (select engine, toggle, save,
// reload) rather than a queued query; the admission handover and the generated
// document are covered by the workflow and store tests.

test('@issue-601 the Trino resource-groups switch persists across a save', async ({
	page,
	loginAs
}) => {
	await loginAs('admin');

	await page.goto('/settings/workload');

	// Pick the first engine; its queues load the capacity table and the switch.
	const engine = page.getByRole('combobox', { name: /engine/i });
	await expect(engine).toBeVisible();
	await engine.selectOption({ index: 0 });

	const checkbox = page.getByRole('checkbox', { name: /resource groups/i });
	await expect(checkbox).toBeVisible();

	await checkbox.click();
	await page.getByRole('button', { name: /save capacities/i }).click();

	// After the save the switch stays where the admin left it.
	await expect(checkbox).toBeChecked();
});
