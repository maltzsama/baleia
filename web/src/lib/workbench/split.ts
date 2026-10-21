// Editor/results split of the workbench (#395): the results area's share of
// the workbench height, in percent. Remembered per browser; it is a viewer
// convenience, so a missing or blocked storage just falls back to the default.

export const SPLIT_DEFAULT = 46;
export const SPLIT_MIN = 15;
export const SPLIT_MAX = 85;
export const SPLIT_STEP = 5;

const STORAGE_KEY = 'baleia.workbench.results-height';

export function clampSplit(percent: number): number {
	if (!Number.isFinite(percent)) return SPLIT_DEFAULT;
	return Math.min(SPLIT_MAX, Math.max(SPLIT_MIN, Math.round(percent)));
}

/** The results share for a pointer at `clientY` over a container box. */
export function splitFromPointer(clientY: number, box: { top: number; height: number }): number {
	if (box.height <= 0) return SPLIT_DEFAULT;
	return clampSplit(((box.top + box.height - clientY) / box.height) * 100);
}

/** The share after a key on the separator, or null for any other key. */
export function splitFromKey(percent: number, key: string): number | null {
	switch (key) {
		case 'ArrowUp':
			return clampSplit(percent + SPLIT_STEP);
		case 'ArrowDown':
			return clampSplit(percent - SPLIT_STEP);
		case 'Home':
			return SPLIT_MAX;
		case 'End':
			return SPLIT_MIN;
		default:
			return null;
	}
}

export function loadSplit(): number {
	try {
		const stored = localStorage.getItem(STORAGE_KEY);
		return stored === null ? SPLIT_DEFAULT : clampSplit(Number(stored));
	} catch {
		return SPLIT_DEFAULT;
	}
}

export function saveSplit(percent: number): void {
	try {
		localStorage.setItem(STORAGE_KEY, String(clampSplit(percent)));
	} catch {
		// Storage blocked (private window): the split just is not remembered.
	}
}
