// Streaming Arrow results (#26). Batches are decoded as they arrive and kept
// columnar: no row objects, no full materialization.
import { RecordBatchReader, type RecordBatch } from 'apache-arrow';
import { apiFetch } from '$lib/api/client';
import { columnMeta, type ColumnMeta } from './format';

function countBytes(
	body: ReadableStream<Uint8Array>,
	onProgress: (bytes: number) => void
): ReadableStream<Uint8Array> {
	let total = 0;
	return body.pipeThrough(
		new TransformStream<Uint8Array, Uint8Array>({
			transform(chunk, controller) {
				total += chunk.byteLength;
				onProgress(total);
				controller.enqueue(chunk);
			}
		})
	);
}

async function responseError(response: Response): Promise<string> {
	const fallback = `Could not load the results (HTTP ${response.status})`;
	const text = await response.text().catch(() => '');
	if (!text) return fallback;
	try {
		const body = JSON.parse(text) as { message?: string };
		return body.message ?? fallback;
	} catch {
		return text.slice(0, 200);
	}
}

function isAbort(error: unknown): boolean {
	return error instanceof DOMException && error.name === 'AbortError';
}

export class ResultsStore {
	columns = $state<ColumnMeta[]>([]);
	// Batch objects are class instances; raw state avoids proxying them.
	batches = $state.raw<RecordBatch[]>([]);
	offsets = $state.raw<number[]>([0]);
	rowCount = $state(0);
	bytes = $state(0);
	totalBytes = $state(0);
	done = $state(false);
	loading = $state(false);
	error = $state('');

	private controller: AbortController | undefined;

	reset(): void {
		this.cancel();
		this.columns = [];
		this.batches = [];
		this.offsets = [0];
		this.rowCount = 0;
		this.bytes = 0;
		this.totalBytes = 0;
		this.done = false;
		this.loading = false;
		this.error = '';
	}

	cancel(): void {
		this.controller?.abort();
		this.controller = undefined;
	}

	async load(url: string): Promise<void> {
		this.reset();
		const controller = new AbortController();
		this.controller = controller;
		this.loading = true;
		try {
			const response = await apiFetch(url, { signal: controller.signal });
			if (!response.ok) throw new Error(await responseError(response));
			if (!response.body) throw new Error('The result stream is empty');

			this.totalBytes = Number(response.headers.get('Content-Length') ?? 0);
			const body = countBytes(response.body, (bytes) => {
				this.bytes = bytes;
			});
			const reader = await RecordBatchReader.from(body);
			for await (const batch of reader) {
				if (controller.signal.aborted) break;
				if (batch.numRows === 0) continue;
				if (this.columns.length === 0) {
					this.columns = batch.schema.fields.map(columnMeta);
				}
				const previous = this.offsets[this.offsets.length - 1] ?? 0;
				this.offsets.push(previous + batch.numRows);
				this.batches.push(batch);
				this.rowCount = previous + batch.numRows;
			}
			if (!controller.signal.aborted) this.done = true;
		} catch (error) {
			if (!isAbort(error)) this.error = error instanceof Error ? error.message : String(error);
		} finally {
			if (this.controller === controller) this.controller = undefined;
			this.loading = false;
		}
	}

	/** The cell at a global row and column index. */
	cell(row: number, column: number): unknown {
		const batches = this.batches;
		if (batches.length === 0) return null;
		const offsets = this.offsets;
		let low = 0;
		let high = batches.length - 1;
		while (low < high) {
			const mid = (low + high + 1) >> 1;
			if (offsets[mid] <= row) low = mid;
			else high = mid - 1;
		}
		const batch = batches[low];
		const vector = batch.getChildAt(column);
		if (!vector) return null;
		return vector.get(row - offsets[low]);
	}
}
