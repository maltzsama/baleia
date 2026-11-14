<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { getLocale } from '$lib/paraglide/runtime';
	import { alignSeries, seriesTimeline, type TimeSeries } from './series';

	// A multi-series chart (#820): one line per series on the same axis, a
	// crosshair and tooltip that follow the pointer or the arrow keys, and the
	// same points as a table. The value text and the table read every series
	// out, so the chart stays usable without sight.
	let {
		series,
		title,
		yLabel,
		unit = ''
	}: {
		series: TimeSeries[];
		title: string;
		yLabel?: string;
		unit?: string;
	} = $props();

	const HEIGHT = 150;
	const PAD = { top: 18, right: 12, bottom: 22, left: 34 };
	// Readable on both themes and distinct from each other.
	const COLORS = ['#2563eb', '#047857', '#b45309', '#dc2626', '#7c3aed', '#0f766e'];

	const uid = $props.id();
	const titleId = `chart-title-${uid}`;
	const summaryId = `chart-summary-${uid}`;
	const tableId = `chart-table-${uid}`;

	let width = $state(600);
	let active = $state<number | null>(null);
	let tableOpen = $state(false);

	const lines = $derived(
		series.map((line, i) => ({ ...line, color: line.color ?? COLORS[i % COLORS.length] }))
	);
	const times = $derived(seriesTimeline(series));
	const values = $derived(alignSeries(series, times));
	const last = $derived(times.length - 1);

	const start = $derived(times[0] ?? 0);
	const end = $derived(times[last] ?? 1);
	// A whole-number top tick over every series, so the axis reads in units.
	const top = $derived(
		Math.max(1, Math.ceil(Math.max(0, ...values.flat().filter((v) => v !== null))))
	);
	const yTicks = $derived(top <= 2 ? [0, top] : [0, Math.round(top / 2), top]);

	const plotW = $derived(Math.max(1, width - PAD.left - PAD.right));
	const plotH = HEIGHT - PAD.top - PAD.bottom;
	const x = (time: number) =>
		PAD.left + (end === start ? 0 : ((time - start) / (end - start)) * plotW);
	const y = (value: number) => PAD.top + plotH - (value / top) * plotH;

	// A line breaks at a gap instead of joining across it.
	const paths = $derived(
		values.map((line) => {
			let d = '';
			let open = false;
			line.forEach((value, i) => {
				if (value === null) {
					open = false;
					return;
				}
				d += `${open ? 'L' : 'M'}${x(times[i]).toFixed(1)},${y(value).toFixed(1)}`;
				open = true;
			});
			return d;
		})
	);

	const timeFormat = $derived(
		new Intl.DateTimeFormat(getLocale(), { hour: '2-digit', minute: '2-digit' })
	);
	const fullFormat = $derived(
		new Intl.DateTimeFormat(getLocale(), { dateStyle: 'short', timeStyle: 'medium' })
	);
	const xTicks = $derived(
		times.length < 2
			? []
			: [start, start + (end - start) / 2, end].map((time) => ({ time, x: x(time) }))
	);

	function at(index: number): { label: string; value: number; color: string }[] {
		return lines.flatMap((line, i) => {
			const value = values[i]?.[index];
			return value === null || value === undefined
				? []
				: [{ label: line.label, value, color: line.color }];
		});
	}

	const withUnit = (value: number) => (unit ? `${value} ${unit}` : `${value}`);

	function readout(index: number): string {
		return m.monitoring_chart_point({
			time: fullFormat.format(times[index]),
			values: at(index)
				.map((item) => `${item.label} ${withUnit(item.value)}`)
				.join(', ')
		});
	}

	const summary = $derived.by(() => {
		const stats = lines.map((line, i) => {
			const samples = values[i].filter((v): v is number => v !== null);
			const latest = samples[samples.length - 1] ?? 0;
			const peak = samples.length ? Math.max(...samples) : 0;
			return m.monitoring_chart_series_stats({ label: line.label, latest, peak });
		});
		return `${m.monitoring_chart_from_to({
			from: fullFormat.format(start),
			to: fullFormat.format(end)
		})} ${stats.join(' ')}`;
	});

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
		if (!times.length) return;
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

	const valueText = $derived(times.length ? readout(active ?? last) : '');
	const tip = $derived(
		active === null
			? null
			: {
					x: x(times[active]),
					time: fullFormat.format(times[active]),
					items: at(active)
				}
	);
</script>

