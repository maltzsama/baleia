import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../test/server';
import { makeMetadataCatalog } from '../../test/factories';
import {
	clearMetadataCache,
	loadCatalogs,
	loadNamespaces,
	loadTables,
	searchMetadata
} from './client';

const WS = '00000000-0000-0000-0000-000000000001';
const CAT = '00000000-0000-0000-0000-0000000000c1';

describe('metadata client', () => {
	beforeEach(() => clearMetadataCache());

	it('caches catalogs for the session', async () => {
		let calls = 0;
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) => {
				calls += 1;
				return response(200).json([makeMetadataCatalog()]);
			})
		);

		const first = await loadCatalogs(WS);
		const second = await loadCatalogs(WS);

		expect(first).toBe(second);
		expect(calls).toBe(1);
	});

	it('does not cache a failed read, so a retry re-issues it', async () => {
		let calls = 0;
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) => {
				calls += 1;
				if (calls === 1) {
					return response(404).json({ code: 'not_found', message: 'boom' });
				}
				return response(200).json([makeMetadataCatalog()]);
			})
		);

		await expect(loadCatalogs(WS)).rejects.toThrow('boom');
		await expect(loadCatalogs(WS)).resolves.toHaveLength(1);
		expect(calls).toBe(2);
	});

	it('sends a multi-level namespace as repeated query parameters', async () => {
		let url = '';
		server.use(
			http.get(
				'/workspaces/{id}/metadata/catalogs/{catalog_id}/namespaces',
				({ request, response }) => {
					url = request.url;
					return response(200).json({ freshness: { metadata_synced_at: null }, items: [] });
				}
			)
		);

		await loadNamespaces(WS, CAT, ['sales', 'b.c']);

		expect(url).toContain('parent=sales');
		expect(url).toContain('parent=b.c');
	});

	it('pages tables by cursor', async () => {
		let url = '';
		server.use(
			http.get(
				'/workspaces/{id}/metadata/catalogs/{catalog_id}/tables',
				({ request, response }) => {
					url = request.url;
					return response(200).json({
						freshness: { metadata_synced_at: null },
						items: [],
						next_cursor: null
					});
				}
			)
		);

		await loadTables(WS, CAT, ['sales'], 'cursor-1', 100);

		expect(url).toContain('ns=sales');
		expect(url).toContain('cursor=cursor-1');
		expect(url).toContain('limit=100');
	});

	it('searches with the query, kind and limit', async () => {
		let url = '';
		server.use(
			http.get('/workspaces/{id}/metadata/search', ({ request, response }) => {
				url = request.url;
				return response(200).json([]);
			})
		);

		await searchMetadata(WS, 'ord', { kind: 'all', limit: 20 });

		expect(url).toContain('q=ord');
		expect(url).toContain('kind=all');
		expect(url).toContain('limit=20');
	});
});
