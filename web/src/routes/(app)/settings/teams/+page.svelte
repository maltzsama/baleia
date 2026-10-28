<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { plural } from '$lib/i18n/plural';
	import { eachLimited } from '$lib/util/limit';
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
		{
			value: 'member',
			get label() {
				return m.settings_teams_member();
			}
		},
		{
			value: 'owner',
			get label() {
				return m.settings_teams_owner();
			}
		}
	];

	let teams = $state<Team[]>([]);
	let members = $state<Record<string, TeamMember[]>>({});
	// Each team's members load on their own (#477), a few teams at a time.
	const MEMBER_LOOKUPS = 4;
	let memberState = $state<
		Record<string, { status: 'loading' | 'ready' | 'error'; error: string }>
	>({});
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
				error = apiErrorMessage(err, m.settings_teams_could_not_load_teams());
				return;
			}
			teams = data;
		} catch {
			error = m.settings_teams_could_not_load_teams();
		} finally {
			loading = false;
		}
		if (error) return;
		// The teams are on screen; their members follow without blocking them.
		memberState = Object.fromEntries(
			teams.map((team) => [team.id, { status: 'loading', error: '' }])
		);
		void eachLimited(teams, MEMBER_LOOKUPS, loadMembers);
	}

	async function loadMembers(team: Team) {
		memberState = { ...memberState, [team.id]: { status: 'loading', error: '' } };
		try {
			const { data: list, error: listErr } = await client.GET('/teams/{id}/members', {
				params: { path: { id: team.id } }
			});
			if (listErr || !list) {
				throw new Error(apiErrorMessage(listErr, m.settings_teams_could_not_load_team_members()));
			}
			members = { ...members, [team.id]: list };
			memberState = { ...memberState, [team.id]: { status: 'ready', error: '' } };
		} catch (e) {
			const message =
				e instanceof Error ? e.message : m.settings_teams_could_not_load_team_members();
			memberState = { ...memberState, [team.id]: { status: 'error', error: message } };
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
				createError = apiErrorMessage(err, m.settings_teams_could_not_create_the_team());
				return;
			}
			createOpen = false;
			createName = '';
			createDescription = '';
			toasts.success(m.settings_teams_team_created());
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
				addError = apiErrorMessage(err, m.settings_teams_could_not_add_the_member());
				return;
			}
			addOpen = false;
			toasts.success(m.settings_teams_member_added());
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
		if (err) throw new Error(apiErrorMessage(err, m.settings_teams_could_not_remove_the_member()));
		toasts.success(m.settings_teams_member_removed());
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
		if (err) throw new Error(apiErrorMessage(err, m.settings_teams_could_not_change_the_role()));
		toasts.success(m.settings_teams_team_role_updated());
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
		if (err) throw new Error(apiErrorMessage(err, m.settings_teams_could_not_delete_the_team()));
		toasts.success(m.settings_teams_team_deleted());
		await load();
	}
</script>

