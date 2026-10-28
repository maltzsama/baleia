import { fireEvent, render, screen, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { authStore } from '$lib/stores/auth.svelte';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { http, server } from '../../test/server';
import { makeMe, makeWorkspace } from '../../test/factories';

const nav = vi.hoisted(() => ({ url: new URL('http://localhost/sql') }));
vi.mock('$app/state', () => ({
	page: {
		get url() {
			return nav.url;
		},
		params: {}
	}
}));

import Header from './Header.svelte';

function renderAt(path: string, retrievers = true) {
	nav.url = new URL(`http://localhost${path}`);
	authStore.set(makeMe({ features: { agents: false, assistant: false, retrievers } }));
	workspaceStore.select(makeWorkspace());
	return render(Header, { props: { onToggleDark: vi.fn(), dark: false } });
}

describe('Header', () => {
	beforeEach(() => {
		server.use(
			http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace()])),
			http.get('/attention', ({ response }) => response(200).json([]))
		);
	});

	// #465: the header names the section of the page, not always "SQL Editor".
	it.each([
		['/connections', 'Connections'],
		['/monitoring', 'Monitoring'],
		['/settings/users', 'Settings · Users']
	])('titles %s as %s', (path, title) => {
		renderAt(path);

		const heading = screen.getByTitle(title);
		expect(heading.textContent).toBe(title);
		// A long title truncates instead of pushing the controls out.
		expect(heading.className).toContain('truncate');
		expect(screen.queryByText('SQL Editor')).toBeNull();
	});

	// #464: an area's pages are tabs next to its name, one of them current.
	it('shows the area pages as tabs with the current one marked', () => {
		renderAt('/history');

		expect(screen.getByTitle('SQL').textContent).toBe('SQL');
		const tabs = within(screen.getByRole('navigation', { name: 'SQL' }));
		expect(tabs.getByRole('link', { name: 'Editor' }).getAttribute('href')).toBe('/sql');
		const history = tabs.getByRole('link', { name: 'History' });
		expect(history.getAttribute('aria-current')).toBe('page');
		expect(tabs.getByRole('link', { name: 'Editor' }).getAttribute('aria-current')).toBeNull();
	});

	it('hides a flagged page from the tabs', () => {
		renderAt('/content', false);
		const tabs = within(screen.getByRole('navigation', { name: 'Content' }));
		expect(tabs.queryByRole('link', { name: 'Retrievers' })).toBeNull();
		expect(tabs.getByRole('link', { name: 'Trash' })).toBeTruthy();
	});

	it('has one attention bell, opening below and closing on Escape', async () => {
		renderAt('/sql');
		const bells = screen.getAllByRole('button', { name: /^Attention/ });
		expect(bells).toHaveLength(1);

		await fireEvent.click(bells[0]);
		const panel = await screen.findByRole('dialog', { name: 'Attention' });
		expect(panel.className).toContain('top-full');
		expect(panel.className).toContain('right-0');
		await fireEvent.keyDown(panel, { key: 'Escape' });
		expect(screen.queryByRole('dialog', { name: 'Attention' })).toBeNull();
		expect(document.activeElement).toBe(bells[0]);
	});
});
