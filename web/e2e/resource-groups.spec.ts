import { expect, test } from './fixtures';

// #601: the per-engine resource-group handover is exposed in Settings ›
// Workload. Toggling the switch publishes the generated resource groups to
// Trino's db manager and Trino's resource group owns the queue; turning it off
// restores Baleia's admission queue. The seed pins BALEIA_WORKLOAD_ENABLED.
//
// The synthetic tpch connector cannot reliably produce a queued run, so this
// spec asserts the switch and its round-trip (toggle, save, reload) rather
// than a queued query; the admission handover and the generated document are
// covered by the workflow and store tests.

test('@issue-601 the Trino resource-groups switch persists across a save', async ({
	page,
	loginAs
}) => {
	await loginAs('admin');

	await page.goto('/settings/workload');
	await page.getByRole('combobox').first().selectOption({ index: 1 });

	const checkbox = page.getByRole('checkbox', { name: /resource groups/i });
	await expect(checkbox).toBeVisible();

	const wasChecked = await checkbox.isChecked();
	await checkbox.click();
	await expect(checkbox).toBeChecked({ checked: !wasChecked });

	await page.getByRole('button', { name: /save/i }).click();
	await expect(checkbox).toBeChecked({ checked: !wasChecked });
});
