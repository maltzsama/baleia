import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import type { components } from '$lib/api/schema';
import { http, server } from '../../test/server';
import AttentionBell from './AttentionBell.svelte';

type Item = components['schemas']['AttentionItem'];

const items: Item[] = [
	{
		kind: 'binding_error',
		severity: 'error',
		resource: { type: 'binding', id: 'b1', name: 'lake.orders' },
		since: '2026-01-01T00:00:00Z',
		href: '/catalogs/c1?tab=bindings'
	},
	{
		kind: 'metadata_stale',
		severity: 'warning',
		resource: { type: 'catalog', id: 'c1', name: 'lake' },
		since: '2026-01-01T00:00:00Z',
		href: '/catalogs/c1'
	}
];

describe('attention bell', () => {
	beforeEach(() => {
		localStorage.clear();
	});

	it('counts errors first and links every item to its fix', async () => {
		server.use(http.get('/attention', ({ response }) => response(200).json(items)));
		render(AttentionBell);

		const bell = await screen.findByRole('button', { name: 'Attention: 1 items' });
		await fireEvent.click(bell);

		expect(await screen.findByText(/Catalog binding error/)).toBeTruthy();
		expect(screen.getByText(/Metadata is stale/)).toBeTruthy();

		const link = screen.getByRole('link', { name: /Catalog binding error/ });
		expect(link.getAttribute('href')).toContain('/catalogs/c1');
	});

	it('refreshes on focus', async () => {
		let calls = 0;
		server.use(
			http.get('/attention', ({ response }) => {
				calls += 1;
				return response(200).json(items);
			})
		);
		render(AttentionBell);
		await screen.findByRole('button', { name: 'Attention: 1 items' });

		window.dispatchEvent(new Event('focus'));
		await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2));
	});
});
