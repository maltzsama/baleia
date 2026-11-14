<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import type { components } from '$lib/api/schema';
	import { apiErrorMessage, client } from '$lib/api/client';
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

	// A quiet dot says "ok"; anything else is named and colored, so a problem
	// stands out of a page of healthy rows.
	const dots: Record<Tone, string> = {
		success: 'bg-green-500',
		warning: 'bg-amber-500',
		danger: 'bg-red-500',
		neutral: 'bg-slate-400'
	};
	const texts: Record<Tone, string> = {
		success: 'text-green-700 dark:text-green-300',
		warning: 'text-amber-700 dark:text-amber-300',
		danger: 'text-red-700 dark:text-red-300',
		neutral: 'text-text-muted'
	};
	const accents: Record<Tone, string> = {
		success: 'border-border',
		warning: 'border-amber-300 dark:border-amber-700',
		danger: 'border-red-300 dark:border-red-800',
		neutral: 'border-border'
	};

	const statLabel = 'text-[10px] uppercase tracking-wide text-text-muted';

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
					{
						get label() {
							return m.settings_system_engines();
						},
						resource: status.engines
					},
					{
						get label() {
							return m.settings_system_bindings();
						},
						resource: status.bindings
					},
					{
						get label() {
							return m.settings_system_metadata();
						},
						resource: status.metadata
					},
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

{#snippet state(value: string)}
	<span class="inline-flex items-center gap-1.5 text-[11px] font-semibold {texts[tone(value)]}">
		<span class="h-2 w-2 rounded-full {dots[tone(value)]}" aria-hidden="true"></span>
		{value}
	</span>
{/snippet}

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
						<RelativeTime at={checkedAt} live />
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
				<ErrorState
					title={m.settings_system_could_not_load_the_system_status_2()}
					message={error}
					onretry={load}
				/>
			</div>
		{:else if status}
			<section class="mt-6">
				<h2 class="mb-2 text-[11px] font-semibold uppercase tracking-wide text-text-muted">
					{m.settings_system_core()}
				</h2>
				<div
					class="grid grid-cols-[repeat(auto-fill,minmax(min(100%,300px),1fr))] items-start gap-3"
				>
					{#each core as card (card.label)}
						<article
							class="rounded-xl border bg-bg-panel p-4 shadow-sm {accents[
								tone(card.section.status)
							]}"
						>
							<header class="flex items-center justify-between gap-2">
								<h3 class="text-[13px] font-semibold">{card.label}</h3>
								{@render state(card.section.status)}
							</header>
							{#if card.section.error_code}
								<p class="mt-1 font-mono text-[11px] text-danger">{card.section.error_code}</p>
							{/if}
							<dl class="mt-3 grid grid-cols-2 gap-x-4 gap-y-2.5">
								{#if card.section.latency_ms !== undefined}
									<div>
										<dt class={statLabel}>{m.settings_system_stat_latency()}</dt>
										<dd class="font-mono text-[13px]">
											{m.settings_system_stat_ms({ ms: card.section.latency_ms })}
										</dd>
									</div>
								{/if}
								{#if card.section.version}
									<div>
										<dt class={statLabel}>{m.settings_system_stat_version()}</dt>
										<dd class="truncate font-mono text-[13px]">{card.section.version}</dd>
									</div>
								{/if}
								{#if card.section.commit}
									<div>
										<dt class={statLabel}>{m.settings_system_stat_commit()}</dt>
										<dd class="truncate font-mono text-[13px]">{card.section.commit}</dd>
									</div>
								{/if}
								{#if card.section.started_at}
									<div>
										<dt class={statLabel}>{m.settings_system_stat_started()}</dt>
										<dd class="text-[13px]"><RelativeTime at={card.section.started_at} /></dd>
									</div>
								{/if}
								{#if card.section.migration_version !== undefined}
									<div>
										<dt class={statLabel}>{m.settings_system_stat_migration()}</dt>
										<dd class="font-mono text-[13px]">{card.section.migration_version}</dd>
									</div>
								{/if}
								{#if card.section.configured !== undefined}
									<div>
										<dt class={statLabel}>{m.settings_system_stat_configured()}</dt>
										<dd class="text-[13px]">
											{card.section.configured ? m.settings_system_yes() : m.settings_system_no()}
										</dd>
									</div>
								{/if}
							</dl>
						</article>
					{/each}
				</div>
			</section>

			<section class="mt-6">
				<h2 class="mb-2 text-[11px] font-semibold uppercase tracking-wide text-text-muted">
					{m.settings_system_platform()}
				</h2>
				<div class="columns-[19rem] gap-3">
					{#each resources as card (card.label)}
						<article
							class="mb-3 break-inside-avoid rounded-xl border bg-bg-panel p-4 shadow-sm {accents[
								tone(card.resource.status)
							]}"
						>
							<header class="flex items-center justify-between gap-2">
								<h3 class="text-[13px] font-semibold">{card.label}</h3>
								{@render state(card.resource.status)}
							</header>
							{#if card.resource.error_code}
								<p class="mt-1 font-mono text-[11px] text-danger">{card.resource.error_code}</p>
							{/if}
							{#if card.resource.items && card.resource.items.length > 0}
								<ul class="mt-3 divide-y divide-border/60">
									{#each card.resource.items as item (item.name)}
										<li class="flex items-center justify-between gap-3 py-1.5 text-[12px]">
											<span class="flex min-w-0 items-center gap-2">
												<span
													class="h-2 w-2 shrink-0 rounded-full {dots[tone(item.status)]}"
													aria-hidden="true"
												></span>
												<span class="truncate font-medium">{item.name}</span>
											</span>
											<span class="flex shrink-0 items-center gap-2 text-[11px]">
												{#if item.detail}
													<span class="font-mono text-text-muted">{item.detail}</span>
												{/if}
												{#if item.value !== undefined}
													<span class="font-mono text-text-muted">{item.value}</span>
												{/if}
												{#if item.status === 'ok'}
													<span class="sr-only">{item.status}</span>
												{:else}
													<span class="font-semibold {texts[tone(item.status)]}">{item.status}</span
													>
												{/if}
											</span>
										</li>
									{/each}
								</ul>
							{:else}
								<p class="mt-2 text-[11px] text-text-muted">
									{m.settings_system_no_items_reported()}
								</p>
							{/if}
						</article>
					{/each}
				</div>
			</section>
		{/if}
	</div>
</div>
