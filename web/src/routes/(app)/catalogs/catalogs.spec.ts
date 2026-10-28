import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import { authStore } from '$lib/stores/auth.svelte';
import { http, server } from '../../../test/server';
import { makeCatalog, makeMe } from '../../../test/factories';
import CatalogsPage from './+page.svelte';

const summary = (overrides: Parameters<typeof makeCatalog>[0]) => ({
	...makeCatalog(overrides),
	origin: 'native',
	bindings: [],
	needs_attention: false
});

describe('catalogs page', () => {
	// #454: the list labels every metadata sync state, and "never" is not green.
	it('labels a catalog that never synced', async () => {
		server.use(
			http.get('/catalogs', ({ response }) =>
				response(200).json([
					summary({ id: 'c1', name: 'fresh', metadata_sync_status: 'never' }),
					summary({ id: 'c2', name: 'done', metadata_sync_status: 'finished' })
				])
			),
			http.get('/engines', ({ response }) => response(200).json([]))
		);
		render(CatalogsPage);

		expect(await screen.findByText('Never synced')).toBeTruthy();
		expect(screen.getByText('Synced')).toBeTruthy();
		expect(screen.queryByText('never')).toBeNull();
	});
});

// #476: an install with no catalog shows the next step; filters that match
// nothing offer to clear them instead.
describe('catalogs page empty states', () => {
	it('points to connections when there is no catalog', async () => {
		authStore.set(makeMe());
		server.use(
			http.get('/catalogs', ({ response }) => response(200).json([])),
			http.get('/engines', ({ response }) => response(200).json([]))
		);
		render(CatalogsPage);

		expect(await screen.findByText('No catalogs yet')).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Go to connections' })).toBeTruthy();
	});

	it('clears filters that match nothing', async () => {
		server.use(
			http.get('/catalogs', ({ request, response }) =>
				response(200).json(
					new URL(request.url).searchParams.get('attention')
						? []
						: [summary({ id: 'c1', name: 'lake' })]
				)
			),
			http.get('/engines', ({ response }) => response(200).json([]))
		);
		render(CatalogsPage);
		expect(await screen.findByText('lake')).toBeTruthy();

		await fireEvent.click(screen.getByRole('checkbox'));
		expect(await screen.findByText('No catalog matches these filters')).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
		expect(await screen.findByText('lake')).toBeTruthy();
	});
});
