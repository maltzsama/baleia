import { fireEvent, render, screen } from '@testing-library/svelte';
import { tableFromArrays, tableToIPC } from 'apache-arrow';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ResultsStore } from '$lib/results/store.svelte';
import ResultGrid from './ResultGrid.svelte';

function ipc(values: Record<string, unknown[]>): ArrayBuffer {
	const bytes = tableToIPC(tableFromArrays(values), 'stream');
	const buffer = new ArrayBuffer(bytes.byteLength);
	new Uint8Array(buffer).set(bytes);
	return buffer;
}

// Arrow results are not JSON: like the store's own tests, serve the bytes
// from a stubbed fetch.
async function loaded(values: Record<string, unknown[]>): Promise<ResultsStore> {
	vi.stubGlobal(
		'fetch',
		vi.fn(async () => new Response(ipc(values), { status: 200 }))
	);
	const store = new ResultsStore();
	await store.load('/workspaces/ws/executions/e/statements/1/results');
	return store;
}

// #471: the grid is entered with Tab and navigated with the keyboard.
describe('ResultGrid keyboard', () => {
	afterEach(() => vi.unstubAllGlobals());

	it('starts on the first cell when focused and moves with the arrows', async () => {
		const store = await loaded({ id: [1, 2, 3], note: ['a', 'b', 'c'] });
		render(ResultGrid, { props: { store } });
		const grid = screen.getByRole('grid');
		expect(grid.getAttribute('aria-rowcount')).toBe('4');
		expect(grid.getAttribute('aria-colcount')).toBe('2');

		await fireEvent.focus(grid);
		const first = document.getElementById(grid.getAttribute('aria-activedescendant')!)!;
		expect(first.textContent?.trim()).toBe('1');
		expect(first.getAttribute('aria-colindex')).toBe('1');
		expect(first.parentElement?.getAttribute('aria-rowindex')).toBe('2');

		await fireEvent.keyDown(grid, { key: 'ArrowDown' });
		await fireEvent.keyDown(grid, { key: 'ArrowRight' });
		const active = document.getElementById(grid.getAttribute('aria-activedescendant')!)!;
		expect(active.textContent?.trim()).toBe('b');
		expect(active.getAttribute('aria-selected')).toBe('true');

		// Moving past the edges stays on the last row and column.
		await fireEvent.keyDown(grid, { key: 'ArrowDown' });
		await fireEvent.keyDown(grid, { key: 'ArrowDown' });
		await fireEvent.keyDown(grid, { key: 'Home' });
		const edge = document.getElementById(grid.getAttribute('aria-activedescendant')!)!;
		expect(edge.textContent?.trim()).toBe('3');
	});

	it('does not take keys pressed in other controls', async () => {
		const store = await loaded({ id: [1, 2] });
		render(ResultGrid, { props: { store } });
		const grid = screen.getByRole('grid');
		await fireEvent.focus(grid);
		const before = grid.getAttribute('aria-activedescendant');

		const outside = document.createElement('input');
		document.body.append(outside);
		await fireEvent.keyDown(outside, { key: 'ArrowDown' });
		expect(grid.getAttribute('aria-activedescendant')).toBe(before);
		outside.remove();
	});

	it('has no active cell in an empty grid', async () => {
		const store = await loaded({ id: [] as number[] });
		render(ResultGrid, { props: { store } });
		const grid = screen.getByRole('grid');
		await fireEvent.focus(grid);
		expect(grid.getAttribute('aria-activedescendant')).toBeNull();
	});
});
