<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import Badge from '$lib/ui/Badge.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import { listAuditEvents, type AuditEvent } from './client';

	let {
		resourceType,
		resourceId,
		limit = 50,
		title = m.audit_activitypanel_activity()
	}: {
		resourceType: string;
		resourceId: string;
		limit?: number;
		title?: string;
	} = $props();

	let events = $state<AuditEvent[]>([]);
	let loading = $state(true);
	let failed = $state(false);

	const viewAll = $derived(
		`${resolve('/settings/audit')}?resource_type=${encodeURIComponent(resourceType)}&resource_id=${encodeURIComponent(resourceId)}`
	);

	onMount(async () => {
		try {
			const page = await listAuditEvents({ resourceType, resourceId, limit });
			events = page.items;
		} catch {
			failed = true;
		} finally {
			loading = false;
		}
	});
</script>

{#if loading}
	<div class="flex flex-col gap-2" aria-label={m.audit_activitypanel_loading({ title })}>
		<Skeleton class="h-5 w-2/3" />
		<Skeleton class="h-5 w-3/4" />
	</div>
{:else if !failed && events.length > 0}
	<section class="rounded-lg border border-border bg-bg-panel p-4" aria-label={title}>
		<div class="flex items-center justify-between">
			<h2 class="text-[13px] font-semibold">{title}</h2>
			<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- the query string is built from the resource -->
			<a class="text-[11px] font-semibold text-primary hover:underline" href={viewAll}
				>{m.audit_activitypanel_view_all()}</a
			>
		</div>
		<ul class="mt-2 flex flex-col gap-1.5 text-[11px]">
			{#each events as event (event.id)}
				<li class="flex flex-wrap items-center gap-1.5">
					<Badge
						tone={event.outcome === 'allowed'
							? 'success'
							: event.outcome === 'denied'
								? 'danger'
								: 'warning'}
					>
						{event.outcome}
					</Badge>
					<span class="font-mono">{event.action}</span>
					<span class="text-slate-600 dark:text-slate-300">
						{m.audit_activitypanel_by({ actor: event.actor_id ?? event.actor_kind })}
					</span>
					<span class="ml-auto text-slate-600 dark:text-slate-300">
						<RelativeTime at={event.occurred_at} />
					</span>
				</li>
			{/each}
		</ul>
	</section>
{/if}
