// Shared shape and alignment for the Monitoring time-series chart (#820).
// Each series carries its own timestamps; the chart draws them on one axis,
// so the points are aligned to a single sorted timeline and downsampled to a
// bounded number of samples.

export type TimeSeriesPoint = { t: string; v: number };

export type TimeSeries = {
	key: string;
	label: string;
	points: TimeSeriesPoint[];
	color?: string;
};

/** The most points a chart draws per series; downsampling keeps the DOM small. */
export const MAX_CHART_POINTS = 200;

/**
 * The sorted union of every series' timestamps, evenly downsampled to
 * MAX_CHART_POINTS while keeping the first and the last sample.
 */
export function seriesTimeline(series: TimeSeries[]): number[] {
	const times = [
		...new Set(
			series.flatMap((line) =>
				line.points.map((point) => Date.parse(point.t)).filter(Number.isFinite)
			)
		)
	].sort((a, b) => a - b);
	if (times.length <= MAX_CHART_POINTS) return times;
	const step = (times.length - 1) / (MAX_CHART_POINTS - 1);
	return Array.from({ length: MAX_CHART_POINTS }, (_, i) => times[Math.round(i * step)]);
}

/** Each series' value at each timestamp; null where the series has no sample. */
export function alignSeries(series: TimeSeries[], times: number[]): (number | null)[][] {
	return series.map((line) => {
		const byTime = new Map(line.points.map((point) => [Date.parse(point.t), point.v] as const));
		return times.map((time) => byTime.get(time) ?? null);
	});
}
