import { render, screen, within } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import { authStore } from '$lib/stores/auth.svelte';
import { makeMe } from '../../test/factories';

const nav = vi.hoisted(() => ({ url: new URL('http://localhost/sql') }));
vi.mock('$app/state', () => ({
	page: {
		get url() {
			return nav.url;
		},
		params: {}
	}
}));

import GlobalNav from './GlobalNav.svelte';

function renderAt(path: string) {
	nav.url = new URL(`http://localhost${path}`);
	authStore.set(makeMe());
	render(GlobalNav);
	return within(screen.getByRole('navigation', { name: 'Main navigation' }));
}

// #464: one labelled entry per area, exactly one current, no bell.
describe('GlobalNav', () => {
	it('lists the areas by name', () => {
		const rail = renderAt('/sql');
		for (const name of ['SQL', 'Data', 'Explore', 'Content', 'Connections', 'Monitoring']) {
			expect(rail.getByRole('link', { name })).toBeTruthy();
		}
		expect(rail.getByRole('link', { name: 'Settings' })).toBeTruthy();
		expect(rail.queryByRole('button', { name: /Attention/ })).toBeNull();
	});

	// #1146: governance is for whoever can manage data policies (FD9).
	it('shows Governance only to someone who can manage data policies', () => {
		expect(renderAt('/sql').getByRole('link', { name: 'Governance' })).toBeTruthy();
		document.body.innerHTML = '';
		nav.url = new URL('http://localhost/sql');
		authStore.set(makeMe({ role: 'member', global_actions: ['create_workspace'] }));
		render(GlobalNav);
		const rail = within(screen.getByRole('navigation', { name: 'Main navigation' }));
		expect(rail.queryByRole('link', { name: 'Governance' })).toBeNull();
	});

	it.each([
		['/data/docs', 'Data'],
		['/history', 'SQL'],
		['/models/m1', 'Explore'],
		['/catalogs/c1', 'Connections'],
		['/metrics', 'Content'],
		['/settings/users', 'Settings'],
		['/governance/revisions', 'Governance']
	])('marks only the area of %s (%s) as current', (path, area) => {
		const rail = renderAt(path);
		const current = rail
			.getAllByRole('link')
			.filter((link) => link.getAttribute('aria-current') === 'page');
		expect(current.map((link) => link.textContent?.trim())).toEqual([area]);
	});
});
