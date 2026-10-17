import type { components } from '$lib/api/schema';

/** The 409 version_conflict details shown by the save dialogs (#112). */
export type Conflict = {
	currentVersion: number;
	updatedBy?: { id?: string; name?: string };
	updatedAt?: string;
	theirs: components['schemas']['SavedQuery'];
};
