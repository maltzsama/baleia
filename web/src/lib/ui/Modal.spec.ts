import { createRawSnippet } from 'svelte';
import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

import Modal from './Modal.svelte';

const body = createRawSnippet(() => ({ render: () => '<p>Dialog body</p>' }));

describe('Modal', () => {
	it('opens as a modal dialog and closes with the header button', async () => {
		const onclose = vi.fn();
		render(Modal, {
			props: { open: true, title: 'Confirm', onclose, children: body }
		});

		const dialog = screen.getByRole('dialog');
		expect(dialog).toBeTruthy();
		expect(dialog.getAttribute('open')).not.toBeNull();
		expect(screen.getByText('Dialog body')).toBeTruthy();

		await fireEvent.click(screen.getByRole('button', { name: 'Close' }));
		expect(onclose).toHaveBeenCalledOnce();
	});
});
