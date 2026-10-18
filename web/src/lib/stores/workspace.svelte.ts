import { client } from '$lib/api/client';
import type { components } from '$lib/api/schema';
import { authStore } from '$lib/stores/auth.svelte';

export type Workspace = components['schemas']['Workspace'];

const SELECTED_PREFIX = 'baleia.workspace.';

function selectedKey(): string {
	return `${SELECTED_PREFIX}${authStore.user?.id ?? 'anon'}`;
}

function createWorkspaceStore() {
	let workspaces = $state<Workspace[]>([]);
	let current = $state<Workspace | null>(null);
	let loading = $state(true);
	let loadInFlight: Promise<void> | null = null;

	async function load() {
		// Several components (layout, workbench, data explorer) call load() on
		// mount; share one in-flight request instead of firing duplicates (#233).
		if (loadInFlight) return loadInFlight;
		loadInFlight = doLoad();
		try {
			await loadInFlight;
		} finally {
			loadInFlight = null;
		}
	}

	async function doLoad() {
		loading = true;
		try {
			const { data } = await client.GET('/workspaces');
			workspaces = (data as Workspace[] | undefined) ?? [];
			const urlWs =
				typeof window !== 'undefined'
					? new URLSearchParams(window.location.search).get('ws')
					: null;
			const saved =
				typeof localStorage !== 'undefined' ? localStorage.getItem(selectedKey()) : null;
			const personal = workspaces.find((w) => w.id === authStore.user?.personal_workspace_id);
			current =
				workspaces.find((w) => w.id === urlWs) ??
				workspaces.find((w) => w.id === saved) ??
				personal ??
				workspaces[0] ??
				null;
		} catch {
			workspaces = [];
		} finally {
			loading = false;
		}
	}

	async function create(input: {
		name: string;
		description?: string;
		type?: 'personal' | 'shared';
		ownerTeamId?: string;
	}): Promise<Workspace | null> {
		try {
			const type = input.type ?? 'personal';
			const { data } = await client.POST('/workspaces', {
				body: {
					name: input.name,
					description: input.description,
					type,
					owner_team_id: type === 'shared' ? input.ownerTeamId : null
				}
			});
			if (!data) return null;
			const ws = data as Workspace;
			workspaces.push(ws);
			select(ws);
			return ws;
		} catch {
			return null;
		}
	}

	function select(ws: Workspace) {
		current = ws;
		if (typeof localStorage !== 'undefined') localStorage.setItem(selectedKey(), ws.id);
	}

	return {
		get workspaces() {
			return workspaces;
		},
		get current() {
			return current;
		},
		get loading() {
			return loading;
		},
		load,
		create,
		select
	};
}

export const workspaceStore = createWorkspaceStore();
