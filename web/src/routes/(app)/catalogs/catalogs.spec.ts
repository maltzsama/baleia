import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import { http, server } from '../../../test/server';
import { makeCatalog } from '../../../test/factories';
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
