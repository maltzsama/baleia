import { describe, expect, it, vi } from 'vitest';
import { http, server } from '../../test/server';
import {
	makeContentSearchItem,
	makeSearchItem,
	makeSearchResponse,
	makeSearchResult
} from '../../test/factories';
import { searchPaletteProvider } from './palette';

const WS = '00000000-0000-0000-0000-000000000001';

function setup(openSavedQuery: (id: string) => boolean = () => true) {
	const calls = {
		insert: vi.fn(),
		openSavedQuery: vi.fn(openSavedQuery),
		insertMetric: vi.fn(async () => {}),
		navigate: vi.fn()
	};
	const provider = searchPaletteProvider({ workspaceId: () => WS, ...calls });
	return { provider, ...calls };
}

describe('search palette provider (#943)', () => {
	it('lists one ranked list, each hit labelled with its kind', async () => {
		let asked: URL | undefined;
		server.use(
			http.get('/workspaces/{id}/search', ({ request, response }) => {
				asked = new URL(request.url);
				return response(200).json(
					makeSearchResponse([
						makeSearchItem(),
						makeContentSearchItem('semantic_metric', 'm1', 'Revenue'),
						makeContentSearchItem('saved_query', 'q1', 'Revenue by month')
					])
				);
			})
		);
		const { provider } = setup();

		const items = await provider.search('  revenue ');

		expect(asked?.searchParams.get('q')).toBe('revenue');
		expect(asked?.searchParams.get('limit')).toBe('20');
		expect(items.map((i) => [i.label, i.group, i.detail])).toEqual([
			['orders', 'Table', 'sales'],
			['Revenue', 'Metric', 'Finance'],
			['Revenue by month', 'Saved query', 'Finance']
		]);
	});

	it('asks nothing for a query shorter than two characters', async () => {
		// An unmocked request fails the test, so no handler is the assertion.
		const { provider } = setup();
		expect(await provider.search('r')).toEqual([]);
	});

	it('does what each kind means in the editor', async () => {
		const column = makeSearchItem({
			kind: 'column',
			id: 'c1',
			title: 'order_note',
			metadata: makeSearchResult({ kind: 'column', column: 'order_note' })
		});
		server.use(
			http.get('/workspaces/{id}/search', ({ response }) =>
				response(200).json(
					makeSearchResponse([
						makeSearchItem(),
						column,
						makeContentSearchItem('semantic_metric', 'm1', 'Revenue'),
						makeContentSearchItem('saved_query', 'q1', 'Revenue by month'),
						makeContentSearchItem('semantic_model', 'd1', 'Orders model'),
						makeContentSearchItem('folder', 'f1', 'Reports')
					])
				)
			)
		);
		const { provider, insert, insertMetric, openSavedQuery, navigate } = setup();
		const items = await provider.search('orders');
		const run = (label: string) => items.find((i) => i.label === label)!.run();

		await run('orders');
		expect(insert).toHaveBeenLastCalledWith('sales.orders');
		await run('order_note');
		expect(insert).toHaveBeenLastCalledWith('order_note');
		await run('Revenue');
		expect(insertMetric).toHaveBeenCalledWith('m1');
		await run('Revenue by month');
		expect(openSavedQuery).toHaveBeenCalledWith('q1');
		expect(navigate).not.toHaveBeenCalled();
		await run('Orders model');
		expect(navigate).toHaveBeenLastCalledWith('/models/d1');
		await run('Reports');
		expect(navigate).toHaveBeenLastCalledWith('/content?folder=f1');
	});

	it('opens a saved query the editor does not know as a page', async () => {
		server.use(
			http.get('/workspaces/{id}/search', ({ response }) =>
				response(200).json(
					makeSearchResponse([makeContentSearchItem('saved_query', 'q9', 'Elsewhere')])
				)
			)
		);
		const { provider, navigate } = setup(() => false);
		const [item] = await provider.search('elsewhere');
		await item.run();
		expect(navigate).toHaveBeenCalledWith('/sql/q/q9');
	});
});
