const apiBase = '/api/v1';

export type Workspace = {
  id: string;
  name: string;
  type: 'personal' | 'shared';
  description?: string;
  owner_user_id?: string;
  owner_team_id?: string;
  created_at: string;
  updated_at?: string;
};

function createWorkspaceStore() {
  let workspaces = $state<Workspace[]>([]);
  let current = $state<Workspace | null>(null);
  let loading = $state(true);

  const token = () => localStorage.getItem('token') || '';

  async function load() {
    loading = true;
    try {
      const res = await fetch(`${apiBase}/workspaces`, {
        headers: { Authorization: `Bearer ${token()}` },
      });
      if (res.ok) {
        workspaces = await res.json();
        const saved = localStorage.getItem('workspace_id');
        const match = saved ? workspaces.find((w) => w.id === saved) : null;
        current = match || workspaces[0] || null;
      }
    } catch {
      workspaces = [];
    }
    loading = false;
  }

  async function create(name: string): Promise<Workspace | null> {
    try {
      const res = await fetch(`${apiBase}/workspaces`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token()}` },
        body: JSON.stringify({ name, type: 'personal' }),
      });
      if (res.ok) {
        const ws: Workspace = await res.json();
        workspaces.push(ws);
        select(ws);
        return ws;
      }
    } catch { /* */ }
    return null;
  }

  function select(ws: Workspace) {
    current = ws;
    localStorage.setItem('workspace_id', ws.id);
  }

  return {
    get workspaces() { return workspaces; },
    get current() { return current; },
    get loading() { return loading; },
    load,
    create,
    select,
  };
}

export const workspaceStore = createWorkspaceStore();
