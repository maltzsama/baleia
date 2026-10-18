<script lang="ts">
	import { onMount } from 'svelte';
	import type { components } from '$lib/api/schema';
	import { apiErrorMessage, client } from '$lib/api/client';
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

	type User = components['schemas']['UserPublic'];

	const roleOptions = [
		{ value: 'member', label: 'Member' },
		{ value: 'admin', label: 'Admin' }
	];

	let users = $state<User[]>([]);
	let loading = $state(true);
	let error = $state('');
	let search = $state('');
	let page = $state(1);
	const perPage = 20;

	let inviteOpen = $state(false);
	let inviteEmail = $state('');
	let inviteRole = $state<'admin' | 'member'>('member');
	let inviteBusy = $state(false);
	let inviteError = $state('');

	let roleTarget = $state<User | null>(null);
	let roleValue = $state<'admin' | 'member'>('member');
	let roleOpen = $state(false);

	let suspendTarget = $state<User | null>(null);
	let suspendOpen = $state(false);

	const filtered = $derived(
		users.filter((u) => {
			const q = search.trim().toLowerCase();
			if (!q) return true;
			return (
				u.name.toLowerCase().includes(q) ||
				u.email.toLowerCase().includes(q) ||
				u.status.toLowerCase().includes(q)
			);
		})
	);
	const totalPages = $derived(Math.max(1, Math.ceil(filtered.length / perPage)));
	const visible = $derived(filtered.slice((page - 1) * perPage, page * perPage));

	async function load() {
		loading = true;
		error = '';
		try {
			const { data, error: err } = await client.GET('/users', {
				params: { query: { per_page: 200 } }
			});
			if (err || !data) {
				error = apiErrorMessage(err, 'Could not load users.');
				return;
			}
			users = data.data ?? [];
		} catch {
			error = 'Could not load users.';
		} finally {
			loading = false;
		}
	}

	onMount(load);

	async function sendInvite() {
		inviteBusy = true;
		inviteError = '';
		try {
			const { error: err } = await client.POST('/users/invite', {
				body: { email: inviteEmail, role: inviteRole }
			});
			if (err) {
				inviteError = apiErrorMessage(err, 'Could not send the invitation.');
				return;
			}
			inviteOpen = false;
			inviteEmail = '';
			toasts.success('Invitation sent.');
			await load();
		} finally {
			inviteBusy = false;
		}
	}

	function askRole(user: User, role: string) {
		roleTarget = user;
		roleValue = role as 'admin' | 'member';
		roleOpen = true;
	}

	async function confirmRole() {
		if (!roleTarget) return;
		const { error: err } = await client.PATCH('/users/{id}', {
			params: { path: { id: roleTarget.id } },
			body: { role: roleValue }
		});
		if (err) throw new Error(apiErrorMessage(err, 'Could not change the role.'));
		toasts.success('Role updated. The user signs in again to use it.');
		await load();
	}

	function askSuspend(user: User) {
		suspendTarget = user;
		suspendOpen = true;
	}

	async function confirmSuspend() {
		if (!suspendTarget) return;
		const { error: err } = await client.POST('/users/{id}/suspend', {
			params: { path: { id: suspendTarget.id } },
			body: { reason: 'admin' }
		});
		if (err) throw new Error(apiErrorMessage(err, 'Could not suspend the user.'));
		toasts.success('User suspended. Sessions and API keys were revoked.');
		await load();
	}

	async function reinstate(user: User) {
		const { error: err } = await client.POST('/users/{id}/reinstate', {
			params: { path: { id: user.id } }
		});
		if (err) {
			toasts.error(apiErrorMessage(err, 'Could not reinstate the user.'));
			return;
		}
		toasts.success('User reinstated.');
		await load();
	}
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="max-w-5xl">
		<div class="flex flex-wrap items-end justify-between gap-3">
			<div>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">Users</h1>
				<p class="mt-1 text-[13px] text-text-muted">
					Invite people, change roles, and suspend or reinstate accounts. Suspension is reversible;
					users are never deleted here.
				</p>
			</div>
			<Button variant="primary" onclick={() => (inviteOpen = true)}>Invite user</Button>
		</div>

		<div class="mt-4 flex flex-wrap items-end gap-2">
			<Input label="Search" bind:value={search} placeholder="Name, e-mail or status" />
			<Button variant="ghost" onclick={() => (search = '')}>Clear</Button>
		</div>

		{#if loading}
			<div class="mt-6 flex flex-col gap-2">
				<Skeleton height="2.5rem" />
				<Skeleton height="2.5rem" />
				<Skeleton height="2.5rem" />
			</div>
		{:else if error}
			<div class="mt-6">
				<ErrorState title="Could not load users" message={error} onretry={load} />
			</div>
		{:else if filtered.length === 0}
			<div class="mt-6">
				<EmptyState
					title={search ? 'No matching users' : 'No users yet'}
					description={search
						? 'Try another name, e-mail or status.'
						: 'Invite the first teammate to get started.'}
				/>
			</div>
		{:else}
			<div class="mt-4 overflow-x-auto rounded-lg border border-border bg-bg-panel">
				<table class="w-full text-left text-[13px]">
					<thead>
						<tr class="border-b border-border text-[11px] uppercase text-text-muted">
							<th class="px-3 py-2 font-semibold">User</th>
							<th class="px-3 py-2 font-semibold">Role</th>
							<th class="px-3 py-2 font-semibold">Status</th>
							<th class="px-3 py-2 font-semibold">Provider</th>
							<th class="px-3 py-2 text-right font-semibold">Actions</th>
						</tr>
					</thead>
					<tbody>
						{#each visible as user (user.id)}
							<tr class="border-b border-border last:border-0">
								<td class="px-3 py-2">
									<div class="font-medium">{user.name}</div>
									<div class="text-[11px] text-text-muted">{user.email}</div>
								</td>
								<td class="px-3 py-2">
									<Badge tone={user.role === 'admin' ? 'info' : 'neutral'}>{user.role}</Badge>
								</td>
								<td class="px-3 py-2">
									<Badge tone={user.status === 'suspended' ? 'warning' : 'success'}>
										{user.status}
									</Badge>
								</td>
								<td class="px-3 py-2 text-text-muted">{user.auth_provider}</td>
								<td class="px-3 py-2">
									<div class="flex justify-end gap-2">
										<Button
											size="sm"
											onclick={() => askRole(user, user.role === 'admin' ? 'member' : 'admin')}
										>
											{user.role === 'admin' ? 'Make member' : 'Make admin'}
										</Button>
										{#if user.status === 'suspended'}
											<Button size="sm" onclick={() => reinstate(user)}>Reinstate</Button>
										{:else}
											<Button size="sm" variant="danger" onclick={() => askSuspend(user)}
												>Suspend</Button
											>
										{/if}
									</div>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>

			<div class="mt-3 flex items-center justify-between text-[12px] text-text-muted">
				<span>{filtered.length} users</span>
				<div class="flex items-center gap-2">
					<Button size="sm" disabled={page <= 1} onclick={() => (page -= 1)}>Previous</Button>
					<span>Page {page} of {totalPages}</span>
					<Button size="sm" disabled={page >= totalPages} onclick={() => (page += 1)}>Next</Button>
				</div>
			</div>
		{/if}
	</div>
</div>

<Modal bind:open={inviteOpen} title="Invite a user">
	<div class="flex flex-col gap-3">
		{#if inviteError}
			<p class="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-[12px] text-danger">
				{inviteError}
			</p>
		{/if}
		<Input
			label="E-mail"
			type="email"
			bind:value={inviteEmail}
			placeholder="teammate@example.com"
		/>
		<Select label="Role" bind:value={inviteRole} options={roleOptions} />
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (inviteOpen = false)}>Cancel</Button>
			<Button variant="primary" loading={inviteBusy} onclick={sendInvite}>Send invitation</Button>
		</div>
	{/snippet}
</Modal>

<ConfirmDialog
	bind:open={roleOpen}
	title="Change user role"
	resourceName={roleTarget?.name ?? ''}
	consequences={[
		'The change takes effect on the next sign-in; existing sessions are revoked.',
		'Administrators can manage users, identity providers and system settings.'
	]}
	confirmLabel="Change role"
	danger={roleValue === 'admin'}
	onconfirm={confirmRole}
/>

<ConfirmDialog
	bind:open={suspendOpen}
	title="Suspend user"
	resourceName={suspendTarget?.name ?? ''}
	consequences={[
		'The user is signed out everywhere and cannot sign in again.',
		'All API keys are revoked immediately.',
		'The account is kept and can be reinstated at any time.'
	]}
	confirmLabel="Suspend user"
	onconfirm={confirmSuspend}
/>
