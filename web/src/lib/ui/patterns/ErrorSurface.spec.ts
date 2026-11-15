import { fireEvent, render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { goto } from '$app/navigation';
import { recordNavigation, resetNavigationHistory } from '$lib/navigation/history';

// #1062: the boundary reads the router status and never echoes the raw error.
const page = vi.hoisted(() => ({
	status: 404,
	error: null as { message: string } | null,
	url: new URL('http://localhost/missing'),
	params: {}
}));

vi.mock('$app/state', () => ({ page }));
vi.mock('$app/paths', () => ({ resolve: (path: string) => path }));
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));

import ErrorSurface from './ErrorSurface.svelte';

describe('error surface (#1062)', () => {
	beforeEach(() => {
		page.status = 404;
		page.error = { message: 'Not found' };
		vi.mocked(goto).mockClear();
		resetNavigationHistory();
	});

	it('renders the branded 404 surface with recovery actions', () => {
		render(ErrorSurface);

		expect(screen.getByRole('heading', { level: 1, name: 'Page not found' })).toBeTruthy();
		expect(screen.getByRole('link', { name: 'Go to Workbench' }).getAttribute('href')).toBe('/sql');
		expect(screen.getByRole('button', { name: 'Go back' })).toBeTruthy();
		// The raw error text is never the headline.
		expect(screen.queryByText('Not found')).toBeNull();
	});

	it('goes to the Workbench when the app has no previous entry', async () => {
		render(ErrorSurface);
		await fireEvent.click(screen.getByRole('button', { name: 'Go back' }));
		expect(vi.mocked(goto)).toHaveBeenCalledWith('/sql');
	});

	it('uses browser history when the app navigated at least once', async () => {
		recordNavigation();
		const back = vi.spyOn(window.history, 'back').mockImplementation(() => {});
		render(ErrorSurface);
		await fireEvent.click(screen.getByRole('button', { name: 'Go back' }));
		expect(back).toHaveBeenCalled();
		expect(vi.mocked(goto)).not.toHaveBeenCalled();
		back.mockRestore();
	});

	it('renders a safe catalog headline for a server error', () => {
		page.status = 500;
		page.error = { message: 'Boom: relation "x" does not exist' };

		render(ErrorSurface);

		expect(screen.queryByRole('heading', { name: 'Page not found' })).toBeNull();
		expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
		expect(screen.queryByText(/relation "x"/)).toBeNull();
	});
});
