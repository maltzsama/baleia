import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import SessionExpired from '$lib/components/SessionExpired.svelte';

describe('session expired modal', () => {
	it('explains the expiry and keeps the drafts promise', () => {
		render(SessionExpired, { open: true, onsignin: () => {} });

		expect(screen.getByText('Your session has expired')).toBeTruthy();
		expect(screen.getByText(/Unsaved drafts are kept/)).toBeTruthy();
	});

	it('offers a sign-in action', async () => {
		const onsignin = vi.fn();
		render(SessionExpired, { open: true, onsignin });

		await fireEvent.click(screen.getByRole('button', { name: 'Sign in again' }));
		expect(onsignin).toHaveBeenCalledOnce();
	});
});
