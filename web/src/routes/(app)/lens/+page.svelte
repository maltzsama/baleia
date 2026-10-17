<script lang="ts">
	type LensQuery = { user?: string; sql?: string; state?: string; elapsed_ms?: number };
	type LensCluster = { active_workers?: number; running_queries?: number; queued_queries?: number };
	type TableHealth = {
		catalog: string;
		namespace: string;
		table_name: string;
		health_score: number;
		snapshot_count: number;
		needs_compaction?: boolean;
		needs_snapshot_expiry?: boolean;
	};
	type Analytics = { error_count?: number; avg_duration_ms?: number; users_active?: number };

	let queries = $state<LensQuery[]>([]);
	let cluster = $state<LensCluster | null>(null);
	let tableHealth = $state<TableHealth[]>([]);
	let tab = $state<'queries' | 'cluster' | 'tables'>('queries');
	let loading = $state(true);
	let error = $state('');
	let analytics = $state<Analytics | null>(null);

	async function load() {
		loading = true;
		error = '';
		try {
			const [q, c, h, a] = await Promise.all([
				apiFetch('/lens/queries').then((r) => r.json()),
				apiFetch('/lens/cluster').then((r) => r.json()),
				apiFetch('/lens/tables/health').then((r) => r.json()),
				apiFetch('/lens/analytics/queries')
					.then((r) => r.json())
					.catch(() => null)
			]);
			queries = q.data || q || [];
			cluster = c;
			tableHealth = Array.isArray(h) ? h : h.data || [];
			analytics = a;
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
		loading = false;
	}

	import { onMount } from 'svelte';
	import { apiFetch } from '$lib/api/client';
	onMount(load);
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="max-w-6xl">
		<div class="flex items-end justify-between mb-5">
			<div>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">Lens Monitoring</h1>
				<p class="text-[13px] text-text-muted mt-1">
					Cluster status, query history, and table health.
				</p>
			</div>
		</div>

		<div class="flex border-b border-border mb-5">
			<button
				class="py-2.5 px-4 text-[13px] font-semibold cursor-pointer border-b-2 -mb-px transition-colors duration-150 bg-none border-t-0 border-l-0 border-r-0
				{tab === 'queries'
					? 'text-primary border-b-primary'
					: 'text-text-muted border-b-transparent hover:text-text-main'}"
				onclick={() => (tab = 'queries')}
			>
				Queries
			</button>
			<button
				class="py-2.5 px-4 text-[13px] font-semibold cursor-pointer border-b-2 -mb-px transition-colors duration-150 bg-none border-t-0 border-l-0 border-r-0
				{tab === 'cluster'
					? 'text-primary border-b-primary'
					: 'text-text-muted border-b-transparent hover:text-text-main'}"
				onclick={() => (tab = 'cluster')}
			>
				Cluster
			</button>
			<button
				class="py-2.5 px-4 text-[13px] font-semibold cursor-pointer border-b-2 -mb-px transition-colors duration-150 bg-none border-t-0 border-l-0 border-r-0
				{tab === 'tables'
					? 'text-primary border-b-primary'
					: 'text-text-muted border-b-transparent hover:text-text-main'}"
				onclick={() => (tab = 'tables')}
			>
				Table Health
			</button>
		</div>

		{#if loading}
			<p class="text-text-muted">Loading...</p>
		{:else if error}
			<p class="text-red-600">{error}</p>
		{:else if tab === 'queries'}
			<div class="grid grid-cols-4 gap-3 mb-5">
				<div class="bg-bg-panel border border-border rounded-lg p-4">
					<div class="text-[22px] font-bold font-mono">{queries.length}</div>
					<div class="text-[11px] text-text-muted uppercase mt-1">Total Queries</div>
				</div>
				<div class="bg-bg-panel border border-border rounded-lg p-4">
					<div class="text-[22px] font-bold font-mono">{analytics?.error_count ?? 0}</div>
					<div class="text-[11px] text-text-muted uppercase mt-1">Errors</div>
				</div>
				<div class="bg-bg-panel border border-border rounded-lg p-4">
					<div class="text-[22px] font-bold font-mono">{analytics?.avg_duration_ms ?? 0}ms</div>
					<div class="text-[11px] text-text-muted uppercase mt-1">Avg Duration</div>
				</div>
				<div class="bg-bg-panel border border-border rounded-lg p-4">
					<div class="text-[22px] font-bold font-mono">{analytics?.users_active ?? 0}</div>
					<div class="text-[11px] text-text-muted uppercase mt-1">Active Users</div>
				</div>
			</div>

			<div class="bg-bg-panel border border-border rounded-lg overflow-hidden">
				<table class="w-full text-xs">
					<thead>
						<tr>
							<th
								class="bg-bg-toolbar py-2 px-3.5 font-semibold text-text-muted border-b border-border text-[11px] uppercase text-left"
								>User</th
							>
							<th
								class="bg-bg-toolbar py-2 px-3.5 font-semibold text-text-muted border-b border-border text-[11px] uppercase text-left"
								>Query</th
							>
							<th
								class="bg-bg-toolbar py-2 px-3.5 font-semibold text-text-muted border-b border-border text-[11px] uppercase text-left"
								>State</th
							>
							<th
								class="bg-bg-toolbar py-2 px-3.5 font-semibold text-text-muted border-b border-border text-[11px] uppercase text-left"
								>Duration</th
							>
						</tr>
					</thead>
					<tbody>
						{#each queries.slice(0, 20) as q, i (i)}
							<tr class="hover:bg-bg-toolbar">
								<td class="py-2 px-3.5 border-b border-border">{q.user || '-'}</td>
								<td class="py-2 px-3.5 border-b border-border font-mono truncate max-w-[300px]"
									>{q.sql || '-'}</td
								>
								<td class="py-2 px-3.5 border-b border-border">
									<span
										class="inline-flex items-center gap-1 text-[10px] font-semibold font-mono rounded-full px-2 py-0.5
										{q.state === 'finished'
											? 'bg-green-100 text-green-700'
											: q.state === 'failed'
												? 'bg-red-100 text-red-700'
												: 'bg-blue-100 text-blue-700'}"
									>
										{q.state || 'running'}
									</span>
								</td>
								<td class="py-2 px-3.5 border-b border-border font-mono">{q.elapsed_ms ?? '-'}ms</td
								>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{:else if tab === 'cluster'}
			{#if cluster}
				<div class="grid grid-cols-4 gap-3 mb-5">
					<div class="bg-bg-panel border border-border rounded-lg p-4">
						<div class="text-[22px] font-bold font-mono">{cluster.active_workers ?? '-'}</div>
						<div class="text-[11px] text-text-muted uppercase mt-1">Active Workers</div>
					</div>
					<div class="bg-bg-panel border border-border rounded-lg p-4">
						<div class="text-[22px] font-bold font-mono">{cluster.running_queries ?? '-'}</div>
						<div class="text-[11px] text-text-muted uppercase mt-1">Running Queries</div>
					</div>
					<div class="bg-bg-panel border border-border rounded-lg p-4">
						<div class="text-[22px] font-bold font-mono">{cluster.queued_queries ?? '-'}</div>
						<div class="text-[11px] text-text-muted uppercase mt-1">Queued</div>
					</div>
				</div>
			{/if}
		{:else if tab === 'tables'}
			<div class="bg-bg-panel border border-border rounded-lg overflow-hidden">
				<table class="w-full text-xs">
					<thead>
						<tr>
							<th
								class="bg-bg-toolbar py-2 px-3.5 font-semibold text-text-muted border-b border-border text-[11px] uppercase text-left"
								>Table</th
							>
							<th
								class="bg-bg-toolbar py-2 px-3.5 font-semibold text-text-muted border-b border-border text-[11px] uppercase text-left"
								>Score</th
							>
							<th
								class="bg-bg-toolbar py-2 px-3.5 font-semibold text-text-muted border-b border-border text-[11px] uppercase text-left"
								>Snapshots</th
							>
							<th
								class="bg-bg-toolbar py-2 px-3.5 font-semibold text-text-muted border-b border-border text-[11px] uppercase text-left"
								>Needs</th
							>
						</tr>
					</thead>
					<tbody>
						{#each tableHealth as t (t.catalog + '.' + t.namespace + '.' + t.table_name)}
							<tr class="hover:bg-bg-toolbar">
								<td class="py-2 px-3.5 border-b border-border font-mono"
									>{t.catalog}.{t.namespace}.{t.table_name}</td
								>
								<td class="py-2 px-3.5 border-b border-border">
									<div class="w-full bg-border rounded-full h-2">
										<div
											class="h-2 rounded-full transition-all {t.health_score >= 70
												? 'bg-green-500'
												: t.health_score >= 40
													? 'bg-amber-500'
													: 'bg-red-500'}"
											style="width: {t.health_score}%"
										></div>
									</div>
									<span class="text-[10px] text-text-muted">{t.health_score}/100</span>
								</td>
								<td class="py-2 px-3.5 border-b border-border font-mono">{t.snapshot_count}</td>
								<td class="py-2 px-3.5 border-b border-border">
									{#if t.needs_compaction}<span
											class="bg-amber-100 text-amber-700 text-[10px] px-1.5 py-0.5 rounded mr-1"
											>compact</span
										>{/if}
									{#if t.needs_snapshot_expiry}<span
											class="bg-red-100 text-red-700 text-[10px] px-1.5 py-0.5 rounded">expire</span
										>{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</div>
</div>
