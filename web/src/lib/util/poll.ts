// Polling helper (#105 / FD10). Pauses while the tab is hidden, retries on
// errors and stops on a terminal value, a timeout or an explicit stop().
export type PollStatus = 'timeout' | 'stopped';

export type PollOptions<T> = {
	until: (value: T) => boolean;
	intervals?: number[];
	maxDurationMs?: number;
	onValue?: (value: T) => void;
};

export type PollController<T> = {
	stop: () => void;
	done: Promise<T | PollStatus>;
};

export function poll<T>(
	fn: (signal: AbortSignal) => Promise<T>,
	opts: PollOptions<T>
): PollController<T> {
	const intervals = opts.intervals ?? [1000, 2000, 5000, 10000];
	const maxDurationMs = opts.maxDurationMs ?? 10 * 60 * 1000;
	const startedAt = Date.now();

	let finished = false;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let controller: AbortController | undefined;
	let resolveDone!: (value: T | PollStatus) => void;

	const done = new Promise<T | PollStatus>((resolve) => {
		resolveDone = resolve;
	});

	function finish(value: T | PollStatus) {
		if (finished) return;
		finished = true;
		if (timer) clearTimeout(timer);
		controller?.abort();
		if (typeof document !== 'undefined') {
			document.removeEventListener('visibilitychange', onVisibility);
		}
		resolveDone(value);
	}

	function stop() {
		finish('stopped');
	}

	function schedule(attempt: number) {
		if (finished) return;
		const delay = intervals[Math.min(attempt, intervals.length - 1)];
		timer = setTimeout(() => void tick(attempt + 1), delay);
	}

	async function tick(attempt: number) {
		if (finished) return;
		if (Date.now() - startedAt >= maxDurationMs) {
			finish('timeout');
			return;
		}
		if (typeof document !== 'undefined' && document.hidden) {
			// Resumed by visibilitychange; do not consume an attempt while hidden.
			return;
		}
		controller = new AbortController();
		try {
			const value = await fn(controller.signal);
			opts.onValue?.(value);
			if (opts.until(value)) {
				finish(value);
				return;
			}
		} catch {
			// Network errors are retried until maxDurationMs.
		}
		schedule(attempt);
	}

	function onVisibility() {
		if (typeof document === 'undefined' || document.hidden || finished) return;
		if (timer) clearTimeout(timer);
		void tick(0);
	}

	if (typeof document !== 'undefined') {
		document.addEventListener('visibilitychange', onVisibility);
	}

	void tick(0);

	return { stop, done };
}
