// Settings navigation metadata (#262). Each settings slice appends its own
// entry; access comes from /auth/me's global_actions, never from the role.
import { m } from '$lib/paraglide/messages';
import { canGlobal, type GlobalAction } from '$lib/authz/can';
import type { AuthUser, Features } from '$lib/stores/auth.svelte';

export type SettingsSection = {
	href: string;
	label: string;
	description: string;
	/** Absent means every authenticated user can open the section. */
	action?: GlobalAction;
	/** Feature flag from /auth/me; absent means no feature gating. */
	feature?: keyof Features;
};

export const settingsSections: SettingsSection[] = [
	{
		href: '/settings/system',
		get label() {
			return m.settings_sections_system_status();
		},
		get description() {
			return m.settings_sections_health_of_every_platform_component();
		},
		action: 'view_system'
	},
	{
		href: '/settings/identity',
		get label() {
			return m.settings_sections_identity_providers();
		},
		get description() {
			return m.settings_sections_oidc_and_ldap_sign_in_group();
		},
		action: 'manage_identity'
	},
	{
		href: '/settings/users',
		get label() {
			return m.settings_sections_users();
		},
		get description() {
			return m.settings_sections_invite_people_change_roles_and_suspend();
		},
		action: 'manage_users'
	},
	{
		href: '/settings/teams',
		get label() {
			return m.settings_sections_teams();
		},
		get description() {
			return m.settings_sections_team_membership_and_roles_for_sharing();
		}
	},
	{
		href: '/settings/agents',
		get label() {
			return m.settings_sections_agents();
		},
		get description() {
			return m.settings_sections_read_only_mcp_agents_their_tokens();
		},
		feature: 'agents'
	},
	{
		href: '/settings/assistant',
		get label() {
			return m.settings_sections_assistant();
		},
		get description() {
			return m.settings_sections_provider_data_sharing_and_turn_limits();
		},
		feature: 'assistant'
	},
	{
		href: '/settings/audit',
		get label() {
			return m.settings_sections_audit_trail();
		},
		get description() {
			return m.settings_sections_who_changed_what_when_and_which();
		}
	},
	{
		href: '/settings/profile',
		get label() {
			return m.settings_sections_profile();
		},
		get description() {
			return m.settings_sections_your_name_e_mail_and_password();
		}
	},
	{
		href: '/settings/api-keys',
		get label() {
			return m.settings_sections_api_keys();
		},
		get description() {
			return m.settings_sections_keys_for_scripts_and_agents_shown();
		}
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
