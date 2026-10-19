<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import type { components } from '$lib/api/schema';
	import { apiErrorMessage, client } from '$lib/api/client';
	import Button from '$lib/ui/Button.svelte';
	import Checkbox from '$lib/ui/Checkbox.svelte';
	import IconButton from '$lib/ui/IconButton.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';

	type Detail = components['schemas']['IdentityProviderDetail'];
	type Create = components['schemas']['CreateIdentityProviderRequest'];
	type Update = components['schemas']['UpdateIdentityProviderRequest'];
	type RoleMapping = { group: string; role: 'admin' | 'member' };
	type TeamMapping = { group: string; team_id: string; team_role: 'owner' | 'member' };
	type Preview = components['schemas']['IdentityMappingPreview'];

	let {
		open = $bindable(false),
		provider = null,
		onsubmit
	}: {
		open?: boolean;
		provider?: Detail | null;
		onsubmit: (body: Create | Update) => Promise<void>;
	} = $props();

	const roleOptions = [
		{ value: 'member', label: 'Member' },
		{ value: 'admin', label: 'Admin' }
	];
	const teamRoleOptions = [
		{ value: 'member', label: 'Member' },
		{ value: 'owner', label: 'Owner' }
	];

	let name = $state('');
	let type = $state<'oidc' | 'ldap'>('oidc');
	let enabled = $state(true);
	let busy = $state(false);
	let error = $state('');

	// OIDC
	let issuerUrl = $state('');
	let clientId = $state('');
	let clientSecret = $state('');
	let autoCreate = $state(false);
	let allowedDomains = $state('');
	let oidcDefaultRole = $state<'admin' | 'member'>('member');

	// LDAP
	let url = $state('');
	let startTls = $state(false);
	let bindDn = $state('');
	let bindPassword = $state('');
	let baseDn = $state('');
	let userFilter = $state('(uid={{username}})');
	let groupAttribute = $state('');
	let emailAttribute = $state('');
	let nameAttribute = $state('');
	let nestedGroups = $state(false);
	let requiredGroup = $state('');
	let timeoutSeconds = $state('10');
	let ldapDefaultRole = $state<'admin' | 'member'>('member');

	let roleMappings = $state<RoleMapping[]>([]);
	let teamMappings = $state<TeamMapping[]>([]);

	// Mapping preview (#264)
	let previewGroups = $state('');
	let preview = $state<Preview | null>(null);
	let previewError = $state('');
	let previewing = $state(false);

	let loadedKey = '';

	function populate(source: Detail | null) {
		name = source?.name ?? '';
		type = (source?.type as 'oidc' | 'ldap') ?? 'oidc';
		enabled = source?.enabled ?? true;
		error = '';
		preview = null;
		previewError = '';
		previewGroups = '';

		const oidc = source?.oidc;
		issuerUrl = oidc?.issuer_url ?? '';
		clientId = oidc?.client_id ?? '';
		clientSecret = '';
		autoCreate = oidc?.auto_create ?? false;
		allowedDomains = (oidc?.allowed_email_domains ?? []).join(', ');
		oidcDefaultRole = (oidc?.default_role as 'admin' | 'member') ?? 'member';

		const ldap = source?.ldap;
		url = ldap?.url ?? '';
		startTls = ldap?.start_tls ?? false;
		bindDn = ldap?.bind_dn ?? '';
		bindPassword = '';
		baseDn = ldap?.base_dn ?? '';
		userFilter = ldap?.user_filter ?? '(uid={{username}})';
		groupAttribute = ldap?.group_attribute ?? '';
		emailAttribute = ldap?.email_attribute ?? '';
		nameAttribute = ldap?.name_attribute ?? '';
		nestedGroups = ldap?.nested_groups ?? false;
		requiredGroup = ldap?.required_group ?? '';
		timeoutSeconds = String(ldap?.timeout_seconds ?? 10);
		ldapDefaultRole = (ldap?.default_role as 'admin' | 'member') ?? 'member';

		roleMappings = (oidc?.role_mapping ?? ldap?.role_mapping ?? []).map((m) => ({
			group: m.group,
			role: m.role as 'admin' | 'member'
		}));
		teamMappings = (oidc?.team_mapping ?? ldap?.team_mapping ?? []).map((m) => ({
			group: m.group,
			team_id: m.team_id,
			team_role: m.team_role as 'owner' | 'member'
		}));
	}

	$effect(() => {
		if (!open) {
			loadedKey = '';
			return;
		}
		const key = provider?.id ?? 'new';
		if (loadedKey === key) return;
		loadedKey = key;
		populate(provider);
	});

	function addRole() {
		roleMappings = [...roleMappings, { group: '', role: 'member' }];
	}
	function removeRole(index: number) {
		roleMappings = roleMappings.filter((_, i) => i !== index);
	}
	function addTeam() {
		teamMappings = [...teamMappings, { group: '', team_id: '', team_role: 'member' }];
	}
	function removeTeam(index: number) {
		teamMappings = teamMappings.filter((_, i) => i !== index);
	}

	function config() {
		if (type === 'oidc') {
			return {
				issuer_url: issuerUrl,
				client_id: clientId,
				client_secret: clientSecret,
				auto_create: autoCreate,
				allowed_email_domains: allowedDomains
					.split(',')
					.map((d) => d.trim())
					.filter(Boolean),
				default_role: oidcDefaultRole,
				role_mapping: roleMappings,
				team_mapping: teamMappings
			};
		}
		return {
			url,
			start_tls: startTls,
			bind_dn: bindDn,
			bind_password: bindPassword,
			base_dn: baseDn,
			user_filter: userFilter,
			group_attribute: groupAttribute,
			email_attribute: emailAttribute,
			name_attribute: nameAttribute,
			nested_groups: nestedGroups,
			required_group: requiredGroup,
			timeout_seconds: Number(timeoutSeconds) || 10,
			default_role: ldapDefaultRole,
			role_mapping: roleMappings,
			team_mapping: teamMappings
		};
	}

	async function submit() {
		if (busy) return;
		busy = true;
		error = '';
		try {
			if (!provider) {
				const body: Create = {
					name,
					type,
					enabled
				} as Create;
				if (type === 'oidc') body.oidc = config() as Create['oidc'];
				else body.ldap = config() as Create['ldap'];
				await onsubmit(body);
			} else {
				const body: Update = { name, enabled };
				if (type === 'oidc') {
					const cfg = config() as NonNullable<Update['oidc']>;
					// An empty secret keeps the stored one; omitted means keep.
					if (!clientSecret) delete cfg.client_secret;
					body.oidc = cfg;
				} else {
					const cfg = config() as NonNullable<Update['ldap']>;
					if (!bindPassword) delete cfg.bind_password;
					body.ldap = cfg;
				}
				await onsubmit(body);
			}
			open = false;
		} catch (err) {
			error = apiErrorMessage(err, m.identity_identityproviderform_could_not_save_the_provider());
		} finally {
			busy = false;
		}
	}

	async function runPreview() {
		previewing = true;
		previewError = '';
		preview = null;
		try {
			const { data, error: err } = await client.POST('/admin/identity-providers/mapping-preview', {
				body: {
					groups: previewGroups
						.split(',')
						.map((g) => g.trim())
						.filter(Boolean),
					role_mapping: roleMappings,
					team_mapping: teamMappings,
					default_role: type === 'oidc' ? oidcDefaultRole : ldapDefaultRole
				}
			});
			if (err || !data) {
				previewError = apiErrorMessage(
					err,
					m.identity_identityproviderform_could_not_preview_the_mapping()
				);
				return;
			}
			preview = data;
		} catch {
			previewError = m.identity_identityproviderform_could_not_preview_the_mapping();
		} finally {
			previewing = false;
		}
	}
