<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { plural } from '$lib/i18n/plural';
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
		{
			value: 'member',
			get label() {
				return m.settings_users_member();
			}
		},
		{
			value: 'admin',
			get label() {
				return m.settings_users_admin();
			}
		}
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
			// The API paginates; walk every server page so search and the count
			// cover installations with more than one page of users.
			const all: User[] = [];
			let serverPage = 1;
			let serverPages = 1;
			do {
				const { data, error: err } = await client.GET('/users', {
					params: { query: { page: serverPage, per_page: 200 } }
				});
				if (err || !data) {
					error = apiErrorMessage(err, m.settings_users_could_not_load_users());
					return;
				}
				all.push(...(data.data ?? []));
				serverPages = data.pagination?.total_pages ?? 1;
				serverPage += 1;
			} while (serverPage <= serverPages);
			users = all;
		} catch {
			error = m.settings_users_could_not_load_users();
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
				inviteError = apiErrorMessage(err, m.settings_users_could_not_send_the_invitation());
				return;
			}
			inviteOpen = false;
			inviteEmail = '';
			toasts.success(m.settings_users_invitation_sent());
			await load();
		} catch (err) {
			inviteError =
				err instanceof Error ? err.message : m.settings_users_could_not_send_the_invitation();
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
		if (err) throw new Error(apiErrorMessage(err, m.settings_users_could_not_change_the_role()));
		toasts.success(m.settings_users_role_updated_the_user_signs_in());
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
		if (err) throw new Error(apiErrorMessage(err, m.settings_users_could_not_suspend_the_user()));
		toasts.success(m.settings_users_user_suspended_sessions_and_api_keys());
		await load();
	}

	async function reinstate(user: User) {
		try {
			const { error: err } = await client.POST('/users/{id}/reinstate', {
				params: { path: { id: user.id } }
			});
			if (err) {
				toasts.error(apiErrorMessage(err, m.settings_users_could_not_reinstate_the_user()));
				return;
			}
			toasts.success(m.settings_users_user_reinstated());
			await load();
		} catch (err) {
			toasts.error(
				err instanceof Error ? err.message : m.settings_users_could_not_reinstate_the_user()
			);
		}
	}
</script>

<div class="flex-1 overflow-y-auto p-7">
	<!-- Lists and tables use the full width (#468). -->
	<div class="w-full">
		<div class="flex flex-wrap items-end justify-between gap-3">
			<div>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.settings_users_users()}</h1>
				<p class="mt-1 text-[13px] text-text-muted">
					{m.settings_users_invite_people_change_roles_and_suspend_or()}
				</p>
			</div>
			<Button variant="primary" onclick={() => (inviteOpen = true)}
				>{m.settings_users_invite_user()}</Button
			>
		</div>

		<div class="mt-4 flex flex-wrap items-end gap-2">
			<Input
				label={m.common_search()}
				bind:value={search}
				placeholder={m.settings_users_name_e_mail_or_status()}
				oninput={() => (page = 1)}
			/>
			<Button variant="ghost" onclick={() => (search = '')}>{m.common_clear()}</Button>
		</div>

		{#if loading}
			<div class="mt-6 flex flex-col gap-2">
				<Skeleton height="2.5rem" />
				<Skeleton height="2.5rem" />
				<Skeleton height="2.5rem" />
			</div>
		{:else if error}
			<div class="mt-6">
				<ErrorState
					title={m.settings_users_could_not_load_users_2()}
					message={error}
					onretry={load}
				/>
			</div>
		{:else if filtered.length === 0}
			<div class="mt-6">
				<EmptyState
					title={search ? m.settings_users_no_matching_users() : m.settings_users_no_users_yet()}
					description={search
						? m.settings_users_try_another_name_e_mail_or_status()
						: m.settings_users_invite_the_first_teammate_to_get_started()}
				/>
			</div>
		{:else}
			<div class="mt-4 overflow-x-auto rounded-lg border border-border bg-bg-panel">
				<table class="w-full text-left text-[13px]">
					<thead>
						<tr class="border-b border-border text-[11px] uppercase text-text-muted">
							<th class="px-3 py-2 font-semibold">{m.settings_users_user()}</th>
							<th class="px-3 py-2 font-semibold">{m.settings_users_role()}</th>
							<th class="px-3 py-2 font-semibold">{m.settings_users_status()}</th>
							<th class="px-3 py-2 font-semibold">{m.settings_users_provider()}</th>
							<th class="px-3 py-2 text-right font-semibold">{m.settings_users_actions()}</th>
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
											{user.role === 'admin'
												? m.settings_teams_make_member()
												: m.settings_users_make_admin()}
										</Button>
										{#if user.status === 'suspended'}
											<Button size="sm" onclick={() => reinstate(user)}
												>{m.settings_users_reinstate()}</Button
											>
										{:else}
											<Button size="sm" variant="danger" onclick={() => askSuspend(user)}
												>{m.settings_users_suspend()}</Button
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
				<span
					>{plural(filtered.length, m.settings_users_count_one, m.settings_users_count_other)}</span
				>
				<div class="flex items-center gap-2">
					<Button size="sm" disabled={page <= 1} onclick={() => (page -= 1)}
						>{m.settings_users_previous()}</Button
					>
					<span>{m.settings_users_page_of({ page, totalPages })}</span>
					<Button size="sm" disabled={page >= totalPages} onclick={() => (page += 1)}
						>{m.settings_users_next()}</Button
					>
				</div>
			</div>
		{/if}
	</div>
</div>

<Modal bind:open={inviteOpen} title={m.settings_users_invite_a_user()}>
	<div class="flex flex-col gap-3">
		{#if inviteError}
			<p class="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-[12px] text-danger">
				{inviteError}
			</p>
		{/if}
		<Input
			label={m.settings_users_e_mail()}
			type="email"
			bind:value={inviteEmail}
			placeholder={m.settings_users_teammate_example_com()}
		/>
		<Select label={m.common_role()} bind:value={inviteRole} options={roleOptions} />
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (inviteOpen = false)}>{m.common_cancel()}</Button>
			<Button variant="primary" loading={inviteBusy} onclick={sendInvite}
				>{m.settings_users_send_invitation()}</Button
			>
		</div>
	{/snippet}
</Modal>

<ConfirmDialog
	bind:open={roleOpen}
	title={m.settings_users_change_user_role()}
	resourceName={roleTarget?.name ?? ''}
	consequences={[
		m.settings_users_the_change_takes_effect_on_the(),
		m.settings_users_administrators_can_manage_users_identity_p()
	]}
	confirmLabel={m.settings_users_change_role()}
	danger={roleValue === 'admin'}
	onconfirm={confirmRole}
/>

<ConfirmDialog
	bind:open={suspendOpen}
	title={m.settings_users_suspend_user()}
	resourceName={suspendTarget?.name ?? ''}
	consequences={[
		m.settings_users_the_user_is_signed_out_everywhere(),
		m.settings_users_all_api_keys_are_revoked_immediately(),
		m.settings_users_the_account_is_kept_and_can_be_reinstated()
	]}
	confirmLabel={m.settings_users_suspend_user()}
	onconfirm={confirmSuspend}
/>
