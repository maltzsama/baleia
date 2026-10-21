import { describe, expect, it } from 'vitest';
import { http, server } from '../../test/server';
import { makeSemanticModel } from '../../test/factories';
import { modelPaletteProvider } from './palette';

const WS = '00000000-0000-0000-0000-000000000001';

describe('Insert from model provider', () => {
	it('lists dimensions and metrics and inserts the compiled SQL', async () => {
		let compiled: unknown;
		server.use(
			http.get('/workspaces/{id}/semantic/models', ({ response }) =>
				response(200).json([makeSemanticModel()])
			),
			http.post('/workspaces/{id}/semantic/explore/compile', async ({ request, response }) => {
				compiled = await request.json();
				return response(200).json({ sql: 'SELECT SUM(id) FROM orders', dialect: 'postgresql' });
			})
		);
		const inserted: string[] = [];
		const provider = modelPaletteProvider({
			workspaceId: () => WS,
			bindingId: () => 'b1',
			insert: (sql) => inserted.push(sql)
		});

		const items = await provider.search('revenue');
		expect(items.map((i) => [i.label, i.group])).toEqual([['Revenue', 'Models']]);
		await items[0].run();

		expect(inserted).toEqual(['SELECT SUM(id) FROM orders']);
		expect(compiled).toEqual({
			query: { model: 'orders', metrics: ['revenue'] },
			binding_id: 'b1'
		});
	});

	it('returns nothing without a workspace', async () => {
		const provider = modelPaletteProvider({
			workspaceId: () => '',
			bindingId: () => '',
			insert: () => {}
		});
		expect(await provider.search('')).toEqual([]);
	});
});
