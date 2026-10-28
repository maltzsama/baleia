// @vitest-environment node
import { describe, expect, it } from 'vitest';
// @ts-expect-error -- plain ESM script without type declarations
import { findHardCodedText } from '../../../scripts/check-i18n.mjs';

const find = (path: string, source: string): string[] => findHardCodedText(path, source);

// #469: npm run check fails on a reintroduced literal, wherever it renders.
describe('check-i18n', () => {
	it('finds text in every branch, not only the first', () => {
		const source = `{#if a}<p>{x}</p>{:else}<p>Nothing yet</p>{/if}
{#await p}{:then v}<span>Loaded</span>{:catch}<span>Failed</span>{/await}
{#snippet row()}<td>Open</td>{/snippet}`;
		expect(find('src/x.svelte', source)).toEqual(['Nothing yet', 'Loaded', 'Failed', 'Open']);
	});

	it('finds text attributes and literals in expressions', () => {
		const source = `<Select label="Catalog" />
<button aria-label={open ? 'Close' : 'Open menu'}>{'Create'}</button>
<span title={\`\${n} items\`}></span>`;
		expect(find('src/x.svelte', source)).toEqual([
			'Catalog',
			'Close',
			'Open menu',
			'Create',
			'{} items'
		]);
	});

	it('finds copy in script code and label tables', () => {
		const svelte = `<script lang="ts">
	let error = $state('');
	function fail() {
		error = 'Could not save';
		toasts.success('Saved.');
	}
</script>`;
		expect(find('src/x.svelte', svelte)).toEqual(['Could not save', 'Saved.']);
		expect(
			find('src/lib/x.ts', `export const s = [{ href: '/a', label: 'System status' }];`)
		).toEqual(['System status']);
	});

	it('leaves code, class lists and messages alone', () => {
		const source = `<script lang="ts">
	let status = $state('running');
	const label = m.common_save();
</script>
<code>SELECT 1 FROM orders</code>
<div class="flex gap-2 text-[11px] dark:text-slate-300">{m.common_save()}</div>
<input placeholder="(uid={{username}})" />
<Badge label="SQL" />`;
		expect(find('src/x.svelte', source)).toEqual([]);
	});
});
