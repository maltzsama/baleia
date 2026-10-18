// Settings navigation metadata (#262). Each settings slice appends its own
// entry; access comes from /auth/me's global_actions, never from the role.
import { canGlobal, type GlobalAction } from '$lib/authz/can';
import type { AuthUser } from '$lib/stores/auth.svelte';

export type SettingsSection = {
	href: string;
	label: string;
	description: string;
	/** Absent means every authenticated user can open the section. */
	action?: GlobalAction;
};

export const settingsSections: SettingsSection[] = [
	{
		href: '/settings/identity',
		label: 'Identity providers',
		description: 'OIDC and LDAP sign-in, group mapping and provider health.',
		action: 'manage_identity'
	},
	{
		href: '/settings/users',
		label: 'Users',
		description: 'Invite people, change roles and suspend or reinstate accounts.',
		action: 'manage_users'
	},
	{
		href: '/settings/teams',
		label: 'Teams',
		description: 'Team membership and roles for sharing workspaces.'
	},
	{
		href: '/settings/audit',
		label: 'Audit trail',
		description: 'Who changed what, when, and which requests were denied.'
	}
];

export function filterSections(
	sections: SettingsSection[],
	user: AuthUser | null | undefined
): SettingsSection[] {
	return sections.filter((section) => !section.action || canGlobal(user, section.action));
}

export function visibleSections(user: AuthUser | null | undefined): SettingsSection[] {
	return filterSections(settingsSections, user);
}

export function firstVisibleSection(
	user: AuthUser | null | undefined
): SettingsSection | undefined {
	return visibleSections(user)[0];
}

/** The deepest section whose path prefixes the current one. */
export function sectionForPath(
	pathname: string,
	sections: SettingsSection[] = settingsSections
): SettingsSection | undefined {
	return sections
		.filter((section) => pathname === section.href || pathname.startsWith(`${section.href}/`))
		.sort((a, b) => b.href.length - a.href.length)[0];
}

export function canAccessSection(
	section: SettingsSection | undefined,
	user: AuthUser | null | undefined
): boolean {
	return !section?.action || canGlobal(user, section.action);
}
