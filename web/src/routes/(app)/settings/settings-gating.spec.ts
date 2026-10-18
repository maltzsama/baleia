import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import Forbidden from '$lib/components/Forbidden.svelte';
import SettingsNav from '$lib/components/SettingsNav.svelte';
import {
	canAccessSection,
	filterSections,
	sectionForPath,
	type SettingsSection
} from '$lib/settings/sections';
import { makeMe } from '../../../test/factories';

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
});
