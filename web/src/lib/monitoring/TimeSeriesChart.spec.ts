import { fireEvent, render, screen, within } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import TimeSeriesChart from './TimeSeriesChart.svelte';
import type { TimeSeries } from './series';

const base = Date.parse('2026-10-28T12:00:00Z');

function points(values: number[]) {
	return values.map((v, i) => ({ t: new Date(base + i * 60_000).toISOString(), v }));
}

const running: TimeSeries = { key: 'running', label: 'Running', points: points([3, 7, 17, 4]) };
const queued: TimeSeries = { key: 'queued', label: 'Queued', points: points([1, 2, 5, 2]) };

function renderChart(series: TimeSeries[]) {
	return render(TimeSeriesChart, {
		props: { series, title: 'Running queries', yLabel: 'Running', unit: 'queries' }
	});
}

// #820: the chart draws N series on one scale, with the same keyboard-readable
// points and table alternative as the single-series chart it replaces.
describe('TimeSeriesChart', () => {
	it('draws every series on one scale with a legend', () => {
		const { container } = renderChart([running, queued]);

		const ticks = [...container.querySelectorAll('text')].map((t) => t.textContent?.trim());
		expect(ticks).toEqual(expect.arrayContaining(['0', '9', '17']));
		expect(ticks).toContain('Running');

		const legend = screen.getByRole('list', { name: 'Series' });
		expect(within(legend).getByText('Running')).toBeTruthy();
		expect(within(legend).getByText('Queued')).toBeTruthy();
		expect(container.querySelectorAll('path[stroke]')).toHaveLength(2);
	});

	it('omits the legend for a single series', () => {
		renderChart([running]);
		expect(screen.queryByRole('list', { name: 'Series' })).toBeNull();
	});

	it('reads every series at the active point', async () => {
		renderChart([running, queued]);
		const chart = screen.getByRole('slider', { name: 'Running queries' });
		expect(chart.getAttribute('aria-valuetext')).toContain('Running 4 queries');
		expect(chart.getAttribute('aria-valuetext')).toContain('Queued 2 queries');

		await fireEvent.keyDown(chart, { key: 'Home' });
		expect(chart.getAttribute('aria-valuetext')).toContain('Running 3 queries');
		await fireEvent.keyDown(chart, { key: 'ArrowRight' });
		await fireEvent.keyDown(chart, { key: 'ArrowRight' });

		const tooltip = screen.getByRole('status');
		expect(tooltip.textContent).toContain('Running');
		expect(tooltip.textContent).toContain('17');
		expect(tooltip.textContent).toContain('Queued');
		expect(tooltip.textContent).toContain('5');

		await fireEvent.keyDown(chart, { key: 'Escape' });
		expect(screen.queryByRole('status')).toBeNull();
	});

	it('summarizes every series in text', () => {
		renderChart([running, queued]);
		expect(screen.getByText(/Running: 4 now, peak 17/)).toBeTruthy();
		expect(screen.getByText(/Queued: 2 now, peak 5/)).toBeTruthy();
	});

	it('lists the points as a table with one column per series', async () => {
		renderChart([running, queued]);
		await fireEvent.click(screen.getByRole('button', { name: 'Show as a table' }));

		const table = screen.getByRole('table');
		expect(
			within(table)
				.getAllByRole('columnheader')
				.map((th) => th.textContent?.trim())
		).toEqual(['Time', 'Running', 'Queued']);
		expect(within(table).getAllByRole('row')).toHaveLength(running.points.length + 1);
		expect(within(table).getByText('17')).toBeTruthy();
		expect(within(table).getByText('5')).toBeTruthy();
	});

	it('shows a gap where a series has no sample', async () => {
		const gappy: TimeSeries = {
			key: 'gappy',
			label: 'Gappy',
			points: [running.points[0], running.points[3]]
		};
		renderChart([running, gappy]);
		await fireEvent.click(screen.getByRole('button', { name: 'Show as a table' }));

		const table = screen.getByRole('table');
		expect(within(table).getAllByText('—')).toHaveLength(2);
	});
});
