import { afterEach, describe, expect, it, vi } from 'vitest';
import { tableFromArrays, tableToIPC } from 'apache-arrow';
import { ResultsStore, RESULT_PAGE_SIZE } from './store.svelte';

function ipc(values: Record<string, unknown[]>): ArrayBuffer {
	const bytes = tableToIPC(tableFromArrays(values), 'stream');
	const buffer = new ArrayBuffer(bytes.byteLength);
	new Uint8Array(buffer).set(bytes);
	return buffer;
}

describe('ResultsStore', () => {
	afterEach(() => vi.unstubAllGlobals());

	it('decodes batches and reads cells columnar', async () => {
		const bytes = ipc({ id: [1, 2, 3], note: ['a', 'b', 'c'] });
		vi.stubGlobal(
			'fetch',
			vi.fn(async () => new Response(bytes, { status: 200 }))
		);

		const store = new ResultsStore();
		await store.load('/workspaces/ws/executions/e/statements/1/results');

		expect(store.columns.map((column) => column.name)).toEqual(['id', 'note']);
		expect(store.rowCount).toBe(3);
		expect(store.cell(1, 1)).toBe('b');
		expect(store.done).toBe(true);
		expect(store.error).toBe('');
	});

	it('reports bytes before the stream ends', async () => {
		const bytes = new Uint8Array(ipc({ id: [1, 2, 3] }));
		let push!: (chunk: Uint8Array) => void;
		let close!: () => void;
		const stream = new ReadableStream<Uint8Array>({
			start(controller) {
				push = (chunk) => controller.enqueue(chunk);
				close = () => controller.close();
			}
		});
		vi.stubGlobal(
			'fetch',
			vi.fn(async () => new Response(stream, { status: 200 }))
		);

		const store = new ResultsStore();
		const loading = store.load('/workspaces/ws/executions/e/statements/1/results');
		push(bytes.slice(0, 10));
		await new Promise((resolve) => setTimeout(resolve, 0));

		expect(store.bytes).toBe(10);
		expect(store.done).toBe(false);

		push(bytes.slice(10));
		close();
		await loading;

		expect(store.done).toBe(true);
		expect(store.rowCount).toBe(3);
	});

	it('surfaces the API error instead of swallowing it', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(
				async () =>
					new Response(
						JSON.stringify({ code: 'conflict', message: 'statement has no result yet' }),
						{
							status: 409
						}
					)
			)
		);

		const store = new ResultsStore();
		await store.load('/workspaces/ws/executions/e/statements/1/results');

		expect(store.error).toBe('statement has no result yet');
		expect(store.done).toBe(false);
	});

	it('fetches pages on demand with offset and limit', async () => {
		const calls: string[] = [];
		const pageBytes = ipc({ id: [1, 2, 3] });
		vi.stubGlobal(
			'fetch',
			vi.fn(async (url: string) => {
				calls.push(String(url));
				return new Response(pageBytes, {
					status: 200,
					headers: { 'X-Total-Rows': '1000000' }
				});
			})
		);

		const store = new ResultsStore();
		await store.load('/workspaces/ws/executions/e/statements/1/results', 0);

		expect(calls[0]).toContain('offset=0');
		expect(calls[0]).toContain(`limit=${RESULT_PAGE_SIZE}`);
		expect(store.rowCount).toBe(1000000);
		expect(store.cell(1, 0)).toBe(2);

		await store.ensurePage(1);
		expect(calls[1]).toContain(`offset=${RESULT_PAGE_SIZE}`);
		expect(store.cell(RESULT_PAGE_SIZE + 1, 0)).toBe(2);
		// A page that has not been fetched reads as null, not a wrong value.
		expect(store.cell(3 * RESULT_PAGE_SIZE, 0)).toBeNull();
	});
});