<div class="mt-4 rounded-lg border border-border bg-bg-panel p-3">
	<div class="flex items-center justify-between gap-2">
		<p class="text-[11px] font-semibold" id={titleId}>{title}</p>
		<button
			type="button"
			class="text-[11px] text-primary hover:underline"
			aria-expanded={tableOpen}
			aria-controls={tableId}
			onclick={() => (tableOpen = !tableOpen)}
		>
			{tableOpen ? m.monitoring_hide_table() : m.monitoring_show_as_table()}
		</button>
	</div>

	{#if lines.length > 1}
		<ul class="mt-2 flex flex-wrap gap-x-3 gap-y-1" aria-label={m.monitoring_chart_legend()}>
			{#each lines as line (line.key)}
				<li class="flex items-center gap-1.5 text-[11px] text-text-muted">
					<span
						class="h-2 w-2 shrink-0 rounded-full"
						style="background: {line.color}"
						aria-hidden="true"
					></span>
					{line.label}
				</li>
			{/each}
		</ul>
	{/if}

	{#if times.length > 0}
		<div class="relative mt-2" bind:clientWidth={width}>
			<!-- A slider over the points: arrows walk them and the value text
			     reads each one out; the summary and the table say the same in text. -->
			<!-- Sized inline: the global svg rule (app.css) is for 14px icons. -->
			<svg
				{width}
				height={HEIGHT}
				style="width: {width}px; height: {HEIGHT}px;"
				role="slider"
				aria-labelledby={titleId}
				aria-describedby={summaryId}
				aria-valuemin={0}
				aria-valuemax={Math.max(0, last)}
				aria-valuenow={active ?? last}
				aria-valuetext={valueText}
				tabindex="0"
				class="block outline-none focus-visible:ring-2 focus-visible:ring-primary"
				onpointermove={onPointer}
				onpointerleave={() => (active = null)}
				onkeydown={onKey}
				onblur={() => (active = null)}
			>
				{#if yLabel}
					<text x={PAD.left} y={PAD.top - 8} stroke="none" class="fill-text-muted text-[10px]"
						>{yLabel}</text
					>
				{/if}
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
				{#each paths as path, i (lines[i].key)}
					<path
						d={path}
						fill="none"
						stroke={lines[i].color}
						stroke-width="2"
						stroke-linejoin="round"
					/>
				{/each}
				{#if times.length === 1}
					<!-- One sample has no line to draw: show it as a dot. -->
					{#each values as line, i (lines[i].key)}
						{#if line[0] !== null}
							<circle cx={x(times[0])} cy={y(line[0])} r="3" fill={lines[i].color} />
						{/if}
					{/each}
				{/if}
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
					{#each tip.items as item (item.label)}
						<circle
							cx={tip.x}
							cy={y(item.value)}
							r="4"
							fill={item.color}
							class="stroke-bg-panel"
							stroke-width="2"
						/>
					{/each}
				{/if}
			</svg>
			{#if tip}
				<div
					role="status"
					class="pointer-events-none absolute top-0 z-10 rounded-md border border-border bg-bg-panel px-2 py-1 text-[11px] shadow-md"
					style="left: {tip.x + 140 > width ? tip.x - 142 : tip.x + 12}px;"
				>
					<div class="text-text-muted">{tip.time}</div>
					{#each tip.items as item (item.label)}
						<div class="flex items-center gap-1.5">
							<span
								class="h-2 w-2 shrink-0 rounded-full"
								style="background: {item.color}"
								aria-hidden="true"
							></span>
							<span class="text-text-muted">{item.label}</span>
							<span class="font-semibold">{withUnit(item.value)}</span>
						</div>
					{/each}
				</div>
			{/if}
		</div>
		<p id={summaryId} class="sr-only">{summary}</p>
	{:else}
		<!-- No snapshot in the window: keep the frame so the section does not
		     vanish, with its axis at zero and a note saying why it is empty. -->
		<div class="relative mt-2" bind:clientWidth={width}>
			<svg
				{width}
				height={HEIGHT}
				style="width: {width}px; height: {HEIGHT}px;"
				role="img"
				aria-labelledby={titleId}
				aria-describedby={summaryId}
				class="block"
			>
				{#if yLabel}
					<text x={PAD.left} y={PAD.top - 8} stroke="none" class="fill-text-muted text-[10px]"
						>{yLabel}</text
					>
				{/if}
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
				<text
					x={PAD.left + plotW / 2}
					y={PAD.top + plotH / 2}
					text-anchor="middle"
					dominant-baseline="middle"
					stroke="none"
					class="fill-text-muted text-[11px]">{m.monitoring_chart_no_data()}</text
				>
			</svg>
		</div>
		<p id={summaryId} class="sr-only">{m.monitoring_chart_no_data()}</p>
	{/if}

	{#if tableOpen && times.length > 0}
		<div id={tableId} class="mt-2 max-h-48 overflow-y-auto">
			<table class="w-full text-left text-[11px]">
				<caption class="sr-only">{title}</caption>
				<thead class="text-[10px] uppercase text-slate-600 dark:text-slate-300">
					<tr>
						<th scope="col" class="px-2 py-1">{m.common_time()}</th>
						{#each lines as line (line.key)}
							<th scope="col" class="px-2 py-1 text-right">{line.label}</th>
						{/each}
					</tr>
				</thead>
				<tbody>
					{#each times as time, i (time)}
						<tr class="border-t border-border/60">
							<td class="px-2 py-1">{fullFormat.format(time)}</td>
							{#each lines as line, s (line.key)}
								<td class="px-2 py-1 text-right font-mono">
									{values[s][i] === null ? '—' : values[s][i]}
								</td>
							{/each}
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>
