import { describe, expect, it } from 'vitest';
import { MAX_CHART_POINTS, alignSeries, seriesTimeline, type TimeSeries } from './series';

const base = Date.parse('2026-10-28T12:00:00Z');

function points(values: number[], step = 60_000) {
	return values.map((v, i) => ({ t: new Date(base + i * step).toISOString(), v }));
}

describe('seriesTimeline', () => {
	it('merges the timestamps of every series, sorted', () => {
		const series: TimeSeries[] = [
			{ key: 'a', label: 'A', points: points([1, 2], 120_000) },
			{
				key: 'b',
				label: 'B',
				points: [{ t: new Date(base + 60_000).toISOString(), v: 9 }]
			}
		];
		expect(seriesTimeline(series)).toEqual([base, base + 60_000, base + 120_000]);
	});

	it('downsamples a long window to the cap, keeping the first and last sample', () => {
		const long = points(Array.from({ length: 500 }, (_, i) => i));
		const times = seriesTimeline([{ key: 'a', label: 'A', points: long }]);
		const first = long[0]!;
		const last = long[long.length - 1]!;
		expect(times).toHaveLength(MAX_CHART_POINTS);
		expect(times[0]).toBe(Date.parse(first.t));
		expect(times.at(-1)).toBe(Date.parse(last.t));
	});
});

describe('alignSeries', () => {
	it('lines a series up with the timeline and leaves gaps as null', () => {
		const times = [base, base + 60_000, base + 120_000];
		const values = alignSeries(
			[
				{
					key: 'a',
					label: 'A',
					points: [
						{ t: new Date(base).toISOString(), v: 3 },
						{ t: new Date(base + 120_000).toISOString(), v: 7 }
					]
				}
			],
			times
		);
		expect(values).toEqual([[3, null, 7]]);
	});
});
