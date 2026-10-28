<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Button from '$lib/ui/Button.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
	import { listPrincipals, type WorkspacePrincipal } from '$lib/content/client';
	import {
		savedQueries,
		type PrincipalType,
		type Space,
		type SpaceMember,
		type SpaceRole
	} from '$lib/stores/savedQueries.svelte';

	let { open = $bindable(false), space }: { open?: boolean; space: Space | null } = $props();

	let members = $state<SpaceMember[]>([]);
	let loading = $state(false);
	let error = $state('');
	let principalType = $state('user');
	let principalId = $state('');
	let newRole = $state('viewer');
	let busy = $state(false);
	let principals = $state<WorkspacePrincipal[]>([]);
	let principalsError = $state('');

	const roleOptions = [
		{
			value: 'viewer',
			get label() {
				return m.components_spaceaccessdialog_viewer();
			}
		},
		{
			value: 'editor',
			get label() {
				return m.components_spaceaccessdialog_editor();
			}
		},
		{
			value: 'manager',
			get label() {
				return m.components_spaceaccessdialog_manager();
			}
		}
	];

	$effect(() => {
		if (!open || !space) return;
		members = [];
		principalId = '';
		void load(space.id);
	});

	$effect(() => {
		if (!open || !space) return;
		const workspaceId = savedQueries.workspaceId;
		if (!workspaceId) return;
		principalsError = '';
		void listPrincipals(workspaceId, '', principalType as 'user' | 'team')
			.then((data) => (principals = data))
			.catch((e) => (principalsError = e instanceof Error ? e.message : String(e)));
	});

	async function load(spaceId: string) {
		loading = true;
		error = '';
		const res = await savedQueries.listMembers(spaceId);
		if (res.error) error = res.error;
		members = res.members ?? [];
		loading = false;
	}

	async function changeRole(member: SpaceMember, role: SpaceRole) {
		if (!space) return;
		const res = await savedQueries.setMember(
			space.id,
			member.principal_type as PrincipalType,
			member.principal_id,
			role
		);
		if (res.error) error = res.error;
		else
			members = members.map((m) => (m.principal_id === member.principal_id ? { ...m, role } : m));
	}

	async function removeMember(member: SpaceMember) {
		if (!space) return;
		const res = await savedQueries.removeMember(
			space.id,
			member.principal_type as PrincipalType,
			member.principal_id
		);
		if (res.error) error = res.error;
		else members = members.filter((m) => m.principal_id !== member.principal_id);
	}

	async function add() {
		if (!space || busy) return;
		if (!principalId) {
			error = m.components_spaceaccessdialog_choose_a_person_or_team();
			return;
		}
		busy = true;
		error = '';
		const res = await savedQueries.setMember(
			space.id,
			principalType as PrincipalType,
			principalId.trim(),
			newRole as SpaceRole
		);
		busy = false;
		if (res.error) {
			error = res.error;
			return;
		}
		principalId = '';
		await load(space.id);
	}
</script>

<Modal
	bind:open
	title={m.components_spaceaccessdialog_access({
		value: space?.name ?? m.components_spaceaccessdialog_space()
	})}
>
	<div class="flex flex-col gap-4">
		{#if loading}
			<p class="text-xs text-text-muted">{m.common_loading()}</p>
		{:else if error}
			<p class="text-[11px] text-danger" role="alert">{error}</p>
		{:else if members.length === 0}
			<p class="text-xs text-text-muted">
				{m.components_spaceaccessdialog_only_workspace_owners_and_global_admins()}
			</p>
		{:else}
			<ul class="flex flex-col gap-2">
				{#each members as member (member.principal_type + ':' + member.principal_id)}
					<li class="flex items-center gap-2 rounded-md border border-border px-2 py-1.5">
						<span class="flex-1 truncate text-xs">
							<span class="font-semibold">{member.principal_type}</span>
							<span class="text-text-muted"> {member.principal_id}</span>
						</span>
						<Select
							label={m.components_spaceaccessdialog_role()}
							value={member.role}
							options={roleOptions}
							class="w-28"
							onchange={(e) =>
								void changeRole(member, (e.target as HTMLSelectElement).value as SpaceRole)}
						/>
						<Button variant="ghost" size="sm" onclick={() => void removeMember(member)}>
							{m.components_spaceaccessdialog_remove()}
						</Button>
					</li>
				{/each}
			</ul>
		{/if}

		<div class="flex flex-col gap-2 border-t border-border pt-3">
			<Select
				label={m.common_kind()}
				bind:value={principalType}
				options={[
					{ value: 'user', label: 'User' },
					{ value: 'team', label: 'Team' }
				]}
			/>
			<Select
				label={principalType === 'team'
					? m.components_spaceaccessdialog_team()
					: m.components_spaceaccessdialog_person()}
				bind:value={principalId}
				options={principals.map((principal) => ({
					value: principal.id,
					label: principal.email ? `${principal.name} (${principal.email})` : principal.name
				}))}
			/>
			{#if principalsError}
				<p class="text-[11px] text-danger" role="alert">{principalsError}</p>
			{/if}
			<Select label={m.common_role()} bind:value={newRole} options={roleOptions} />
			<div class="flex justify-end">
				<Button variant="primary" size="sm" onclick={add} loading={busy} disabled={busy}>
					{m.common_add()}
				</Button>
			</div>
		</div>
	</div>
	{#snippet footer()}
		<div class="flex justify-end">
			<Button onclick={() => (open = false)}>{m.common_close()}</Button>
		</div>
	{/snippet}
</Modal>