<div class="flex-1 overflow-y-auto p-7">
	<!-- Lists and tables use the full width (#468). -->
	<div class="w-full">
		<div class="flex flex-wrap items-end justify-between gap-3">
			<div>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.settings_teams_teams()}</h1>
				<p class="mt-1 text-[13px] text-text-muted">
					{isAdmin
						? m.settings_teams_every_team_its_members_and_their_roles()
						: m.settings_teams_the_teams_you_belong_to_and_your_role_in_e()}
				</p>
			</div>
			{#if isAdmin}
				<Button variant="primary" onclick={() => (createOpen = true)}
					>{m.settings_teams_new_team()}</Button
				>
			{/if}
		</div>

		{#if loading}
			<div class="mt-6 flex flex-col gap-3">
				<Skeleton height="5rem" />
				<Skeleton height="5rem" />
			</div>
		{:else if error}
			<div class="mt-6">
				<ErrorState
					title={m.settings_teams_could_not_load_teams_2()}
					message={error}
					onretry={load}
				/>
			</div>
		{:else if teams.length === 0}
			<div class="mt-6">
				<EmptyState
					title={m.settings_teams_no_teams()}
					description={isAdmin
						? m.settings_teams_create_a_team_to_share_workspaces_and_cont()
						: m.settings_teams_you_do_not_belong_to_a_team_yet()}
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
								{#if memberState[team.id]?.status === 'ready'}
									<p class="mt-1 text-[11px] text-text-muted">
										{plural(
											(members[team.id] ?? []).length,
											m.settings_teams_member_count_one,
											m.settings_teams_member_count_other
										)}
									</p>
								{/if}
							</div>
							{#if isAdmin}
								<div class="flex gap-2">
									<Button size="sm" onclick={() => openAdd(team)}
										>{m.settings_teams_add_member()}</Button
									>
									<Button size="sm" variant="danger" onclick={() => askDelete(team)}
										>{m.settings_teams_delete()}</Button
									>
								</div>
							{/if}
						</div>

						{#if memberState[team.id]?.status === 'loading'}
							<div
								class="mt-3 border-t border-border pt-3"
								role="status"
								aria-label={m.settings_teams_loading_members()}
							>
								<Skeleton height="1rem" width="60%" />
							</div>
						{:else if memberState[team.id]?.status === 'error'}
							<div class="mt-3 border-t border-border pt-3">
								<ErrorState
									title={m.settings_teams_could_not_load_team_members()}
									message={memberState[team.id].error}
									onretry={() => loadMembers(team)}
								/>
							</div>
						{:else}
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
													{member.role === 'owner'
														? m.settings_teams_make_member()
														: m.settings_teams_make_owner()}
												</Button>
												<Button size="sm" variant="ghost" onclick={() => askRemove(team, member)}
													>{m.settings_teams_remove()}</Button
												>
											{/if}
										</div>
									</li>
								{/each}
								{#if (members[team.id] ?? []).length === 0}
									<li class="text-[12px] text-text-muted">{m.settings_teams_no_members()}</li>
								{/if}
							</ul>
						{/if}
					</article>
				{/each}
			</div>
		{/if}
	</div>
</div>

<Modal bind:open={createOpen} title={m.settings_teams_new_team()}>
	<div class="flex flex-col gap-3">
		{#if createError}
			<p class="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-[12px] text-danger">
				{createError}
			</p>
		{/if}
		<Input
			label={m.common_name()}
			bind:value={createName}
			placeholder={m.settings_teams_engineering()}
		/>
		<Input
			label={m.common_description()}
			bind:value={createDescription}
			placeholder={m.common_optional()}
		/>
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (createOpen = false)}>{m.common_cancel()}</Button>
			<Button variant="primary" loading={createBusy} onclick={createTeam}
				>{m.settings_teams_create_team()}</Button
			>
		</div>
	{/snippet}
</Modal>

<Modal bind:open={addOpen} title={m.settings_teams_add_team_member()}>
	<div class="flex flex-col gap-3">
		{#if addError}
			<p class="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-[12px] text-danger">
				{addError}
			</p>
		{/if}
		<Select
			label={m.common_user()}
			bind:value={addUserId}
			placeholder={m.settings_teams_choose_a_user()}
			options={users.map((u) => ({ value: u.id, label: `${u.name} (${u.email})` }))}
		/>
		<Select label={m.common_role()} bind:value={addRole} options={teamRoleOptions} />
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (addOpen = false)}>{m.common_cancel()}</Button>
			<Button variant="primary" loading={addBusy} onclick={addMember}
				>{m.settings_teams_add_member()}</Button
			>
		</div>
	{/snippet}
</Modal>

<ConfirmDialog
	bind:open={removeOpen}
	title={m.settings_teams_remove_team_member()}
	resourceName={removeTarget?.member.user.name ?? ''}
	consequences={[
		m.settings_teams_they_immediately_lose_access_to_workspaces(),
		m.settings_teams_their_account_and_other_team_memberships_a()
	]}
	confirmLabel={m.settings_teams_remove_member()}
	onconfirm={confirmRemove}
/>

<ConfirmDialog
	bind:open={roleOpen}
	title={m.settings_teams_change_team_role()}
	resourceName={roleTarget?.member.user.name ?? ''}
	consequences={[
		m.settings_teams_owners_can_manage_the_team_and(),
		m.settings_teams_the_change_applies_immediately()
	]}
	confirmLabel={m.settings_teams_change_role()}
	onconfirm={confirmRole}
/>

<ConfirmDialog
	bind:open={deleteOpen}
	title={m.settings_teams_delete_team()}
	resourceName={deleteTarget?.name ?? ''}
	consequences={[
		m.settings_teams_members_lose_access_to_the_workspaces(),
		m.settings_teams_the_team_is_removed_its_workspaces_and_con()
	]}
	confirmLabel={m.settings_teams_delete_team()}
	onconfirm={confirmDelete}
/>
