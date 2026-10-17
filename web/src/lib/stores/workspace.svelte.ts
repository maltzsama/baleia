import { client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type Workspace = components['schemas']['Workspace'];

const SELECTED_KEY = 'workspace_id';

function createWorkspaceStore() {
	let workspaces = $state<Workspace[]>([]);
	let current = $state<Workspace | null>(null);
	let loading = $state(true);

	async function load() {
		loading = true;
		try {
			const { data } = await client.GET('/workspaces');
			workspaces = (data as Workspace[] | undefined) ?? [];
			const saved = typeof localStorage !== 'undefined' ? localStorage.getItem(SELECTED_KEY) : null;
			const match = saved ? workspaces.find((w) => w.id === saved) : null;
			current = match || workspaces[0] || null;
		} catch {
			workspaces = [];
		} finally {
			loading = false;
		}
	}

	async function create(name: string): Promise<Workspace | null> {
		try {
			const { data } = await client.POST('/workspaces', {
				body: { name, type: 'personal' }
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
		if (typeof localStorage !== 'undefined') localStorage.setItem(SELECTED_KEY, ws.id);
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
