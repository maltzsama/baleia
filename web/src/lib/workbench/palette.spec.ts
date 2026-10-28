import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { registerProvider, resetProviders } from '$lib/palette/registry';
import CommandPalette from './CommandPalette.svelte';

describe('command palette', () => {
	beforeEach(() => {
		resetProviders();
	});

	it('searches the registered providers and runs the selected item on Enter', async () => {
		const run = vi.fn();
		registerProvider({
			id: 'test',
			label: 'Test',
			search: (query) =>
				[{ id: 'alpha', label: 'Alpha', group: 'Test', run }].filter((item) =>
					item.label.toLowerCase().includes(query.toLowerCase())
				)
		});

		render(CommandPalette, { props: { open: true } });

		expect(await screen.findByText('Alpha')).toBeTruthy();
		await fireEvent.keyDown(screen.getByRole('textbox', { name: 'Command palette' }), {
			key: 'Enter'
		});
		await waitFor(() => expect(run).toHaveBeenCalledOnce());
	});

	it('quick open only queries the saved-query provider', async () => {
		const queries = vi.fn(() => [
			{ id: 'q', label: 'Revenue', group: 'Saved queries', run: vi.fn() }
		]);
		const tables = vi.fn(() => [{ id: 't', label: 'Orders', group: 'Tables', run: vi.fn() }]);
		registerProvider({ id: 'queries', label: 'Saved queries', search: queries });
		registerProvider({ id: 'tables', label: 'Tables', search: tables });

		render(CommandPalette, { props: { open: true, mode: 'quick' } });

		expect(await screen.findByText('Revenue')).toBeTruthy();
		expect(screen.queryByText('Orders')).toBeNull();
		expect(tables).not.toHaveBeenCalled();
	});
});
