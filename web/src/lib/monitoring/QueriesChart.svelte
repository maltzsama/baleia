<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { getLocale } from '$lib/paraglide/runtime';

	// Running queries over the selected window (#482). One series, so the
	// title names it and there is no legend; the axes give the time window and
	// the count, a crosshair and tooltip follow the pointer or the arrow keys,
	// and the same points are available as a table.
	type Point = { captured_at: string; running_queries: number };

	let { series }: { series: Point[] } = $props();

	const HEIGHT = 140;
	const PAD = { top: 10, right: 12, bottom: 22, left: 34 };

	let width = $state(600);
	let active = $state<number | null>(null);
	let tableOpen = $state(false);

	const times = $derived(series.map((point) => new Date(point.captured_at).getTime()));
	const start = $derived(times[0] ?? 0);
	const end = $derived(times[times.length - 1] ?? 1);
	// A whole-number top tick, so the axis reads in queries.
	const top = $derived(Math.max(1, Math.ceil(Math.max(...series.map((p) => p.running_queries)))));
	const yTicks = $derived(top <= 2 ? [0, top] : [0, Math.round(top / 2), top]);

	const plotW = $derived(Math.max(1, width - PAD.left - PAD.right));
	const plotH = HEIGHT - PAD.top - PAD.bottom;
	const x = (time: number) =>
		PAD.left + (end === start ? 0 : ((time - start) / (end - start)) * plotW);
	const y = (value: number) => PAD.top + plotH - (value / top) * plotH;

	const path = $derived(
		series
			.map(
				(point, i) =>
					`${i === 0 ? 'M' : 'L'}${x(times[i]).toFixed(1)},${y(point.running_queries).toFixed(1)}`
			)
			.join(' ')
	);

	const timeFormat = $derived(
		new Intl.DateTimeFormat(getLocale(), { hour: '2-digit', minute: '2-digit' })
	);
	const fullFormat = $derived(
		new Intl.DateTimeFormat(getLocale(), { dateStyle: 'short', timeStyle: 'medium' })
	);
	const xTicks = $derived(
		series.length < 2
			? []
			: [start, start + (end - start) / 2, end].map((time) => ({ time, x: x(time) }))
	);

	const peak = $derived(Math.max(...series.map((p) => p.running_queries)));
	const latest = $derived(series[series.length - 1]?.running_queries ?? 0);
	const summary = $derived(
		m.monitoring_chart_summary({
			from: fullFormat.format(start),
			to: fullFormat.format(end),
			peak,
			latest
		})
	);

	function nearest(clientX: number, rect: DOMRect): number {
		const time = start + ((clientX - rect.left - PAD.left) / plotW) * (end - start);
		let best = 0;
		for (let i = 1; i < times.length; i++) {
			if (Math.abs(times[i] - time) < Math.abs(times[best] - time)) best = i;
		}
		return best;
	}

	function onPointer(event: PointerEvent) {
		const svg = event.currentTarget as SVGSVGElement;
		active = nearest(event.clientX, svg.getBoundingClientRect());
	}

	function onKey(event: KeyboardEvent) {
		if (!series.length) return;
		const last = series.length - 1;
		const moves: Record<string, number> = {
			ArrowRight: Math.min(last, (active ?? -1) + 1),
			ArrowLeft: Math.max(0, (active ?? last + 1) - 1),
			Home: 0,
			End: last
		};
		if (event.key in moves) {
			event.preventDefault();
			active = moves[event.key];
		} else if (event.key === 'Escape') {
			active = null;
		}
	}

	const valueText = $derived.by(() => {
		const index = active ?? series.length - 1;
		const point = series[index];
		if (!point) return '';
		return `${fullFormat.format(times[index])}, ${m.monitoring_running_count({ count: point.running_queries })}`;
	});

	const tip = $derived(
		active === null
			? null
			: {
					x: x(times[active]),
					y: y(series[active].running_queries),
					time: fullFormat.format(times[active]),
					value: series[active].running_queries
				}
	);
</script>

