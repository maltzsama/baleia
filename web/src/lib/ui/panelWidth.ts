// Side panel widths (#468): sidebars, trees and field panels can be widened
// on a large screen. A width is remembered per browser under its key; it is a
// viewer convenience, so blocked storage just falls back to the default.

export type PanelBounds = { min: number; max: number; initial: number };

export const PANEL_STEP = 24;

const PREFIX = 'baleia.panel.';

export function clampWidth(width: number, bounds: PanelBounds): number {
	if (!Number.isFinite(width)) return bounds.initial;
	return Math.min(bounds.max, Math.max(bounds.min, Math.round(width)));
}

/**
 * The width while dragging: a panel on the left grows as the pointer moves
 * right, one on the right as it moves left.
 */
export function widthFromDrag(
	start: number,
	dx: number,
	side: 'left' | 'right',
	bounds: PanelBounds
): number {
	return clampWidth(start + (side === 'left' ? dx : -dx), bounds);
}

/** The width after a key on the separator, or null for any other key. */
export function widthFromKey(
	width: number,
	key: string,
	side: 'left' | 'right',
	bounds: PanelBounds
): number | null {
	const grow = side === 'left' ? 'ArrowRight' : 'ArrowLeft';
	const shrink = side === 'left' ? 'ArrowLeft' : 'ArrowRight';
	switch (key) {
		case grow:
			return clampWidth(width + PANEL_STEP, bounds);
		case shrink:
			return clampWidth(width - PANEL_STEP, bounds);
		case 'Home':
			return bounds.min;
		case 'End':
			return bounds.max;
		default:
			return null;
	}
}

export function loadWidth(key: string, bounds: PanelBounds): number {
	try {
		const stored = localStorage.getItem(PREFIX + key);
		return stored === null ? bounds.initial : clampWidth(Number(stored), bounds);
	} catch {
		return bounds.initial;
	}
}

export function saveWidth(key: string, width: number, bounds: PanelBounds): void {
	try {
		localStorage.setItem(PREFIX + key, String(clampWidth(width, bounds)));
	} catch {
		// Storage blocked (private window): the width just is not remembered.
	}
}
