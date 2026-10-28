import { render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { describe, expect, it, vi } from 'vitest';
import Forbidden from '$lib/components/Forbidden.svelte';
import SettingsNav from '$lib/components/SettingsNav.svelte';
import {
	canAccessSection,
	filterSections,
	sectionForPath,
	type SettingsSection
} from '$lib/settings/sections';
import { authStore } from '$lib/stores/auth.svelte';
import { makeMe } from '../../../test/factories';
import { http, server } from '../../../test/server';

const nav = vi.hoisted(() => ({ path: '/settings' }));
vi.mock('$app/stores', () => ({
	page: {
		subscribe(run: (value: { url: URL }) => void) {
			run({ url: new URL(`http://localhost${nav.path}`) });
			return () => {};
		}
	}
}));

import SettingsLayout from './+layout.svelte';

const audit: SettingsSection = {
	href: '/settings/audit',
	label: 'Audit trail',
	description: 'Who changed what.'
};
const system: SettingsSection = {
	href: '/settings/system',
	label: 'System',
	description: 'Health of every component.',
	action: 'view_system'
};

describe('settings gating', () => {
	it('hides admin sections from a member', () => {
		const member = makeMe({ role: 'member', global_actions: [] });
		expect(filterSections([system, audit], member)).toEqual([audit]);
		expect(canAccessSection(system, member)).toBe(false);
		expect(canAccessSection(audit, member)).toBe(true);
	});

	it('shows admin sections to an admin', () => {
		const admin = makeMe();
		expect(filterSections([system, audit], admin)).toEqual([system, audit]);
		expect(canAccessSection(system, admin)).toBe(true);
	});

	it('resolves the deepest matching section', () => {
		expect(sectionForPath('/settings/audit/event-1', [audit, system])?.href).toBe(
			'/settings/audit'
		);
		expect(sectionForPath('/settings', [audit, system])).toBeUndefined();
	});

	it('renders the member nav without admin links', () => {
		const member = makeMe({ role: 'member', global_actions: [] });
		render(SettingsNav, {
			current: '/settings/audit',
			sections: filterSections([system, audit], member)
		});
		expect(screen.getByText('Audit trail')).toBeTruthy();
		expect(screen.queryByText('System')).toBeNull();
	});

	it('renders the 403 page for a non-admin', () => {
		render(Forbidden);
		const alert = screen.getByRole('alert');
		expect(alert.textContent).toContain('403');
		expect(alert.textContent).toContain('Administrator access required');
	});
	// #459: a feature-gated section is closed on a direct URL too, and its
	// page is never mounted; with the flag on, the action still applies.
	describe('feature gates', () => {
		const agents: SettingsSection = {
			href: '/settings/agents',
			label: 'Agents',
			description: 'Agent identities.',
			action: 'manage_users',
			feature: 'agents'
		};
		const flags = (agentsOn: boolean) => ({
			agents: agentsOn,
			assistant: false,
			retrievers: true
		});

		it('needs the flag and the action', () => {
			expect(canAccessSection(agents, makeMe({ features: flags(false) }))).toBe(false);
			expect(canAccessSection(agents, makeMe({ features: flags(true) }))).toBe(true);
			const member = makeMe({ role: 'member', global_actions: [], features: flags(true) });
			expect(canAccessSection(agents, member)).toBe(false);
			expect(filterSections([agents, audit], makeMe({ features: flags(false) }))).toEqual([audit]);
		});

		async function signIn(me: ReturnType<typeof makeMe>) {
			server.use(http.get('/auth/me', ({ response }) => response(200).json(me)));
			await authStore.load();
		}

		const page = createRawSnippet(() => ({ render: () => '<p>agents page</p>' }));

		it.each([
			['/settings/agents', 'agents'],
			['/settings/assistant', 'assistant']
		])('does not mount %s with its flag off', async (path) => {
			nav.path = path;
			await signIn(makeMe({ features: { agents: false, assistant: false, retrievers: true } }));
			render(SettingsLayout, { props: { children: page } });

			expect(screen.getByText('This feature is turned off')).toBeTruthy();
			expect(screen.queryByText('agents page')).toBeNull();
		});

		it('mounts the section with its flag on', async () => {
			nav.path = '/settings/agents';
			await signIn(makeMe({ features: flags(true) }));
			render(SettingsLayout, { props: { children: page } });

			expect(screen.getByText('agents page')).toBeTruthy();
		});
	});
});
