<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Button from '$lib/ui/Button.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Select from '$lib/ui/Select.svelte';
	import type { DataPolicySubject } from './api';
	import type { Directory, DirectoryEntry } from './directory';

	type Kind = 'user' | 'team' | 'group' | 'service_account' | 'everyone';

	let {
		directory,
		allowEveryone = true,
		legend,
		addLabel,
		onadd
	}: {
		directory: Directory;
		allowEveryone?: boolean;
		/** Names the group of fields (FD11). */
		legend: string;
		addLabel: string;
		onadd: (subject: DataPolicySubject) => void;
	} = $props();

	let kind = $state<Kind>('user');
	let value = $state('');

	const kinds = $derived([
		{ value: 'user', label: m.governance_kind_user() },
		{ value: 'team', label: m.governance_kind_team() },
		{ value: 'group', label: m.governance_kind_group() },
		{ value: 'service_account', label: m.governance_kind_service_account() },
		...(allowEveryone ? [{ value: 'everyone', label: m.governance_kind_everyone() }] : [])
	]);

	// The directory list for the kind, when the caller could read one. An
	// unreadable list (a connection owner listing users) falls back to an id.
	const entries = $derived.by((): DirectoryEntry[] => {
		if (kind === 'user') return directory.users;
		if (kind === 'team') return directory.teams;
		if (kind === 'service_account') return directory.serviceAccounts;
		return [];
	});

	const canAdd = $derived(kind === 'everyone' || value.trim() !== '');

	function add() {
		if (!canAdd) return;
		const v = value.trim();
		const subject: DataPolicySubject =
			kind === 'everyone'
				? { everyone: true }
				: kind === 'group'
					? { group: v }
					: kind === 'team'
						? { team: v }
						: kind === 'service_account'
							? { service_account: v }
							: { user: v };
		onadd(subject);
		value = '';
	}
</script>

<fieldset class="flex flex-wrap items-end gap-2">
	<legend class="sr-only">{legend}</legend>
	<Select
		label={m.governance_subject_kind()}
		bind:value={kind}
		options={kinds}
		onchange={() => (value = '')}
	/>
	{#if kind === 'group'}
		<Input label={m.governance_group_name()} bind:value placeholder="data-eu" />
	{:else if kind !== 'everyone' && entries.length > 0}
		<Select
			label={m.governance_subject_name()}
			bind:value
			options={[
				{ value: '', label: m.governance_choose() },
				...entries.map((entry) => ({
					value: entry.id,
					label: entry.detail ? `${entry.name} (${entry.detail})` : entry.name
				}))
			]}
		/>
	{:else if kind !== 'everyone'}
		<Input label={m.governance_subject_id()} bind:value />
	{/if}
	<Button size="sm" onclick={add} disabled={!canAdd}>{addLabel}</Button>
</fieldset>
