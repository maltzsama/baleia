import { describe, expect, it } from 'vitest';
import { catalogAccessStore } from './catalogAccess.svelte';

describe('catalogAccessStore', () => {
	it('moves the version on every change', () => {
		const before = catalogAccessStore.version;
		catalogAccessStore.changed();
		catalogAccessStore.changed();
		expect(catalogAccessStore.version).toBe(before + 2);
	});

	it('refetches on focus at most once per throttle window', () => {
		const t0 = 10_000_000;
		const before = catalogAccessStore.version;
		catalogAccessStore.focused(t0);
		catalogAccessStore.focused(t0 + 1_000);
		expect(catalogAccessStore.version).toBe(before + 1);
		catalogAccessStore.focused(t0 + 20_000);
		expect(catalogAccessStore.version).toBe(before + 2);
	});
});
