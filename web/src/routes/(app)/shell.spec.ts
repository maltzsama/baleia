import { fireEvent, render, screen, within } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { describe, expect, it, vi } from 'vitest';
import { http, server } from '../../test/server';
import { makeMe, makeWorkspace } from '../../test/factories';

vi.mock('$app/state', () => ({
	page: { url: new URL('http://localhost/data/docs'), params: {} }
}));

import AppLayout from './+layout.svelte';
import LeftSidebar from '$lib/components/LeftSidebar.svelte';

const content = createRawSnippet(() => ({ render: () => '<p>page body</p>' }));

// #466: one named navigation, one current destination, a skip link and
// exactly one main landmark around the page.
describe('app shell landmarks', () => {
	it('skips to the one main landmark of the page', async () => {
		server.use(
			http.get('/auth/me', ({ response }) => response(200).json(makeMe())),
			http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace()])),
			http.get('/attention', ({ response }) => response(200).json([]))
		);
		render(AppLayout, { props: { children: content } });

		const skip = screen.getByRole('link', { name: 'Skip to content' });
		expect(skip.getAttribute('href')).toBe('#main-content');
		const mains = screen.getAllByRole('main');
		expect(mains).toHaveLength(1);
		expect(mains[0].id).toBe('main-content');
		expect(within(mains[0]).getByText('page body')).toBeTruthy();

		const rail = screen.getByRole('navigation', { name: 'Main navigation' });
		const current = within(rail)
			.getAllByRole('link')
			.filter((link) => link.getAttribute('aria-current') === 'page');
		expect(current.map((link) => link.textContent?.trim())).toEqual(['Data']);
	});
});

describe('workbench sidebar views', () => {
	it('are tabs with the selected one announced and arrow keys', async () => {
		server.use(
			http.get('/workspaces/{id}/metadata/catalogs', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/spaces', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/queries', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/folders', ({ response }) => response(200).json([]))
		);
		render(LeftSidebar, { props: {} });

		const list = screen.getByRole('tablist', { name: 'Sidebar views' });
		const [datasources, queries] = within(list).getAllByRole('tab');
		expect(datasources.getAttribute('aria-selected')).toBe('true');
		expect(queries.getAttribute('aria-selected')).toBe('false');
		expect(screen.getByRole('tabpanel').getAttribute('aria-labelledby')).toBe(datasources.id);

		await fireEvent.keyDown(datasources, { key: 'ArrowRight' });
		expect(queries.getAttribute('aria-selected')).toBe('true');
		expect(document.activeElement).toBe(queries);
		await screen.findByRole('tabpanel', { name: 'Queries' });
	});
});
