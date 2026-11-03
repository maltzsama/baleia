<script lang="ts">
	// Members of a workspace and their roles (#653). Admins change roles;
	// a personal workspace is shared by e-mail, a shared one gets its members
	// from its team.
	import { m } from '$lib/paraglide/messages';
	import { apiErrorMessage, client } from '$lib/api/client';
	import type { components } from '$lib/api/schema';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';

	type Member = components['schemas']['WorkspaceMember'];
	type Role = components['schemas']['WorkspaceRole'];

	let {
		workspaceId,
		personal,
		canManage
	}: { workspaceId: string; personal: boolean; canManage: boolean } = $props();

	let members = $state.raw<Member[]>([]);
	let loading = $state(true);
	let loadError = $state('');
	let actionError = $state('');
	let email = $state('');
	let newRole = $state<Role>('viewer');
	let adding = $state(false);
	let removeOpen = $state(false);
	let removing = $state<Member | null>(null);

	const roleOptions = $derived([
		{ value: 'admin', label: m.workspace_role_admin() },
		{ value: 'editor', label: m.workspace_role_editor() },
		{ value: 'viewer', label: m.workspace_role_viewer() }
	]);

	function roleLabel(role: Role): string {
		return roleOptions.find((option) => option.value === role)?.label ?? role;
	}

	function sourceLabel(member: Member): string {
		if (member.source === 'owner') return m.workspace_members_source_owner();
		if (member.source === 'team') return m.workspace_members_source_team();
		return m.workspace_members_source_shared();
	}

	async function load() {
		loading = true;
		loadError = '';
		const { data, error } = await client.GET('/workspaces/{id}/members', {
			params: { path: { id: workspaceId } }
		});
		if (error) {
			loadError = apiErrorMessage(error, m.workspace_members_load_failed());
		} else {
			members = data ?? [];
		}
		loading = false;
	}

	async function setRole(member: Member, role: string) {
		actionError = '';
		const { error } = await client.PUT('/workspaces/{id}/members/{user_id}', {
			params: { path: { id: workspaceId, user_id: member.user.id } },
			body: { role: role as Role }
		});
		if (error) {
			actionError = apiErrorMessage(error, m.workspace_members_change_failed());
		}
		await load();
	}

	async function add(event: SubmitEvent) {
		event.preventDefault();
		if (!email.trim() || adding) return;
		adding = true;
		actionError = '';
		const { error } = await client.POST('/workspaces/{id}/members', {
			params: { path: { id: workspaceId } },
			body: { email: email.trim(), role: newRole }
		});
		if (error) {
			actionError = apiErrorMessage(error, m.workspace_members_change_failed());
		} else {
			email = '';
			await load();
		}
		adding = false;
	}

	async function remove() {
		if (!removing) return;
		const { error } = await client.DELETE('/workspaces/{id}/members/{user_id}', {
			params: { path: { id: workspaceId, user_id: removing.user.id } }
		});
		if (error) throw error;
		removing = null;
		await load();
	}

	$effect(() => {
		if (workspaceId) void load();
	});
</script>

<div class="flex flex-col gap-3 text-[12px]">
	<p class="text-slate-600 dark:text-slate-300">
		{personal ? m.workspace_members_personal_hint() : m.workspace_members_team_hint()}
	</p>

	{#if canManage && personal}
		<form class="flex flex-wrap items-end gap-2" onsubmit={add}>
			<Input
				label={m.workspace_members_share_email()}
				type="email"
				bind:value={email}
				required
				class="min-w-[220px] flex-1"
			/>
			<Select label={m.workspace_members_role()} bind:value={newRole} options={roleOptions} />
			<Button type="submit" variant="primary" disabled={adding || !email.trim()}>
				{m.workspace_members_share()}
			</Button>
		</form>
	{/if}

	{#if actionError}<p class="text-danger" role="alert">{actionError}</p>{/if}

	{#if loading}
		<Skeleton height="4rem" />
	{:else if loadError}
		<ErrorState title={m.workspace_members_load_failed()} message={loadError} onretry={load} />
	{:else if members.length === 0}
		<EmptyState title={m.settings_workspaces_detail_no_members()} />
	{:else}
		<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
			<table class="w-full text-left text-[11px]">
				<thead
					class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
				>
					<tr>
						<th class="px-3 py-2">{m.settings_workspaces_detail_member()}</th>
						<th class="px-3 py-2">{m.workspace_members_source()}</th>
						<th class="px-3 py-2">{m.workspace_members_role()}</th>
						<th class="px-3 py-2"><span class="sr-only">{m.common_action()}</span></th>
					</tr>
				</thead>
				<tbody>
					{#each members as member (member.user.id)}
						<tr class="border-b border-border/60 last:border-b-0">
							<td class="px-3 py-1.5">
								{member.user.name}
								<span class="text-slate-600 dark:text-slate-300">({member.user.email})</span>
							</td>
							<td class="px-3 py-1.5"><Badge>{sourceLabel(member)}</Badge></td>
							<td class="px-3 py-1.5">
								{#if canManage && member.source !== 'owner'}
									<Select
										label={m.workspace_members_role_of({ name: member.user.name })}
										value={member.role}
										options={roleOptions}
										class="max-w-[140px] [&_label]:sr-only"
										onchange={(event) =>
											setRole(member, (event.currentTarget as HTMLSelectElement).value)}
									/>
								{:else}
									{roleLabel(member.role)}
								{/if}
							</td>
							<td class="px-3 py-1.5 text-right">
								{#if canManage && member.source === 'shared'}
									<Button
										size="sm"
										variant="ghost"
										onclick={() => {
											removing = member;
											removeOpen = true;
										}}>{m.workspace_members_stop_sharing()}</Button
									>
								{:else if canManage && member.source === 'team' && member.role_set}
									<Button
										size="sm"
										variant="ghost"
										onclick={() => {
											removing = member;
											removeOpen = true;
										}}>{m.workspace_members_use_default_role()}</Button
									>
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>

<ConfirmDialog
	bind:open={removeOpen}
	title={removing?.source === 'shared'
		? m.workspace_members_stop_sharing()
		: m.workspace_members_use_default_role()}
	resourceName={removing?.user.email ?? ''}
	consequences={[
		removing?.source === 'shared'
			? m.workspace_members_stop_sharing_consequence()
			: m.workspace_members_default_role_consequence()
	]}
	onconfirm={remove}
/>
