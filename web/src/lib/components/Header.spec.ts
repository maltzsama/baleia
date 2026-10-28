import { render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
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

// #465: the header names the section of the page, not always "SQL Editor".
describe('Header', () => {
	it.each([
		['/connections', 'Connections'],
		['/history', 'SQL · History'],
		['/settings/users', 'Settings · Users']
	])('titles %s as %s', (path, title) => {
		nav.url = new URL(`http://localhost${path}`);
		authStore.set(makeMe());
		workspaceStore.select(makeWorkspace());
		server.use(http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace()])));
		render(Header, { props: { onToggleDark: vi.fn(), dark: false } });

		const heading = screen.getByTitle(title);
		expect(heading.textContent).toBe(title);
		// A long title truncates instead of pushing the controls out.
		expect(heading.className).toContain('truncate');
		expect(screen.queryByText('SQL Editor')).toBeNull();
	});
});
