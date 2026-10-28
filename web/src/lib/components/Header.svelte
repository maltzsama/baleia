<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { locate, sectionTitle, visiblePages } from '$lib/nav/sections';
	import AttentionBell from '$lib/components/AttentionBell.svelte';
	import { client } from '$lib/api/client';
	import { canGlobal } from '$lib/authz/can';
	import { authStore } from '$lib/stores/auth.svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import type { Workspace } from '$lib/stores/workspace.svelte';

	// The section the page belongs to (#465), from the shared nav map. An area
	// with several pages shows them as tabs next to its name (#464).
	const located = $derived(locate(page.url.pathname));
	const tabs = $derived(
		located && located.area.key !== 'settings'
			? visiblePages(located.area, authStore.user?.features)
			: []
	);
	const title = $derived(
		tabs.length > 1 && located ? located.area.label() : sectionTitle(page.url.pathname)
	);
	let {
		onToggleDark,
		dark
	}: {
		onToggleDark: () => void;
		dark: boolean;
	} = $props();

	let wsOpen = $state(false);
	let showCreate = $state(false);
	let createName = $state('');
	let createDescription = $state('');
	let createType = $state<'personal' | 'shared'>('personal');
	let createTeamId = $state('');
	let teams = $state<{ id: string; name: string }[]>([]);
	let creating = $state(false);
	let createError = $state('');
	let search = $state('');

	const canShare = $derived(canGlobal(authStore.user, 'create_shared_workspace'));
	const showSearch = $derived(workspaceStore.workspaces.length > 8);
	const personal = $derived(
		workspaceStore.workspaces.filter(
			(ws) =>
				ws.type === 'personal' && (!search || ws.name.toLowerCase().includes(search.toLowerCase()))
		)
	);
	const shared = $derived(
		workspaceStore.workspaces.filter(
			(ws) =>
				ws.type === 'shared' && (!search || ws.name.toLowerCase().includes(search.toLowerCase()))
		)
	);
	const canUpdateCurrent = $derived(!!workspaceStore.current?.allowed_actions?.includes('update'));

	function toggleWs() {
		wsOpen = !wsOpen;
		closeCreate();
	}

	function selectWs(ws: Workspace) {
		workspaceStore.select(ws);
		if (typeof window !== 'undefined') {
			const url = new URL(window.location.href);
			url.searchParams.set('ws', ws.id);
			window.history.replaceState(null, '', url);
		}
		wsOpen = false;
	}

	async function openCreate(e: Event) {
		e.stopPropagation();
		createName = workspaceStore.workspaces.length === 0 ? 'Personal' : '';
		createDescription = '';
		createType = 'personal';
		createTeamId = '';
		createError = '';
		showCreate = true;
		wsOpen = false;
		if (canShare && teams.length === 0) {
			const { data } = await client.GET('/teams');
			teams = (data ?? []) as { id: string; name: string }[];
		}
	}

	function closeCreate() {
		showCreate = false;
		createName = '';
		createError = '';
	}

	async function doCreate() {
		if (!createName.trim()) return;
		if (createType === 'shared' && !createTeamId) {
			createError = m.components_header_choose_the_team_that_owns_the_workspace();
			return;
		}
		creating = true;
		createError = '';
		const ws = await workspaceStore.create({
			name: createName.trim(),
			description: createDescription.trim() || undefined,
			type: createType,
			ownerTeamId: createTeamId || undefined
		});
		creating = false;
		if (ws) closeCreate();
		else createError = 'Could not create the workspace.';
	}

	function openSettings() {
		if (!workspaceStore.current) return;
		window.location.href = `/settings/workspaces/${workspaceStore.current.id}`;
		wsOpen = false;
	}
</script>

<header
	class="h-12 bg-bg-panel flex items-center px-4 border-b border-border gap-3 shrink-0 relative z-40"