</script>

<Modal
	bind:open
	title={provider
		? m.identity_identityproviderform_edit_identity_provider()
		: m.identity_identityproviderform_add_identity_provider()}
>
	<div class="flex max-h-[70vh] flex-col gap-3 overflow-y-auto pr-1">
		{#if error}
			<p class="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-[12px] text-danger">
				{error}
			</p>
		{/if}

		<div class="grid grid-cols-2 gap-3">
			<Input
				label={m.common_name()}
				bind:value={name}
				placeholder={m.identity_identityproviderform_okta()}
			/>
			{#if !provider}
				<Select
					label={m.identity_identityproviderform_type()}
					bind:value={type}
					options={[
						{ value: 'oidc', label: 'OIDC' },
						{ value: 'ldap', label: 'LDAP' }
					]}
				/>
			{:else}
				<Input label="Type" value={type.toUpperCase()} disabled />
			{/if}
		</div>

		<Checkbox label={m.common_enabled()} bind:checked={enabled} />

		{#if type === 'oidc'}
			<div class="grid grid-cols-2 gap-3">
				<Input
					label={m.identity_identityproviderform_issuer_url()}
					bind:value={issuerUrl}
					placeholder={m.identity_identityproviderform_https_idp_example_com()}
				/>
				<Input label={m.identity_identityproviderform_client_id()} bind:value={clientId} />
			</div>
			<Input
				label={provider
					? m.identity_identityproviderform_client_secret_leave_blank_to_keep()
					: m.identity_identityproviderform_client_secret()}
				type="password"
				bind:value={clientSecret}
			/>
			<Checkbox
				label={m.identity_identityproviderform_auto_create_users_from_allowed_domains()}
				bind:checked={autoCreate}
			/>
			<Input
				label={m.identity_identityproviderform_allowed_e_mail_domains_comma_separated()}
				bind:value={allowedDomains}
				placeholder={m.identity_identityproviderform_example_com_corp_example_com()}
			/>
			<Select label={m.common_default_role()} bind:value={oidcDefaultRole} options={roleOptions} />
		{:else}
			<div class="grid grid-cols-2 gap-3">
				<Input label="URL" bind:value={url} placeholder="ldaps://ldap.example.com:636" />
				<Input label="Base DN" bind:value={baseDn} placeholder="dc=example,dc=com" />
			</div>
			<Checkbox label="StartTLS (required for ldap://)" bind:checked={startTls} />
			<div class="grid grid-cols-2 gap-3">
				<Input label="Bind DN" bind:value={bindDn} />
				<Input
					label={provider
						? m.identity_identityproviderform_bind_password_leave_blank_to_keep()
						: m.identity_identityproviderform_bind_password()}
					type="password"
					bind:value={bindPassword}
				/>
			</div>
			<Input label="User filter" bind:value={userFilter} placeholder={'(uid={{username}})'} />
			<div class="grid grid-cols-3 gap-3">
				<Input label="Group attribute" bind:value={groupAttribute} placeholder="memberOf" />
				<Input label="E-mail attribute" bind:value={emailAttribute} placeholder="mail" />
				<Input label="Name attribute" bind:value={nameAttribute} placeholder="cn" />
			</div>
			<div class="grid grid-cols-2 gap-3">
				<Input label="Required group" bind:value={requiredGroup} />
				<Input label="Timeout (seconds)" type="number" bind:value={timeoutSeconds} />
			</div>
			<Checkbox label="Nested groups (AD)" bind:checked={nestedGroups} />
			<Select label="Default role" bind:value={ldapDefaultRole} options={roleOptions} />
		{/if}

		<div class="rounded-md border border-border p-3">
			<div class="mb-2 flex items-center justify-between">
				<p class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">
					{m.identity_identityproviderform_role_mapping()}
				</p>
				<Button size="sm" variant="ghost" onclick={addRole}
					>{m.identity_identityproviderform_add_rule()}</Button
				>
			</div>
			{#each roleMappings as rule, index (index)}
				<div class="mb-2 grid grid-cols-[1fr_140px_32px] items-end gap-2">
					<Input
						label={m.identity_identityproviderform_directory_group()}
						bind:value={rule.group}
					/>
					<Select label={m.common_role()} bind:value={rule.role} options={roleOptions} />
					<IconButton
						label={m.identity_identityproviderform_remove_rule()}
						onclick={() => removeRole(index)}>×</IconButton
					>
				</div>
			{/each}
			{#if roleMappings.length === 0}
				<p class="text-[11px] text-text-muted">
					{m.identity_identityproviderform_no_rules_the_default_role_applies()}
				</p>
			{/if}
		</div>

		<div class="rounded-md border border-border p-3">
			<div class="mb-2 flex items-center justify-between">
				<p class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">
					{m.identity_identityproviderform_team_mapping()}
				</p>
				<Button size="sm" variant="ghost" onclick={addTeam}
					>{m.identity_identityproviderform_add_rule()}</Button
				>
			</div>
			{#each teamMappings as rule, index (index)}
				<div class="mb-2 grid grid-cols-[1fr_1fr_120px_32px] items-end gap-2">
					<Input
						label={m.identity_identityproviderform_directory_group()}
						bind:value={rule.group}
					/>
					<Input label={m.identity_identityproviderform_team_id()} bind:value={rule.team_id} />
					<Select
						label={m.identity_identityproviderform_team_role()}
						bind:value={rule.team_role}
						options={teamRoleOptions}
					/>
					<IconButton
						label={m.identity_identityproviderform_remove_rule()}
						onclick={() => removeTeam(index)}>×</IconButton
					>
				</div>
			{/each}
		</div>

		<div class="rounded-md border border-border p-3">
			<p class="mb-2 text-[11px] font-semibold uppercase tracking-wide text-text-muted">
				{m.identity_identityproviderform_preview_mapping()}
			</p>
			<div class="flex items-end gap-2">
				<Input
					label={m.identity_identityproviderform_test_groups_comma_separated()}
					bind:value={previewGroups}
				/>
				<Button size="sm" loading={previewing} onclick={runPreview}
					>{m.identity_identityproviderform_preview()}</Button
				>
			</div>
			{#if previewError}
				<p class="mt-2 text-[11px] text-danger">{previewError}</p>
			{/if}
			{#if preview}
				<p class="mt-2 text-[12px]">
					{m.identity_identityproviderform_role()} <strong>{preview.role}</strong>
					{#if preview.teams.length > 0}
						{m.identity_identityproviderform_teams()}
						{preview.teams.map((t) => `${t.team_id} (${t.team_role})`).join(', ')}
					{/if}
				</p>
			{/if}
		</div>
	</div>

	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (open = false)}>{m.common_cancel()}</Button>
			<Button variant="primary" loading={busy} onclick={submit}>
				{provider
					? m.retrievers_retrieverform_save_changes()
					: m.identity_identityproviderform_create_provider()}
			</Button>
		</div>
	{/snippet}
</Modal>