<div class="mt-4 rounded-lg border border-border bg-bg-panel p-3">
	<div class="flex items-center justify-between gap-2">
		<p class="text-[11px] font-semibold" id="queries-chart-title">
			{m.monitoring_running_queries()}
		</p>
		<button
			type="button"
			class="text-[11px] text-primary hover:underline"
			aria-expanded={tableOpen}
			aria-controls="queries-chart-table"
			onclick={() => (tableOpen = !tableOpen)}
		>
			{tableOpen ? m.monitoring_hide_table() : m.monitoring_show_as_table()}
		</button>
	</div>

	<div class="relative mt-2" bind:clientWidth={width}>
		<!-- A slider over the points (#482): arrows walk them and the value text
		     reads each one out; the summary and the table say the same in text. -->
		<!-- Sized inline: the global svg rule (app.css) is for 14px icons. -->
		<svg
			{width}
			height={HEIGHT}
			style="width: {width}px; height: {HEIGHT}px;"
			role="slider"
			aria-labelledby="queries-chart-title"
			aria-describedby="queries-chart-summary"
			aria-valuemin={0}
			aria-valuemax={Math.max(0, series.length - 1)}
			aria-valuenow={active ?? series.length - 1}
			aria-valuetext={valueText}
			tabindex="0"
			class="block outline-none focus-visible:ring-2 focus-visible:ring-primary"
			onpointermove={onPointer}
			onpointerleave={() => (active = null)}
			onkeydown={onKey}
			onblur={() => (active = null)}
		>
			{#each yTicks as tick (tick)}
				<line
					x1={PAD.left}
					x2={PAD.left + plotW}
					y1={y(tick)}
					y2={y(tick)}
					class="stroke-border"
					stroke-width="1"
				/>
				<text
					x={PAD.left - 6}
					y={y(tick)}
					text-anchor="end"
					dominant-baseline="middle"
					stroke="none"
					class="fill-text-muted text-[10px]">{tick}</text
				>
			{/each}
			{#each xTicks as tick, i (i)}
				<text
					x={tick.x}
					y={HEIGHT - 6}
					text-anchor={i === 0 ? 'start' : i === xTicks.length - 1 ? 'end' : 'middle'}
					stroke="none"
					class="fill-text-muted text-[10px]">{timeFormat.format(tick.time)}</text
				>
			{/each}
			<path d={path} fill="none" stroke="currentColor" stroke-width="2" class="text-primary" />
			{#if tip}
				<line
					x1={tip.x}
					x2={tip.x}
					y1={PAD.top}
					y2={PAD.top + plotH}
					class="stroke-text-muted"
					stroke-width="1"
					stroke-dasharray="3 3"
				/>
				<circle cx={tip.x} cy={tip.y} r="4" class="fill-primary stroke-bg-panel" stroke-width="2" />
			{/if}
		</svg>
		{#if tip}
			<div
				role="status"
				class="pointer-events-none absolute top-0 z-10 rounded-md border border-border bg-bg-panel px-2 py-1 text-[11px] shadow-md"
				style="left: {tip.x + 140 > width ? tip.x - 142 : tip.x + 12}px;"
			>
				<div class="text-text-muted">{tip.time}</div>
				<div class="font-semibold">{m.monitoring_running_count({ count: tip.value })}</div>
			</div>
		{/if}
	</div>
	<p id="queries-chart-summary" class="sr-only">{summary}</p>

	{#if tableOpen}
		<div id="queries-chart-table" class="mt-2 max-h-48 overflow-y-auto">
			<table class="w-full text-left text-[11px]">
				<caption class="sr-only">{m.monitoring_running_queries()}</caption>
				<thead class="text-[10px] uppercase text-slate-600 dark:text-slate-300">
					<tr>
						<th scope="col" class="px-2 py-1">{m.common_time()}</th>
						<th scope="col" class="px-2 py-1 text-right">{m.monitoring_running_queries()}</th>
					</tr>
				</thead>
				<tbody>
					{#each series as point, i (point.captured_at)}
						<tr class="border-t border-border/60">
							<td class="px-2 py-1">{fullFormat.format(times[i])}</td>
							<td class="px-2 py-1 text-right font-mono">{point.running_queries}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>
