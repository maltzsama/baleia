import { createRawSnippet } from 'svelte';
import { fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

import Drawer from './Drawer.svelte';
import Modal from './Modal.svelte';

const body = createRawSnippet(() => ({ render: () => '<p>Drawer body</p>' }));

// #483: the drawer is a native modal dialog: Tab stays inside and the page
// behind is inert (showModal), focus starts in the panel and returns to the
// control that opened it.
describe('Drawer', () => {
	let trigger: HTMLButtonElement;

	function openFrom(onclose = vi.fn()) {
		trigger = document.createElement('button');
		trigger.textContent = 'Open details';
		document.body.append(trigger);
		trigger.focus();
		render(Drawer, { props: { open: true, title: 'Details', onclose, children: body } });
		return onclose;
	}

	afterEach(() => trigger?.remove());

	it('opens as a modal dialog with focus in the panel', () => {
		const showModal = vi.spyOn(HTMLDialogElement.prototype, 'showModal');
		openFrom();

		const dialog = screen.getByRole('dialog', { name: 'Details' });
		expect(showModal).toHaveBeenCalled();
		expect(dialog.hasAttribute('open')).toBe(true);
		expect(dialog.contains(document.activeElement)).toBe(true);
		showModal.mockRestore();
	});

	it('returns focus to the trigger when closed', async () => {
		const onclose = openFrom();

		await fireEvent.click(screen.getByRole('button', { name: 'Close' }));
		expect(onclose).toHaveBeenCalledOnce();
		expect(document.activeElement).toBe(trigger);
	});

	it('closes on a backdrop click but not on a click inside', async () => {
		const onclose = openFrom();
		const dialog = screen.getByRole('dialog', { name: 'Details' });

		await fireEvent.click(screen.getByText('Drawer body'));
		expect(onclose).not.toHaveBeenCalled();
		await fireEvent.click(dialog);
		expect(onclose).toHaveBeenCalledOnce();
	});
});

// #468: content-heavy drawers (SQL, JSON, diffs) take a wide size.
describe('Drawer sizes', () => {
	it.each([
		[undefined, 'max-w-[420px]'],
		['lg', 'max-w-[min(960px,60vw)]']
	] as const)('renders size %s at %s', (size, width) => {
		render(Drawer, { props: { open: true, title: 'Sized', size, children: body } });
		expect(screen.getByRole('dialog', { name: 'Sized' }).className).toContain(width);
	});
});

describe('Modal sizes', () => {
	it.each([
		['md', 'max-w-[480px]'],
		['lg', 'max-w-[900px]'],
		['xl', 'max-w-[1100px]']
	] as const)('renders %s at %s', (size, width) => {
		render(Modal, { props: { open: true, title: 'Sized', size, children: body } });
		expect(screen.getByRole('dialog').className).toContain(width);
	});
});
