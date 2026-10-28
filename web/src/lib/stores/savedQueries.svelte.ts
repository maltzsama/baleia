// Saved content and spaces (#40). One typed store for the workbench sidebar:
// spaces, folders and saved queries are loaded together and every mutation
// goes through it. Components never use window events for app state.
import { m } from '$lib/paraglide/messages';
import { apiErrorMessage, client } from '$lib/api/client';
import type { components } from '$lib/api/schema';

export type SavedQuery = components['schemas']['SavedQuery'];
export type Space = components['schemas']['Space'];
export type SpaceMember = components['schemas']['SpaceMember'];
export type QueryFolder = components['schemas']['QueryFolder'];
export type SpaceRole = 'viewer' | 'editor' | 'manager';

export type Result = { error?: string };
export type PrincipalType = 'user' | 'team';

function createSavedQueriesStore() {
	let list = $state<SavedQuery[]>([]);
	let spaces = $state<Space[]>([]);
	let folders = $state<QueryFolder[]>([]);
	let loading = $state(false);
	let error = $state('');
	let workspaceId = $state('');

	async function reload(id = workspaceId) {
		workspaceId = id;
		if (!id) {
			list = [];
			spaces = [];
			folders = [];
			loading = false;
			return;
		}
		loading = true;
		error = '';
		const [spaceRes, queryRes, folderRes] = await Promise.all([
			client.GET('/workspaces/{id}/spaces', { params: { path: { id } } }),
			client.GET('/workspaces/{id}/queries', { params: { path: { id } } }),
			client.GET('/workspaces/{id}/folders', { params: { path: { id } } })
		]);
		const firstError = spaceRes.error ?? queryRes.error ?? folderRes.error;
		if (firstError)
			error = apiErrorMessage(firstError, m.stores_savedqueriessvelte_failed_to_load_content());
		spaces = spaceRes.data ?? [];
		list = queryRes.data ?? [];
		folders = folderRes.data ?? [];
		loading = false;
	}

	/** The caller's effective role in a space, or null when it is invisible. */
	function role(spaceId: string): SpaceRole | null {
		return (spaces.find((s) => s.id === spaceId)?.my_role as SpaceRole | null | undefined) ?? null;
	}

	function canEdit(spaceId: string): boolean {
		const r = role(spaceId);
		return r === 'editor' || r === 'manager';
	}

	function canManage(spaceId: string): boolean {
		return role(spaceId) === 'manager';
	}

	function space(id: string): Space | null {
		return spaces.find((s) => s.id === id) ?? null;
	}

	function queriesIn(spaceId: string): SavedQuery[] {
		return list.filter((q) => q.space_id === spaceId);
	}

	function foldersIn(spaceId: string): QueryFolder[] {
		return folders.filter((f) => f.space_id === spaceId);
	}

	function onSaved(query: SavedQuery) {
		const index = list.findIndex((item) => item.id === query.id);
		if (index >= 0) list[index] = query;
		else list = [...list, query];
	}

	async function createSpace(
		body: components['schemas']['CreateSpaceRequest']
	): Promise<Result & { space?: Space }> {
		const { data, error: err } = await client.POST('/workspaces/{id}/spaces', {
			params: { path: { id: workspaceId } },
			body
		});
		if (err)
			return { error: apiErrorMessage(err, m.stores_savedqueriessvelte_failed_to_create_space()) };
		if (data) spaces = [...spaces, data];
		return { space: data };
	}

	async function updateSpace(
		id: string,
		body: components['schemas']['UpdateSpaceRequest']
	): Promise<Result> {
		const { data, error: err } = await client.PATCH('/workspaces/{id}/spaces/{space_id}', {
			params: { path: { id: workspaceId, space_id: id } },
			body
		});
		if (err)
			return { error: apiErrorMessage(err, m.stores_savedqueriessvelte_failed_to_update_space()) };
		if (data) spaces = spaces.map((s) => (s.id === id ? data : s));
		return {};
	}

	async function deleteSpace(id: string, force = false): Promise<Result> {
		const { error: err } = await client.DELETE('/workspaces/{id}/spaces/{space_id}', {
			params: { path: { id: workspaceId, space_id: id }, query: { force } }
		});
		if (err)
			return { error: apiErrorMessage(err, m.stores_savedqueriessvelte_failed_to_delete_space()) };
		spaces = spaces.filter((s) => s.id !== id);
		list = list.filter((q) => q.space_id !== id);
		folders = folders.filter((f) => f.space_id !== id);
		return {};
	}

	async function listMembers(
		spaceId: string
	): Promise<{ members?: SpaceMember[]; error?: string }> {
		const { data, error: err } = await client.GET('/workspaces/{id}/spaces/{space_id}/members', {
			params: { path: { id: workspaceId, space_id: spaceId } }
		});
		if (err)
			return {
				error: apiErrorMessage(err, m.stores_savedqueriessvelte_failed_to_load_the_access_list())
			};
		return { members: data ?? [] };
	}

	async function setMember(
		spaceId: string,
		principalType: PrincipalType,
		principalId: string,
		roleName: SpaceRole
	): Promise<Result> {
		const { error: err } = await client.PUT(
			'/workspaces/{id}/spaces/{space_id}/members/{principal_type}/{principal_id}',
			{
				params: {
					path: {
						id: workspaceId,
						space_id: spaceId,
						principal_type: principalType,
						principal_id: principalId
					}
				},
				body: { role: roleName }
			}
		);
		return err
			? { error: apiErrorMessage(err, m.stores_savedqueriessvelte_failed_to_set_the_member()) }
			: {};
	}

	async function removeMember(
		spaceId: string,
		principalType: PrincipalType,
		principalId: string
	): Promise<Result> {
		const { error: err } = await client.DELETE(
			'/workspaces/{id}/spaces/{space_id}/members/{principal_type}/{principal_id}',
			{
				params: {
					path: {
						id: workspaceId,
						space_id: spaceId,
						principal_type: principalType,
						principal_id: principalId
					}
				}
			}
		);
		return err
			? { error: apiErrorMessage(err, m.stores_savedqueriessvelte_failed_to_remove_the_member()) }
			: {};
	}

	async function renameQuery(query: SavedQuery, name: string): Promise<Result> {
		const { data, error: err } = await client.PATCH('/workspaces/{id}/queries/{query_id}', {
			params: { path: { id: workspaceId, query_id: query.id } },
			body: { name }
		});
		if (err)
			return {
				error: apiErrorMessage(err, m.stores_savedqueriessvelte_failed_to_rename_the_query())
			};
		if (data) onSaved(data);
		return {};
	}

	async function moveQuery(
		query: SavedQuery,
		spaceId: string,
		folderId: string | null = null
	): Promise<Result> {
		const body: components['schemas']['UpdateSavedQueryRequest'] = { space_id: spaceId };
		if (folderId) body.folder_id = folderId;
		else body.move_to_root = true;
		const { data, error: err } = await client.PATCH('/workspaces/{id}/queries/{query_id}', {
			params: { path: { id: workspaceId, query_id: query.id } },
			body
		});
		if (err)
			return {
				error: apiErrorMessage(err, m.stores_savedqueriessvelte_failed_to_move_the_query())
			};
		if (data) onSaved(data);
		return {};
	}

	async function deleteQuery(query: SavedQuery): Promise<Result> {
		const { error: err } = await client.DELETE('/workspaces/{id}/queries/{query_id}', {
			params: { path: { id: workspaceId, query_id: query.id } }
		});
		if (err)
			return {
				error: apiErrorMessage(err, m.stores_savedqueriessvelte_failed_to_delete_the_query())
			};
		list = list.filter((item) => item.id !== query.id);
		return {};
	}

	async function saveCopy(
		query: SavedQuery,
		name: string,
		spaceId: string
	): Promise<Result & { query?: SavedQuery }> {
		const { data, error: err } = await client.POST('/workspaces/{id}/queries', {
			params: { path: { id: workspaceId } },
			body: { name, sql: query.sql, space_id: spaceId }
		});
		if (err)
			return { error: apiErrorMessage(err, m.stores_savedqueriessvelte_failed_to_save_a_copy()) };
		if (data) onSaved(data);
		return { query: data };
	}

	return {
		get list() {
			return list;
		},
		get spaces() {
			return spaces;
		},
		get folders() {
			return folders;
		},
		get loading() {
			return loading;
		},
		get error() {
			return error;
		},
		get workspaceId() {
			return workspaceId;
		},
		reload,
		role,
		canEdit,
		canManage,
		space,
		queriesIn,
		foldersIn,
		onSaved,
		createSpace,
		updateSpace,
		deleteSpace,
		listMembers,
		setMember,
		removeMember,
		renameQuery,
		moveQuery,
		deleteQuery,
		saveCopy
	};
}

export const savedQueries = createSavedQueriesStore();
