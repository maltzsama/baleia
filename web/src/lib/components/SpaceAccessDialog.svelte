<script lang="ts">
	import Button from '$lib/ui/Button.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
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

	const roleOptions = [
		{ value: 'viewer', label: 'Viewer' },
		{ value: 'editor', label: 'Editor' },
		{ value: 'manager', label: 'Manager' }
	];
	const uuidRe = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

	$effect(() => {
		if (!open || !space) return;
		members = [];
		void load(space.id);
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
		if (!uuidRe.test(principalId.trim())) {
			error = 'Enter a user or team UUID';
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

<Modal bind:open title={`Access — ${space?.name ?? 'space'}`}>
	<div class="flex flex-col gap-4">
		{#if loading}
			<p class="text-xs text-text-muted">Loading...</p>
		{:else if error}
			<p class="text-[11px] text-danger" role="alert">{error}</p>
		{:else if members.length === 0}
			<p class="text-xs text-text-muted">
				Only workspace owners and global admins can see this space. Add a user or team below.
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
							label="Role"
							value={member.role}
							options={roleOptions}
							class="w-28"
							onchange={(e) =>
								void changeRole(member, (e.target as HTMLSelectElement).value as SpaceRole)}
						/>
						<Button variant="ghost" size="sm" onclick={() => void removeMember(member)}>
							Remove
						</Button>
					</li>
				{/each}
			</ul>
		{/if}

		<div class="flex flex-col gap-2 border-t border-border pt-3">
			<Select
				label="Kind"
				bind:value={principalType}
				options={[
					{ value: 'user', label: 'User' },
					{ value: 'team', label: 'Team' }
				]}
			/>
			<Input label="Principal id" bind:value={principalId} placeholder="User or team UUID" />
			<Select label="Role" bind:value={newRole} options={roleOptions} />
			<div class="flex justify-end">
				<Button variant="primary" size="sm" onclick={add} loading={busy} disabled={busy}>
					Add
				</Button>
			</div>
		</div>
	</div>
	{#snippet footer()}
		<div class="flex justify-end">
			<Button onclick={() => (open = false)}>Close</Button>
		</div>
	{/snippet}
</Modal>
