import { fireEvent, render, screen, within } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import QueriesChart from './QueriesChart.svelte';

const base = Date.parse('2026-10-28T12:00:00Z');
const series = [3, 7, 17, 4].map((running_queries, i) => ({
	captured_at: new Date(base + i * 60_000).toISOString(),
	running_queries
}));

// #482: the chart has a count scale, a time window, keyboard-readable points
// and the same data as a table.
describe('QueriesChart', () => {
	it('labels the count axis and summarizes the window', () => {
		const { container } = render(QueriesChart, { props: { series } });
		const ticks = [...container.querySelectorAll('text')].map((t) => t.textContent?.trim());
		expect(ticks).toEqual(expect.arrayContaining(['0', '9', '17']));
		expect(ticks.length).toBeGreaterThanOrEqual(6);
		expect(screen.getByText(/peak of 17 running queries, 4 now/)).toBeTruthy();
	});

	it('reads each point with the arrow keys', async () => {
		render(QueriesChart, { props: { series } });
		const chart = screen.getByRole('slider', { name: 'Running queries' });
		expect(chart.getAttribute('aria-valuetext')).toContain('4 running');

		await fireEvent.keyDown(chart, { key: 'Home' });
		expect(chart.getAttribute('aria-valuetext')).toContain('3 running');
		await fireEvent.keyDown(chart, { key: 'ArrowRight' });
		await fireEvent.keyDown(chart, { key: 'ArrowRight' });
		expect(chart.getAttribute('aria-valuetext')).toContain('17 running');
		expect(screen.getByRole('status').textContent).toContain('17 running');

		await fireEvent.keyDown(chart, { key: 'Escape' });
		expect(screen.queryByRole('status')).toBeNull();
	});

	it('shows the points as a table', async () => {
		render(QueriesChart, { props: { series } });
		const toggle = screen.getByRole('button', { name: 'Show as a table' });
		await fireEvent.click(toggle);
		expect(toggle.getAttribute('aria-expanded')).toBe('true');
		const table = screen.getByRole('table');
		expect(within(table).getAllByRole('row')).toHaveLength(series.length + 1);
		expect(within(table).getByText('17')).toBeTruthy();
	});
});