>
	<span class="min-w-0 max-w-[40%] shrink truncate text-sm font-semibold" {title}>{title}</span>

	{#if tabs.length > 1 && located}
		<nav aria-label={located.area.label()} class="flex h-full items-stretch gap-1">
			{#each tabs as tab (tab.href)}
				<a
					href={resolve(tab.href as '/')}
					aria-current={located.page?.href === tab.href ? 'page' : undefined}
					class="flex items-center border-b-2 px-2 text-[12px] font-semibold no-underline transition-colors duration-150
						{located.page?.href === tab.href
						? 'border-primary text-primary'
						: 'border-transparent text-text-muted hover:text-text-main'}"
				>
					{tab.label()}
				</a>
			{/each}
		</nav>
	{/if}

	<div class="relative">
		{#if workspaceStore.current}
			<button
				onclick={toggleWs}
				class="flex items-center gap-2 bg-slate-50 dark:bg-slate-800 border border-border hover:border-border-hover rounded-md px-[10px] py-[5px] cursor-pointer text-xs font-semibold transition-all duration-150"
			>
				<div class="w-2 h-2 rounded-full bg-success shrink-0"></div>
				<span>{workspaceStore.current.name}</span>
				<span class="text-text-muted font-normal">{m.components_header_workspace()}</span>
				<svg viewBox="0 0 24 24" class="w-3 h-3"><path d="M6 9l6 6 6-6" /></svg>
			</button>
		{:else}
			<button
				onclick={toggleWs}
				class="flex items-center gap-2 border border-dashed border-border hover:border-border-hover rounded-md px-[10px] py-[5px] cursor-pointer text-xs font-semibold transition-all duration-150 text-text-muted hover:text-text-main"
			>
				<svg viewBox="0 0 24 24" class="w-3 h-3"
					><circle cx="12" cy="12" r="10" /><path d="M12 8v8M8 12h8" /></svg
				>
				{workspaceStore.workspaces.length > 0
					? m.components_header_select_workspace()
					: m.components_header_create_workspace()}
			</button>
		{/if}

		{#if wsOpen}
			<div
				class="absolute top-full left-0 mt-1 w-72 bg-bg-panel border border-border rounded-lg shadow-lg z-50 py-1"
			>
				{#if showSearch}
					<div class="px-2 pb-1">
						<input
							class="w-full h-8 rounded-md border border-border bg-bg-toolbar px-2 text-xs outline-none focus:border-primary"
							placeholder={m.components_header_search_workspaces()}
							bind:value={search}
						/>
					</div>
				{/if}
				{#if personal.length > 0}
					<p
						class="px-3 pb-0.5 pt-1 text-[9px] font-semibold uppercase tracking-wide text-text-muted"
					>
						{m.components_header_personal()}
					</p>
					{#each personal as ws (ws.id)}
						<button
							onclick={() => selectWs(ws)}
							class="w-full text-left px-3 py-2 text-xs hover:bg-slate-100 dark:hover:bg-slate-800 flex items-center gap-2 cursor-pointer bg-none border-none text-inherit"
						>
							<span
								class="w-1.5 h-1.5 rounded-full {ws.id === workspaceStore.current?.id
									? 'bg-primary'
									: 'bg-transparent border border-border'}"
							></span>
							<span class="font-medium">{ws.name}</span>
						</button>
					{/each}
				{/if}
				{#if shared.length > 0}
					<p
						class="px-3 pb-0.5 pt-1 text-[9px] font-semibold uppercase tracking-wide text-text-muted"
					>
						{m.components_header_shared()}
					</p>
					{#each shared as ws (ws.id)}
						<button
							onclick={() => selectWs(ws)}
							class="w-full text-left px-3 py-2 text-xs hover:bg-slate-100 dark:hover:bg-slate-800 flex items-center gap-2 cursor-pointer bg-none border-none text-inherit"
						>
							<span
								class="w-1.5 h-1.5 rounded-full {ws.id === workspaceStore.current?.id
									? 'bg-primary'
									: 'bg-transparent border border-border'}"
							></span>
							<span class="font-medium">{ws.name}</span>
							<span class="ml-auto text-[10px] text-text-muted"
								>{m.components_header_shared_2()}</span
							>
						</button>
					{/each}
				{/if}
				{#if personal.length === 0 && shared.length === 0}
					<div class="px-3 py-3 text-xs text-text-muted text-center">
						{m.components_header_no_workspaces()}
					</div>
				{/if}
				<div class="border-t border-border my-1"></div>
				{#if canUpdateCurrent}
					<button
						onclick={openSettings}
						class="w-full text-left px-3 py-2 text-xs hover:bg-slate-100 dark:hover:bg-slate-800 flex items-center gap-2 cursor-pointer bg-none border-none text-inherit"
					>
						{m.components_header_workspace_settings()}
					</button>
				{/if}
				<a
					href={resolve('/settings')}
					class="w-full text-left px-3 py-2 text-xs hover:bg-slate-100 dark:hover:bg-slate-800 flex items-center gap-2 text-inherit no-underline"
					onclick={() => (wsOpen = false)}
				>
					{m.common_settings()}
				</a>
				<button
					onclick={openCreate}
					class="w-full text-left px-3 py-2 text-xs hover:bg-slate-100 dark:hover:bg-slate-800 flex items-center gap-2 cursor-pointer bg-none border-none text-inherit text-primary font-medium"
				>
					<svg viewBox="0 0 24 24" class="w-3.5 h-3.5"><path d="M12 5v14M5 12h14" /></svg>
					{m.components_header_create_workspace()}
				</button>
			</div>
		{/if}
	</div>

	<div class="flex-1"></div>

	<AttentionBell />

	<div class="flex items-center gap-1.5 text-[11px] text-text-muted">
		<svg viewBox="0 0 24 24" class="w-[13px] h-[13px]"
			><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" /></svg
		>
		<div
			role="switch"
			aria-label={m.components_header_dark_mode()}
			aria-checked={dark}
			tabindex="0"
			onclick={onToggleDark}
			onkeydown={(event) => {
				if (event.key === 'Enter' || event.key === ' ') {
					event.preventDefault();
					onToggleDark();
				}
			}}
			class="w-8 h-[18px] rounded-full cursor-pointer relative transition-colors duration-200 shrink-0
				{dark ? 'bg-primary' : 'bg-border'}"
		>
			<span
				class="absolute w-[14px] h-[14px] bg-white rounded-full top-[2px] left-[2px] shadow-sm transition-transform duration-200
				{dark ? 'translate-x-[14px]' : 'translate-x-0'}"
			>
			</span>
		</div>
	</div>
</header>

{#if showCreate}
	<div
		class="fixed inset-0 bg-black/30 backdrop-blur-[2px] z-[100] flex items-center justify-center p-6"
		onclick={(e) => e.target === e.currentTarget && closeCreate()}
	>
		<div
			class="bg-bg-panel border border-border rounded-xl w-full max-w-[400px] shadow-[0_24px_64px_rgba(0,0,0,.2)]"
			onclick={(e) => e.stopPropagation()}
		>
			<div class="flex items-center px-5 py-[18px] border-b border-border">
				<h2 class="text-sm font-semibold flex-1">{m.components_header_create_workspace()}</h2>
				<button
					class="w-[26px] h-[26px] rounded-md border-none bg-transparent cursor-pointer flex items-center justify-center text-text-muted hover:bg-bg-toolbar"
					onclick={closeCreate}>×</button
				>
			</div>
			<div class="flex flex-col gap-3 p-5">
				<input
					class="w-full h-9 bg-bg-toolbar border border-border rounded-md px-2.5 text-xs outline-none focus:border-primary"
					type="text"
					placeholder={m.components_header_workspace_name()}
					bind:value={createName}
					onkeydown={(e) => e.key === 'Enter' && doCreate()}
				/>
				<input
					class="w-full h-9 bg-bg-toolbar border border-border rounded-md px-2.5 text-xs outline-none focus:border-primary"
					type="text"
					placeholder={m.components_header_description_optional()}
					bind:value={createDescription}
				/>
				{#if canShare}
					<select
						class="w-full h-9 bg-bg-toolbar border border-border rounded-md px-2.5 text-xs outline-none focus:border-primary"
						bind:value={createType}
					>
						<option value="personal">{m.components_header_personal()}</option>
						<option value="shared">{m.components_header_shared()}</option>
					</select>
					{#if createType === 'shared'}
						<select
							class="w-full h-9 bg-bg-toolbar border border-border rounded-md px-2.5 text-xs outline-none focus:border-primary"
							bind:value={createTeamId}
						>
							<option value="">{m.components_header_choose_the_owning_team()}</option>
							{#each teams as team (team.id)}
								<option value={team.id}>{team.name}</option>
							{/each}
						</select>
					{/if}
				{/if}
				{#if createError}<p class="text-[11px] text-danger" role="alert">{createError}</p>{/if}
				<div class="flex justify-end gap-1.5">
					<button
						class="h-[34px] px-3.5 rounded-md border border-border bg-transparent text-xs font-medium text-text-muted cursor-pointer hover:border-border-hover"
						onclick={closeCreate}>{m.common_cancel()}</button
					>
					<button
						class="h-[34px] px-4 rounded-md border-none bg-primary text-white text-xs font-semibold cursor-pointer hover:bg-primary-hover disabled:opacity-50 disabled:cursor-not-allowed"
						disabled={!createName.trim() || creating}
						onclick={doCreate}
					>
						{creating ? m.components_header_creating() : 'Create'}
					</button>
				</div>
			</div>
		</div>
	</div>
{/if}
