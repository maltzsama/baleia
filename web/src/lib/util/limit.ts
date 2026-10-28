// Bounded concurrency for per-item lookups (#477): a page renders its list
// first and fills each item in the background without flooding the API.

/** Runs `task` for every item, at most `limit` at a time. Never rejects. */
export async function eachLimited<T>(
	items: readonly T[],
	limit: number,
	task: (item: T) => Promise<void>
): Promise<void> {
	let next = 0;
	async function worker() {
		while (next < items.length) {
			const item = items[next++];
			try {
				await task(item);
			} catch {
				// The task records its own error on the item it was loading.
			}
		}
	}
	const workers = Array.from({ length: Math.max(1, Math.min(limit, items.length)) }, worker);
	await Promise.all(workers);
}
