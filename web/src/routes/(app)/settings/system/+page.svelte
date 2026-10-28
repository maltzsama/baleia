<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import type { components } from '$lib/api/schema';
	import { apiErrorMessage, client } from '$lib/api/client';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';

	type Status = components['schemas']['SystemStatus'];

	let status = $state<Status | null>(null);
	let loading = $state(true);
	let error = $state('');
	let checkedAt = $state<Date | null>(null);

	type Tone = 'success' | 'warning' | 'danger' | 'neutral';

	function tone(value: string): Tone {
		switch (value) {
			case 'ok':
				return 'success';
			case 'missing':
			case 'degraded':
				return 'warning';
			case 'unreachable':
				return 'danger';
			default:
				return 'neutral';
		}
	}

	const core = $derived(
		status
			? [
					{ label: m.common_baleia(), section: status.baleia },
					{ label: m.settings_system_database(), section: status.database },
					{ label: m.settings_system_temporal(), section: status.temporal },
					{ label: m.settings_system_object_storage(), section: status.object_storage },
					{ label: m.settings_system_smtp(), section: status.smtp }
				]
			: []
	);

	const resources = $derived(
		status
			? [
					{ label: m.settings_system_schedules(), resource: status.schedules },
					{ label: 'Engines', resource: status.engines },
					{ label: 'Bindings', resource: status.bindings },
					{ label: 'Metadata', resource: status.metadata },
					{ label: m.settings_system_reconcile(), resource: status.reconcile },
					{ label: m.settings_system_audit_export(), resource: status.audit_export },
					{ label: m.settings_system_plugin(), resource: status.plugin }
				]
			: []
	);

	async function load() {
		loading = true;
		error = '';
		try {
			const { data, error: err } = await client.GET('/admin/system/status');
			if (err || !data) {
				error = apiErrorMessage(err, m.settings_system_could_not_load_the_system_status());
				return;
			}
			status = data;
			checkedAt = new Date();
		} catch {
			error = m.settings_system_could_not_load_the_system_status();
		} finally {
			loading = false;
		}
	}

	onMount(load);
</script>

<div class="flex-1 overflow-y-auto p-7">
	<!-- Lists and tables use the full width (#468). -->
	<div class="w-full">
		<div class="flex flex-wrap items-end justify-between gap-3">
			<div>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.settings_system_system_status()}</h1>
				<p class="mt-1 text-[13px] text-text-muted">
					{m.settings_system_every_platform_component_each_probed_indep()}
				</p>
			</div>
			<div class="flex items-center gap-3">
				{#if checkedAt}
					<span class="text-[11px] text-text-muted">
						{m.settings_system_checked()}
						<RelativeTime at={checkedAt} />
					</span>
				{/if}
				<Button {loading} onclick={load}>{m.settings_system_refresh()}</Button>
			</div>
		</div>

		{#if loading && !status}
			<div class="mt-6 grid grid-cols-[repeat(auto-fill,minmax(min(100%,320px),1fr))] gap-3">
				<Skeleton height="6rem" />
				<Skeleton height="6rem" />
				<Skeleton height="6rem" />
			</div>
		{:else if error}
			<div class="mt-6">
				<ErrorState title="Could not load the system status" message={error} onretry={load} />
			</div>
		{:else if status}
			<section class="mt-6">
				<h2 class="mb-2 text-[11px] font-semibold uppercase tracking-wide text-text-muted">Core</h2>
				<div class="grid grid-cols-[repeat(auto-fill,minmax(min(100%,320px),1fr))] gap-3">
					{#each core as card (card.label)}
						<article class="rounded-lg border border-border bg-bg-panel p-3">
							<div class="flex items-center justify-between gap-2">
								<h3 class="text-[13px] font-semibold">{card.label}</h3>
								<Badge tone={tone(card.section.status)}>{card.section.status}</Badge>
							</div>
							{#if card.section.error_code}
								<p class="mt-1 font-mono text-[11px] text-danger">{card.section.error_code}</p>
							{/if}
							<dl class="mt-2 space-y-0.5 text-[11px] text-text-muted">
								{#if card.section.latency_ms !== undefined}
									<div>Latency: {card.section.latency_ms} ms</div>
								{/if}
								{#if card.section.version}
									<div>Version: {card.section.version}</div>
								{/if}
								{#if card.section.commit}
									<div>Commit: {card.section.commit}</div>
								{/if}
								{#if card.section.started_at}
									<div>Started: <RelativeTime at={card.section.started_at} /></div>
								{/if}
								{#if card.section.migration_version !== undefined}
									<div>Migration: {card.section.migration_version}</div>
								{/if}
								{#if card.section.configured !== undefined}
									<div>Configured: {card.section.configured ? 'yes' : 'no'}</div>
								{/if}
							</dl>
						</article>
					{/each}
				</div>
			</section>

			<section class="mt-6">
				<h2 class="mb-2 text-[11px] font-semibold uppercase tracking-wide text-text-muted">
					Platform
				</h2>
				<div class="grid grid-cols-[repeat(auto-fill,minmax(min(100%,320px),1fr))] gap-3">
					{#each resources as card (card.label)}
						<article class="rounded-lg border border-border bg-bg-panel p-3">
							<div class="flex items-center justify-between gap-2">
								<h3 class="text-[13px] font-semibold">{card.label}</h3>
								<Badge tone={tone(card.resource.status)}>{card.resource.status}</Badge>
							</div>
							{#if card.resource.error_code}
								<p class="mt-1 font-mono text-[11px] text-danger">{card.resource.error_code}</p>
							{/if}
							{#if card.resource.items && card.resource.items.length > 0}
								<ul class="mt-2 flex flex-col gap-1">
									{#each card.resource.items as item (item.name)}
										<li class="flex items-center justify-between gap-2 text-[12px]">
											<span class="truncate font-medium">{item.name}</span>
											<span class="flex items-center gap-2">
												{#if item.detail}
													<span class="text-[11px] text-text-muted">{item.detail}</span>
												{/if}
												{#if item.value !== undefined}
													<span class="text-[11px] text-text-muted">{item.value}</span>
												{/if}
												<Badge tone={tone(item.status)}>{item.status}</Badge>
											</span>
										</li>
									{/each}
								</ul>
							{:else}
								<p class="mt-2 text-[11px] text-text-muted">No items reported.</p>
							{/if}
						</article>
					{/each}
				</div>
			</section>
		{/if}
	</div>
</div>
