import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import MenuButton from './MenuButton.svelte';

function mount(onConvert = vi.fn(), onOptimize = vi.fn()) {
	render(MenuButton, {
		props: {
			label: 'Transform',
			items: [
				{ id: 'convert', label: 'Convert dialect', onselect: onConvert },
				{ id: 'optimize', label: 'Optimize SQL', onselect: onOptimize }
			]
		}
	});
	return { onConvert, onOptimize };
}

describe('MenuButton', () => {
	it('opens a menu of items from the button and announces it', async () => {
		mount();
		const button = screen.getByRole('button', { name: 'Transform' });
		expect(button.getAttribute('aria-haspopup')).toBe('menu');
		expect(button.getAttribute('aria-expanded')).toBe('false');
		expect(screen.queryByRole('menu')).toBeNull();

		await fireEvent.click(button);
		expect(button.getAttribute('aria-expanded')).toBe('true');
		const items = screen.getAllByRole('menuitem').map((item) => item.textContent?.trim());
		expect(items).toEqual(['Convert dialect', 'Optimize SQL']);
	});

	it('runs the chosen item, closes, and returns focus to the button', async () => {
		const { onConvert, onOptimize } = mount();
		const button = screen.getByRole('button', { name: 'Transform' });
		await fireEvent.click(button);
		await fireEvent.click(screen.getByRole('menuitem', { name: 'Optimize SQL' }));
		expect(onOptimize).toHaveBeenCalledOnce();
		expect(onConvert).not.toHaveBeenCalled();
		expect(screen.queryByRole('menu')).toBeNull();
		expect(document.activeElement).toBe(button);
	});

	it('is driven from the keyboard: arrows move, Escape closes', async () => {
		mount();
		const button = screen.getByRole('button', { name: 'Transform' });
		button.focus();
		await fireEvent.keyDown(button, { key: 'ArrowDown' });
		const [first, second] = screen.getAllByRole('menuitem');
		await vi.waitFor(() => expect(document.activeElement).toBe(first));

		await fireEvent.keyDown(screen.getByRole('menu'), { key: 'ArrowDown' });
		expect(document.activeElement).toBe(second);
		await fireEvent.keyDown(screen.getByRole('menu'), { key: 'ArrowDown' });
		expect(document.activeElement).toBe(first); // wraps
		await fireEvent.keyDown(screen.getByRole('menu'), { key: 'End' });
		expect(document.activeElement).toBe(second);

		await fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' });
		expect(screen.queryByRole('menu')).toBeNull();
		expect(document.activeElement).toBe(button);
	});

	it('closes when the pointer goes down outside', async () => {
		mount();
		await fireEvent.click(screen.getByRole('button', { name: 'Transform' }));
		expect(screen.getByRole('menu')).toBeTruthy();
		await fireEvent.pointerDown(document.body);
		expect(screen.queryByRole('menu')).toBeNull();
	});
});
