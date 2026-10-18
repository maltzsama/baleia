<script lang="ts">
	import { onMount } from 'svelte';
	import type { components } from '$lib/api/schema';
	import { apiErrorMessage, client } from '$lib/api/client';
	import { canGlobal } from '$lib/authz/can';
	import { authStore } from '$lib/stores/auth.svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import { toasts } from '$lib/ui/toast.svelte';

	type Team = components['schemas']['Team'];
	type TeamMember = components['schemas']['TeamMember'];
	type User = components['schemas']['UserPublic'];

	const teamRoleOptions = [
		{ value: 'member', label: 'Member' },
		{ value: 'owner', label: 'Owner' }
	];

	let teams = $state<Team[]>([]);
	let members = $state<Record<string, TeamMember[]>>({});
	let loading = $state(true);
	let error = $state('');
	let users = $state<User[]>([]);

	let createOpen = $state(false);
	let createName = $state('');
	let createDescription = $state('');
	let createBusy = $state(false);
	let createError = $state('');

	let addTeam = $state<Team | null>(null);
	let addOpen = $state(false);
	let addUserId = $state('');
	let addRole = $state<'member' | 'owner'>('member');
	let addBusy = $state(false);
	let addError = $state('');

	let removeTarget = $state<{ team: Team; member: TeamMember } | null>(null);
	let removeOpen = $state(false);

	let roleTarget = $state<{ team: Team; member: TeamMember } | null>(null);
	let roleValue = $state<'member' | 'owner'>('owner');
	let roleOpen = $state(false);

	let deleteTarget = $state<Team | null>(null);
	let deleteOpen = $state(false);

	const isAdmin = $derived(canGlobal(authStore.user, 'manage_users'));

	async function load() {
		loading = true;
		error = '';
		try {
			const { data, error: err } = await client.GET('/teams');
			if (err || !data) {
				error = apiErrorMessage(err, 'Could not load teams.');
				return;
			}
			teams = data;
			const next: Record<string, TeamMember[]> = {};
			for (const team of teams) {
				const { data: list, error: listErr } = await client.GET('/teams/{id}/members', {
					params: { path: { id: team.id } }
				});
				if (listErr || !list) {
					error = apiErrorMessage(listErr, 'Could not load team members.');
					return;
				}
				next[team.id] = list;
			}
			members = next;
		} catch {
			error = 'Could not load teams.';
		} finally {
			loading = false;
		}
	}

	async function loadUsers() {
		if (!isAdmin || users.length > 0) return;
		const { data } = await client.GET('/users', { params: { query: { per_page: 200 } } });
		users = data?.data ?? [];
	}

	onMount(load);

	async function createTeam() {
		createBusy = true;
		createError = '';
		try {
			const { error: err } = await client.POST('/teams', {
				body: { name: createName, description: createDescription }
			});
			if (err) {
				createError = apiErrorMessage(err, 'Could not create the team.');
				return;
			}
			createOpen = false;
			createName = '';
			createDescription = '';
			toasts.success('Team created.');
			await load();
		} finally {
			createBusy = false;
		}
	}

	async function openAdd(team: Team) {
		addTeam = team;
		addUserId = '';
		addRole = 'member';
		addError = '';
		addOpen = true;
		await loadUsers();
	}

	async function addMember() {
		if (!addTeam) return;
		addBusy = true;
		addError = '';
		try {
			const { error: err } = await client.POST('/teams/{id}/members', {
				params: { path: { id: addTeam.id } },
				body: { user_id: addUserId, role: addRole }
			});
			if (err) {
				addError = apiErrorMessage(err, 'Could not add the member.');
				return;
			}
			addOpen = false;
			toasts.success('Member added.');
			await load();
		} finally {
			addBusy = false;
		}
	}

	function askRemove(team: Team, member: TeamMember) {
		removeTarget = { team, member };
		removeOpen = true;
	}

	async function confirmRemove() {
		if (!removeTarget) return;
		const { error: err } = await client.DELETE('/teams/{id}/members/{user_id}', {
			params: { path: { id: removeTarget.team.id, user_id: removeTarget.member.user.id } }
		});
		if (err) throw new Error(apiErrorMessage(err, 'Could not remove the member.'));
		toasts.success('Member removed.');
		await load();
	}

	function askRole(team: Team, member: TeamMember) {
		roleTarget = { team, member };
		roleValue = member.role === 'owner' ? 'member' : 'owner';
		roleOpen = true;
	}

	async function confirmRole() {
		if (!roleTarget) return;
		const { error: err } = await client.PATCH('/teams/{id}/members/{user_id}', {
			params: { path: { id: roleTarget.team.id, user_id: roleTarget.member.user.id } },
			body: { role: roleValue }
		});
		if (err) throw new Error(apiErrorMessage(err, 'Could not change the role.'));
		toasts.success('Team role updated.');
		await load();
	}

	function askDelete(team: Team) {
		deleteTarget = team;
		deleteOpen = true;
	}

	async function confirmDelete() {
		if (!deleteTarget) return;
		const { error: err } = await client.DELETE('/teams/{id}', {
			params: { path: { id: deleteTarget.id } }
		});
		if (err) throw new Error(apiErrorMessage(err, 'Could not delete the team.'));
		toasts.success('Team deleted.');
		await load();
	}
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="max-w-4xl">
		<div class="flex flex-wrap items-end justify-between gap-3">
			<div>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">Teams</h1>
				<p class="mt-1 text-[13px] text-text-muted">
					{isAdmin
						? 'Every team, its members and their roles.'
						: 'The teams you belong to and your role in each.'}
				</p>
			</div>
			{#if isAdmin}
				<Button variant="primary" onclick={() => (createOpen = true)}>New team</Button>
			{/if}
		</div>

		{#if loading}
			<div class="mt-6 flex flex-col gap-3">
				<Skeleton height="5rem" />
				<Skeleton height="5rem" />
			</div>
		{:else if error}
			<div class="mt-6">
				<ErrorState title="Could not load teams" message={error} onretry={load} />
			</div>
		{:else if teams.length === 0}
			<div class="mt-6">
				<EmptyState
					title="No teams"
					description={isAdmin
						? 'Create a team to share workspaces and content.'
						: 'You do not belong to a team yet.'}
				/>
			</div>
		{:else}
			<div class="mt-6 flex flex-col gap-4">
				{#each teams as team (team.id)}
					<article class="rounded-lg border border-border bg-bg-panel p-4">
						<div class="flex flex-wrap items-start justify-between gap-3">
							<div>
								<h2 class="text-[14px] font-semibold">{team.name}</h2>
								{#if team.description}
									<p class="text-[12px] text-text-muted">{team.description}</p>
								{/if}
								<p class="mt-1 text-[11px] text-text-muted">
									{(members[team.id] ?? []).length}
									{(members[team.id] ?? []).length === 1 ? 'member' : 'members'}
								</p>
							</div>
							{#if isAdmin}
								<div class="flex gap-2">
									<Button size="sm" onclick={() => openAdd(team)}>Add member</Button>
									<Button size="sm" variant="danger" onclick={() => askDelete(team)}>Delete</Button>
								</div>
							{/if}
						</div>

						<ul class="mt-3 flex flex-col gap-2 border-t border-border pt-3">
							{#each members[team.id] ?? [] as member (member.user.id)}
								<li class="flex flex-wrap items-center justify-between gap-2 text-[13px]">
									<div>
										<span class="font-medium">{member.user.name}</span>
										<span class="ml-2 text-[11px] text-text-muted">{member.user.email}</span>
									</div>
									<div class="flex items-center gap-2">
										<Badge tone={member.role === 'owner' ? 'info' : 'neutral'}>
											{member.role}
										</Badge>
										{#if isAdmin}
											<Button size="sm" onclick={() => askRole(team, member)}>
												{member.role === 'owner' ? 'Make member' : 'Make owner'}
											</Button>
											<Button size="sm" variant="ghost" onclick={() => askRemove(team, member)}
												>Remove</Button
											>
										{/if}
									</div>
								</li>
							{/each}
							{#if (members[team.id] ?? []).length === 0}
								<li class="text-[12px] text-text-muted">No members.</li>
							{/if}
						</ul>
					</article>
				{/each}
			</div>
		{/if}
	</div>
</div>

<Modal bind:open={createOpen} title="New team">
	<div class="flex flex-col gap-3">
		{#if createError}
			<p class="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-[12px] text-danger">
				{createError}
			</p>
		{/if}
		<Input label="Name" bind:value={createName} placeholder="Engineering" />
		<Input label="Description" bind:value={createDescription} placeholder="Optional" />
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (createOpen = false)}>Cancel</Button>
			<Button variant="primary" loading={createBusy} onclick={createTeam}>Create team</Button>
		</div>
	{/snippet}
</Modal>

<Modal bind:open={addOpen} title="Add team member">
	<div class="flex flex-col gap-3">
		{#if addError}
			<p class="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-[12px] text-danger">
				{addError}
			</p>
		{/if}
		<Select
			label="User"
			bind:value={addUserId}
			placeholder="Choose a user"
			options={users.map((u) => ({ value: u.id, label: `${u.name} (${u.email})` }))}
		/>
		<Select label="Role" bind:value={addRole} options={teamRoleOptions} />
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (addOpen = false)}>Cancel</Button>
			<Button variant="primary" loading={addBusy} onclick={addMember}>Add member</Button>
		</div>
	{/snippet}
</Modal>

<ConfirmDialog
	bind:open={removeOpen}
	title="Remove team member"
	resourceName={removeTarget?.member.user.name ?? ''}
	consequences={[
		'They immediately lose access to workspaces owned by this team.',
		'Their account and other team memberships are unaffected.'
	]}
	confirmLabel="Remove member"
	onconfirm={confirmRemove}
/>

<ConfirmDialog
	bind:open={roleOpen}
	title="Change team role"
	resourceName={roleTarget?.member.user.name ?? ''}
	consequences={[
		'Owners can manage the team and its workspaces; members cannot.',
		'The change applies immediately.'
	]}
	confirmLabel="Change role"
	onconfirm={confirmRole}
/>

<ConfirmDialog
	bind:open={deleteOpen}
	title="Delete team"
	resourceName={deleteTarget?.name ?? ''}
	consequences={[
		'Members lose access to the workspaces this team owns.',
		'The team is removed; its workspaces and content are kept.'
	]}
	confirmLabel="Delete team"
	onconfirm={confirmDelete}
/>
