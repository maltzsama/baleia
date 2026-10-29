// Settings navigation metadata (#262). Each settings slice appends its own
// entry; access comes from /auth/me's global_actions, never from the role.
import { m } from '$lib/paraglide/messages';
import { canGlobal, type GlobalAction } from '$lib/authz/can';
import type { AuthUser, Features } from '$lib/stores/auth.svelte';

/** Settings is administration only; personal pages live under /profile. */
export type SettingsGroup = 'platform' | 'access' | 'activity';

export type SettingsSection = {
	href: string;
	label: string;
	group: SettingsGroup;
	/** Absent means every authenticated user can open the section. */
	action?: GlobalAction;
	/** Feature flag from /auth/me; absent means no feature gating. */
	feature?: keyof Features;
};

export const settingsGroups: { key: SettingsGroup; label: () => string }[] = [
	{ key: 'platform', label: m.settings_group_platform },
	{ key: 'access', label: m.settings_group_access },
	{ key: 'activity', label: m.settings_group_activity }
];

export const settingsSections: SettingsSection[] = [
	{
		href: '/settings/system',
		get label() {
			return m.settings_sections_system_status();
		},
		group: 'platform',
		action: 'view_system'
	},
	{
		href: '/settings/assistant',
		get label() {
			return m.settings_sections_assistant();
		},
		group: 'platform',
		feature: 'assistant'
	},
	{
		href: '/settings/identity',
		get label() {
			return m.settings_sections_identity_providers();
		},
		group: 'access',
		action: 'manage_identity'
	},
	{
		href: '/settings/users',
		get label() {
			return m.settings_sections_users();
		},
		group: 'access',
		action: 'manage_users'
	},
	{
		href: '/settings/teams',
		get label() {
			return m.settings_sections_teams();
		},
		group: 'access'
	},
	{
		href: '/settings/agents',
		get label() {
			return m.settings_sections_agents();
		},
		group: 'access',
		feature: 'agents'
	},
	{
		href: '/settings/audit',
		get label() {
			return m.settings_sections_audit_trail();
		},
		group: 'activity'
	}
];

export function filterSections(
	sections: SettingsSection[],
	user: AuthUser | null | undefined
): SettingsSection[] {
	return sections.filter((section) => canAccessSection(section, user));
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

/** False when the section's feature flag is off for this user. */
export function sectionEnabled(
	section: SettingsSection | undefined,
	user: AuthUser | null | undefined
): boolean {
	return !section?.feature || !!user?.features?.[section.feature];
}

/**
 * Whether a user may open a section: its feature is on and its action is
 * granted. The nav and the direct URL use this one predicate (#459); the
 * server stays the authority.
 */
export function canAccessSection(
	section: SettingsSection | undefined,
	user: AuthUser | null | undefined
): boolean {
	return sectionEnabled(section, user) && (!section?.action || canGlobal(user, section.action));
}
