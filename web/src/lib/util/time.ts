// Human formatting shared by the product screens (#105).

export function formatDuration(ms: number): string {
	if (!Number.isFinite(ms) || ms < 0) return '—';
	if (ms < 1000) return `${Math.round(ms)} ms`;
	const seconds = ms / 1000;
	if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 1 : 0)} s`;
	const minutes = Math.floor(seconds / 60);
	const rest = Math.round(seconds % 60);
	if (minutes < 60) return `${minutes} min${rest ? ` ${rest} s` : ''}`;
	const hours = Math.floor(minutes / 60);
	const mins = minutes % 60;
	return `${hours} h${mins ? ` ${mins} min` : ''}`;
}

export function formatRelativeTime(at: string | Date | number, now = Date.now()): string {
	const then = at instanceof Date ? at.getTime() : new Date(at).getTime();
	if (Number.isNaN(then)) return '—';
	const diff = Math.round((now - then) / 1000);
	if (Math.abs(diff) < 45) return 'just now';
	const units: [Intl.RelativeTimeFormatUnit, number][] = [
		['year', 365 * 24 * 3600],
		['month', 30 * 24 * 3600],
		['day', 24 * 3600],
		['hour', 3600],
		['minute', 60],
		['second', 1]
	];
	const formatter = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });
	for (const [unit, seconds] of units) {
		if (Math.abs(diff) >= seconds) {
			return formatter.format(-Math.round(diff / seconds), unit);
		}
	}
	return 'just now';
}

export function formatAbsoluteTime(at: string | Date): string {
	const date = at instanceof Date ? at : new Date(at);
	if (Number.isNaN(date.getTime())) return '—';
	return `${date.toLocaleString()} (${date.toISOString()})`;
}
