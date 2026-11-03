<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { authStore } from '$lib/stores/auth.svelte';
	import { canGlobal } from '$lib/authz/can';
	import { onMount } from 'svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Modal from '$lib/ui/Modal.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import EngineBadge from '$lib/ui/patterns/EngineBadge.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import StatusBadge from '$lib/ui/patterns/StatusBadge.svelte';
	import {
		createEngine,
		listCatalogConnections,
		listEngines,
		readOnlyGuarantee,
		type CatalogConnection,
		type Engine
	} from '$lib/engines/admin';

	let engines = $state<Engine[]>([]);
	let connections = $state<CatalogConnection[]>([]);
	let loading = $state(true);
	let error = $state('');

	let classFilter = $state('');
	let kindFilter = $state('');
	let healthFilter = $state('');
	let enabledFilter = $state('');

	let registerOpen = $state(false);
	let registerConnectionId = $state('');
	let registerName = $state('');
	let registering = $state(false);
	let registerError = $state('');

	const kinds = $derived([...new Set(engines.map((engine) => engine.kind))].sort());
	const filtered = $derived(
		engines.filter((engine) => {
			if (classFilter && engine.class !== classFilter) return false;
			if (kindFilter && engine.kind !== kindFilter) return false;
			if (healthFilter && engineHealth(engine) !== healthFilter) return false;
			if (enabledFilter === 'enabled' && !engine.enabled) return false;
			if (enabledFilter === 'disabled' && engine.enabled) return false;
			return true;
		})
	);
	const canRegister = $derived(connections.length > 0);

	function clearFilters() {
		classFilter = '';
		kindFilter = '';
		healthFilter = '';
		enabledFilter = '';
	}

	function engineHealth(engine: Engine): string {
		if (!engine.enabled) return 'disabled';
		return engine.health?.status ?? 'unknown';
	}

	async function load() {
		loading = true;
		error = '';
		try {
			engines = await listEngines();
			connections = await listCatalogConnections({
				action: 'register_engine',
				without_engine: true
			});
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	function openRegister() {
		registerName = '';
		registerError = '';
		registerConnectionId = connections[0]?.id ?? '';
		registerOpen = true;
	}

	async function submitRegister() {
		if (!registerName.trim() || !registerConnectionId) {
			registerError = m.engines_choose_a_connection_and_name_the_engine();
			return;
		}
		registering = true;
		registerError = '';
		try {
			const engine = await createEngine({
				name: registerName.trim(),
				connection_id: registerConnectionId
			});
			registerOpen = false;
			window.location.href = `/engines/${engine.id}`;
		} catch (e) {
			registerError = e instanceof Error ? e.message : String(e);
		} finally {
			registering = false;
		}
	}

	onMount(load);
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4 flex flex-wrap items-end justify-between gap-3">
		<div>
			<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.common_engines()}</h1>
			<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
				{m.engines_what_runs_your_sql_what_it_guarantees_and()}
			</p>
		</div>
		{#if canRegister}
			<Button variant="primary" onclick={openRegister}>{m.engines_register_engine()}</Button>
		{/if}
	</div>

	{#if loading}
		<div class="flex flex-col gap-2" role="status" aria-label={m.engines_loading_engines()}>
			{#each [0, 1, 2] as item (item)}
				<Skeleton class="h-12 w-full" />
			{/each}
		</div>
	{:else if error}
		<ErrorState title={m.engines_could_not_load_engines()} message={error} onretry={load} />
	{:else if engines.length === 0}
		<EmptyState
			title={m.engines_no_engines_yet()}
			description={m.engines_a_trino_engine_is_usually_registered_by_the_deployment()}
		>
			<!-- The next step (#476): register one, or first add a database connection. -->
			{#if canRegister}
				<Button variant="primary" onclick={openRegister}>{m.engines_register_engine()}</Button>
			{:else if canGlobal(authStore.user, 'create_connection')}
				<Button variant="primary" onclick={() => (window.location.href = '/connections/new')}>
					{m.connections_new_connection()}
				</Button>
			{/if}
		</EmptyState>
	{:else}
		<div
			class="mb-3 grid gap-2 rounded-lg border border-border bg-bg-panel p-3 sm:grid-cols-2 lg:grid-cols-4"
		>
			<Select
				label={m.engines_class()}
				bind:value={classFilter}
				options={[
					{ value: '', label: m.monitoring_any() },
					{
						value: 'federated',
						get label() {
							return m.engines_federated();
						}
					},
					{
						value: 'direct',
						get label() {
							return m.engines_direct();
						}
					}
				]}
			/>
			<Select
				label={m.engines_kind()}
				bind:value={kindFilter}
				options={[
					{ value: '', label: m.monitoring_any() },
					...kinds.map((kind) => ({ value: kind, label: kind }))
				]}
			/>
			<Select
				label={m.engines_health()}
				bind:value={healthFilter}
				options={[
					{ value: '', label: m.monitoring_any() },
					{ value: 'ok', label: m.statusbadge_healthy() },
					{
						value: 'unreachable',
						get label() {
							return m.engines_unreachable();
						}
					},
					{
						value: 'unknown',
						get label() {
							return m.engines_unknown();
						}
					}
				]}
			/>
			<Select
				label={m.engines_state()}
				bind:value={enabledFilter}
				options={[
					{ value: '', label: m.monitoring_any() },
					{
						value: 'enabled',
						get label() {
							return m.engines_enabled();
						}
					},
					{
						value: 'disabled',
						get label() {
							return m.engines_disabled();
						}
					}
				]}
			/>
		</div>

		{#if filtered.length === 0}
			<div role="status">
				<EmptyState
					title={m.engines_no_engine_matches()}
					description={m.common_change_or_clear_the_filters()}
				>
					<Button onclick={clearFilters}>{m.common_clear_filters()}</Button>
				</EmptyState>
			</div>
		{:else}
			<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
				<table class="w-full text-left text-[11px]">
					<thead
						class="border-b border-border text-[10px] uppercase tracking-wide text-slate-600 dark:text-slate-300"
					>
						<tr>
							<th class="px-3 py-2">{m.engines_engine()}</th>
							<th class="px-3 py-2">{m.engines_version()}</th>
							<th class="px-3 py-2">{m.engines_health()}</th>
							<th class="px-3 py-2">{m.engines_connection()}</th>
							<th class="px-3 py-2 text-right">{m.engines_catalogs()}</th>
							<th class="px-3 py-2">{m.engines_read_only()}</th>
							<th class="px-3 py-2">{m.engines_state()}</th>
						</tr>
					</thead>
					<tbody>
						{#each filtered as engine (engine.id)}
							{@const chip = readOnlyGuarantee(engine)}
							<tr class="border-b border-border/60 last:border-b-0 hover:bg-bg-toolbar">
								<td class="px-3 py-2">
									<button
										type="button"
										class="text-left hover:underline"
										onclick={() => (window.location.href = `/engines/${engine.id}`)}
									>
										<EngineBadge {engine} />
									</button>
								</td>
								<td class="px-3 py-2 font-mono">{engine.version ?? '—'}</td>
								<td class="px-3 py-2">
									<div class="flex flex-col gap-0.5">
										<StatusBadge kind="engine" status={engineHealth(engine)} />
										{#if engine.health?.checked_at}
											<span class="text-[10px] text-slate-600 dark:text-slate-300">
												{m.engines_checked()}
												<RelativeTime at={engine.health.checked_at} />
											</span>
										{/if}
									</div>
								</td>
								<td class="px-3 py-2">
									{#if engine.connection}
										{engine.connection.name}
									{:else}
										<span class="font-mono">{engine.connection_id}</span>
									{/if}
								</td>
								<td class="px-3 py-2 text-right">{engine.catalog_count ?? 0}</td>
								<td class="px-3 py-2">
									<span title={chip.detail}><Badge tone={chip.tone}>{chip.label}</Badge></span>
								</td>
								<td class="px-3 py-2">
									<Badge tone={engine.enabled ? 'success' : 'neutral'}>
										{engine.enabled ? m.engines_enabled() : m.engines_disabled()}
									</Badge>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	{/if}
</div>

<Modal bind:open={registerOpen} title={m.engines_register_engine()}>
	<div class="flex flex-col gap-3 text-[12px]">
		<p class="text-slate-600 dark:text-slate-300">
			{m.engines_registering_probes_the_connection_and_disc()}
		</p>
		<Select
			label={m.engines_connection()}
			bind:value={registerConnectionId}
			options={connections.map((connection) => ({
				value: connection.id,
				label: `${connection.name} (${connection.type})`
			}))}
		/>
		<Input
			label={m.common_name()}
			bind:value={registerName}
			placeholder={m.engines_analytics_pg()}
			class="font-mono"
		/>
		{#if registerError}
			<p class="text-danger" role="alert">{registerError}</p>
		{/if}
	</div>
	{#snippet footer()}
		<div class="flex justify-end gap-2">
			<Button onclick={() => (registerOpen = false)} disabled={registering}
				>{m.common_cancel()}</Button
			>
			<Button
				variant="primary"
				loading={registering}
				disabled={registering}
				onclick={submitRegister}
			>
				{m.common_register()}
			</Button>
		</div>
	{/snippet}
</Modal>
