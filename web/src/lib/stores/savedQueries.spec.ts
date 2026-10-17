import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../test/server';
import { makeFolder, makeSavedQuery, makeSpace } from '../../test/factories';
import { savedQueries } from './savedQueries.svelte';

function mockContent(
	spaces: ReturnType<typeof makeSpace>[],
	queries: ReturnType<typeof makeSavedQuery>[],
	folders: ReturnType<typeof makeFolder>[] = []
) {
	server.use(
		http.get('/workspaces/{id}/spaces', ({ response }) => response(200).json(spaces)),
		http.get('/workspaces/{id}/queries', ({ response }) => response(200).json(queries)),
		http.get('/workspaces/{id}/folders', ({ response }) => response(200).json(folders))
	);
}

describe('savedQueries store', () => {
	beforeEach(() => {
		void savedQueries.reload('');
	});

	it('loads spaces, queries and folders together', async () => {
		mockContent([makeSpace()], [makeSavedQuery()]);
		await savedQueries.reload('w1');
		expect(savedQueries.spaces).toHaveLength(1);
		expect(savedQueries.list).toHaveLength(1);
		expect(savedQueries.queriesIn('00000000-0000-0000-0000-000000000006')).toHaveLength(1);
		expect(savedQueries.foldersIn('00000000-0000-0000-0000-000000000006')).toHaveLength(0);
	});

	it('derives edit and manage permissions from my_role', async () => {
		mockContent(
			[
				makeSpace({ id: 's-editor', my_role: 'editor', is_default: false }),
				makeSpace({ id: 's-viewer', my_role: 'viewer', is_default: false }),
				makeSpace({ id: 's-manager', my_role: 'manager', is_default: false })
			],
			[]
		);
		await savedQueries.reload('w1');
		expect(savedQueries.role('s-editor')).toBe('editor');
		expect(savedQueries.canEdit('s-editor')).toBe(true);
		expect(savedQueries.canEdit('s-viewer')).toBe(false);
		expect(savedQueries.canManage('s-manager')).toBe(true);
		expect(savedQueries.canManage('s-editor')).toBe(false);
		expect(savedQueries.role('missing')).toBeNull();
	});

	it('surfaces a load error', async () => {
		server.use(
			http.get('/workspaces/{id}/spaces', ({ response }) =>
				response(404).json({ code: 'not_found', message: 'boom' })
			),
			http.get('/workspaces/{id}/queries', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/folders', ({ response }) => response(200).json([]))
		);
		await savedQueries.reload('w1');
		expect(savedQueries.error).toContain('boom');
	});
});
