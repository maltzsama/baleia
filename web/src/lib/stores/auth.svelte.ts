// src/lib/stores/auth.svelte.ts
import { clearSession, client } from '$lib/api/client';
import { clearDrafts } from '$lib/workbench/drafts';

export type Features = {
	agents: boolean;
	assistant: boolean;
	retrievers: boolean;
};

export type AuthUser = {
	id: string;
	name: string;
	email: string;
	role: string;
	auth_provider: string;
	created_at: string;
	// Feature flags and actions outside any resource (#104). Optional because
	// login responses carry only the user; /auth/me fills them.
	personal_workspace_id?: string | null;
	features?: Features;
	global_actions?: string[];
};

function createAuthStore() {
	let user = $state<AuthUser | null>(null);
	let loading = $state(true);

	function initials(): string {
		if (!user) return '?';
		return user.name
			.split(' ')
			.map((p) => p[0])
			.slice(0, 2)
			.join('')
			.toUpperCase();
	}

	async function load() {
		loading = true;
		try {
			const { data } = await client.GET('/auth/me');
			user = (data as AuthUser | undefined) ?? null;
			if (!data) clearSession();
		} catch {
			user = null;
			clearSession();
		} finally {
			loading = false;
		}
	}

	function set(u: AuthUser | null) {
		user = u;
	}

	function clear() {
		user = null;
	}

	async function logout() {
		const uid = user?.id;
		try {
			// The browser sends the HttpOnly refresh cookie; the API revokes the
			// whole family and clears it.
			await client.POST('/auth/logout', { body: {} });
		} catch {
			/* best effort: log out locally even if the API is unreachable */
		}
		// Shared computers: explicit logout removes the workbench drafts (#112).
		if (uid) await clearDrafts(uid);
		clearSession();
		clear();
		window.location.href = '/login';
	}

	return {
		get user() {
			return user;
		},
		get loading() {
			return loading;
		},
		get initials() {
			return initials();
		},
		load,
		set,
		clear,
		logout
	};
}

export const authStore = createAuthStore();
