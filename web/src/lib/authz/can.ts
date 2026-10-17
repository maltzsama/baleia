// Permission helpers (#104). Components read `allowed_actions` from a resource
// and `global_actions` from /auth/me; they never compare `role`.
import type { AuthUser } from '../stores/auth.svelte';

export type Resource = { allowed_actions?: string[] };

export type GlobalAction =
	| 'create_connection'
	| 'create_workspace'
	| 'create_shared_workspace'
	| 'manage_users'
	| 'manage_identity'
	| 'view_all_audit'
	| 'view_system'
	| 'manage_assistant';

/** can reports whether the resource carries the action. An absent list is false. */
export function can(resource: Resource | null | undefined, action: string): boolean {
	return !!resource?.allowed_actions?.includes(action);
}

/** canGlobal reports whether /auth/me carries the global action. */
export function canGlobal(user: AuthUser | null | undefined, action: GlobalAction): boolean {
	return !!user?.global_actions?.includes(action);
}
