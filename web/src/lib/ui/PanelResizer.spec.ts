import { fireEvent, render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import PanelResizer from './PanelResizer.svelte';
import { clampWidth, loadWidth, widthFromDrag, widthFromKey } from './panelWidth';

const bounds = { min: 200, max: 560, initial: 260 };

// #468: side panels can be widened by drag or keyboard, within bounds, and
// keep their width per browser.
describe('panel width', () => {
	it('clamps and moves with the side the panel is on', () => {
		expect(clampWidth(1000, bounds)).toBe(560);
		expect(clampWidth(Number.NaN, bounds)).toBe(260);
		expect(widthFromDrag(260, 40, 'left', bounds)).toBe(300);
		expect(widthFromDrag(260, 40, 'right', bounds)).toBe(220);
		expect(widthFromKey(260, 'ArrowRight', 'left', bounds)).toBe(284);
		expect(widthFromKey(260, 'ArrowRight', 'right', bounds)).toBe(236);
		expect(widthFromKey(260, 'End', 'left', bounds)).toBe(560);
		expect(widthFromKey(260, 'x', 'left', bounds)).toBeNull();
	});
});

describe('PanelResizer', () => {
	beforeEach(() => localStorage.clear());

	function setup(side: 'left' | 'right' = 'left') {
		render(PanelResizer, {
			props: { value: 260, bounds, side, storageKey: 'test', label: 'Resize the tree' }
		});
		return screen.getByRole('separator', { name: 'Resize the tree' });
	}

	it('resizes from the keyboard and remembers the width', async () => {
		const handle = setup();
		expect(handle.getAttribute('aria-orientation')).toBe('vertical');
		await fireEvent.keyDown(handle, { key: 'ArrowRight' });
		expect(handle.getAttribute('aria-valuenow')).toBe('284');
		expect(loadWidth('test', bounds)).toBe(284);
		await fireEvent.dblClick(handle);
		expect(handle.getAttribute('aria-valuenow')).toBe('260');
	});

	it('resizes by dragging', async () => {
		const handle = setup();
		await fireEvent.pointerDown(handle, { button: 0, clientX: 300, pointerId: 1 });
		await fireEvent.pointerMove(handle, { clientX: 400, pointerId: 1 });
		await fireEvent.pointerUp(handle, { clientX: 400, pointerId: 1 });
		expect(handle.getAttribute('aria-valuenow')).toBe('360');
		expect(loadWidth('test', bounds)).toBe(360);
	});
});
