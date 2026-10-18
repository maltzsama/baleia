// Paged Arrow results (#26, #238). Batches are decoded columnar as they arrive,
// but only the pages the viewer actually reaches are fetched, so a large result
// never materializes fully in the browser.
import { RecordBatchReader, type RecordBatch } from 'apache-arrow';
import { apiFetch } from '$lib/api/client';
import { columnMeta, type ColumnMeta } from './format';

/** Rows fetched per page. The backend re-emits only the covering batches. */
export const RESULT_PAGE_SIZE = 50_000;
/** Pages kept in memory around the viewport; farther pages are evicted. */
const MAX_CACHED_PAGES = 6;

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

/** One loaded page: its record batches and their cumulative row offsets. */
type Page = { batches: RecordBatch[]; offsets: number[] };

export class ResultsStore {
	columns = $state<ColumnMeta[]>([]);
	/** The statement's full row count, from X-Total-Rows or the statement itself. */
	rowCount = $state(0);
	/** Bumps when a page lands; the raw page map is not itself reactive. */
	version = $state(0);
	bytes = $state(0);
	totalBytes = $state(0);
	done = $state(false);
	loading = $state(false);
	error = $state('');

	// Internal, deliberately non-reactive caches: the `version` signal drives
	// re-renders, and a SvelteMap here would make page loads re-enter the effect
	// that requests them.
	// eslint-disable-next-line svelte/prefer-svelte-reactivity
	private pages = new Map<number, Page>();
	// eslint-disable-next-line svelte/prefer-svelte-reactivity
	private inflight = new Map<number, Promise<void>>();
	private baseUrl = '';
	private activePage = 0;
	private controller: AbortController | undefined;

	reset(): void {
		this.cancel();
		this.columns = [];
		this.rowCount = 0;
		this.version = 0;
		this.bytes = 0;
		this.totalBytes = 0;
		this.done = false;
		this.loading = false;
		this.error = '';
		this.pages.clear();
		this.inflight.clear();
		this.baseUrl = '';
	}

	cancel(): void {
		this.controller?.abort();
		this.controller = undefined;
	}

	/**
	 * Starts a paged read. `totalRows` is the statement's row count; the first
	 * page is fetched immediately and later pages load as the viewer scrolls.
	 */
	async load(baseUrl: string, totalRows = 0): Promise<void> {
		this.reset();
		this.baseUrl = baseUrl;
		this.rowCount = totalRows;
		this.controller = new AbortController();
		this.loading = true;
		try {
			await this.ensurePage(0, true);
			this.done = true;
		} catch (error) {
			if (!isAbort(error)) this.error = error instanceof Error ? error.message : String(error);
		} finally {
			this.loading = false;
		}
	}

	/** Loads a page unless it is already loaded; concurrent calls share one request. */
	ensurePage(page: number, isFirst = false): Promise<void> {
		this.activePage = page;
		if (this.pages.has(page)) {
			this.evict();
			return Promise.resolve();
		}
		const running = this.inflight.get(page);
		if (running) return running;
		const pending = this.fetchPage(page, isFirst).finally(() => {
			this.inflight.delete(page);
			this.evict();
		});
		this.inflight.set(page, pending);
		return pending;
	}

	/** Keeps only the pages closest to the viewport, so scrolling cannot grow memory. */
	private evict(): void {
		if (this.pages.size <= MAX_CACHED_PAGES) return;
		const active = this.activePage;
		const keys = [...this.pages.keys()].sort((a, b) => Math.abs(a - active) - Math.abs(b - active));
		for (const key of keys.slice(MAX_CACHED_PAGES)) this.pages.delete(key);
		// The evicted rows must re-render as blanks, not stale values.
		this.version++;
	}

	private async fetchPage(page: number, isFirst: boolean): Promise<void> {
		const offset = page * RESULT_PAGE_SIZE;
		const sep = this.baseUrl.includes('?') ? '&' : '?';
		const url = `${this.baseUrl}${sep}offset=${offset}&limit=${RESULT_PAGE_SIZE}`;
		const response = await apiFetch(url, { signal: this.controller?.signal });
		if (!response.ok) throw new Error(await responseError(response));
		if (!response.body) throw new Error('The result stream is empty');

		const total = response.headers.get('X-Total-Rows');
		if (total !== null) this.rowCount = Number(total);

		const body = isFirst
			? countBytes(response.body, (bytes) => {
					this.bytes = bytes;
				})
			: response.body;
		if (isFirst) this.totalBytes = Number(response.headers.get('Content-Length') ?? 0);

		const reader = await RecordBatchReader.from(body);
		const batches: RecordBatch[] = [];
		const offsets = [0];
		for await (const batch of reader) {
			if (batch.numRows === 0) continue;
			if (this.columns.length === 0) {
				this.columns = batch.schema.fields.map(columnMeta);
			}
			offsets.push(offsets[offsets.length - 1] + batch.numRows);
			batches.push(batch);
		}
		this.pages.set(page, { batches, offsets });
		// Without a total header, the best we know is what we have seen so far.
		if (total === null) {
			this.rowCount = Math.max(this.rowCount, offset + offsets[offsets.length - 1]);
		}
		this.version++;
	}

	/** The cell at a global row and column index, or null when its page is not loaded. */
	cell(row: number, column: number): unknown {
		// Reading the version makes rendered cells re-evaluate when a page lands.
		void this.version;
		const page = Math.floor(row / RESULT_PAGE_SIZE);
		const local = row - page * RESULT_PAGE_SIZE;
		const data = this.pages.get(page);
		if (!data || data.batches.length === 0) return null;
		const { batches, offsets } = data;
		let low = 0;
		let high = batches.length - 1;
		while (low < high) {
			const mid = (low + high + 1) >> 1;
			if (offsets[mid] <= local) low = mid;
			else high = mid - 1;
		}
		const batch = batches[low];
		const vector = batch.getChildAt(column);
		if (!vector) return null;
		return vector.get(local - offsets[low]);
	}
}
