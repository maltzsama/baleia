import { render } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import KindIcon from './KindIcon.svelte';
import { appearanceFor } from './kindIcon';

describe('KindIcon', () => {
	it('darkens the yellow brands for the light theme', () => {
		expect(appearanceFor('hive').lightHex).toBeTruthy();
		expect(appearanceFor('clickhouse').lightHex).toBeTruthy();
		expect(appearanceFor('trino').lightHex).toBeUndefined();
	});

	// #698: the Hive mark is bright yellow and disappears on the light theme. It
	// gets a darker shade of itself there, never a dark backdrop (#1035).
	it('draws a light mark in a darker shade, without a backdrop', () => {
		const { container } = render(KindIcon, { props: { kind: 'hive' } });
		expect(container.querySelector('span')?.className).not.toContain('bg-brand-dark');
		const svg = container.querySelector('span svg') as SVGElement;
		expect(svg.style.getPropertyValue('--mark')).toBe('#FDEE21');
		expect(svg.style.getPropertyValue('--mark-light')).not.toBe('#FDEE21');
	});

	it('leaves a contrasting brand mark on its own', () => {
		const { container } = render(KindIcon, { props: { kind: 'trino' } });
		const chip = container.querySelector('span')?.className ?? '';
		expect(chip).not.toContain('bg-brand-dark');
		expect(chip).not.toContain('dark:bg-white/90');
	});

	// #1035: a package mark is drawn inline in its brand colour.
	it('draws a simple-icons mark inline in its brand colour', () => {
		const { container } = render(KindIcon, { props: { kind: 'postgresql' } });
		const svg = container.querySelector('span svg');
		expect((svg as SVGElement | null)?.style.getPropertyValue('--mark')).toBe('#4169E1');
		expect(svg?.getAttribute('class')).toContain('kind-mark');
		expect(svg?.querySelector('path')?.getAttribute('d')).toBeTruthy();
		expect(container.querySelector('img')).toBeNull();
	});

	it('derives the chip from the brand colour', () => {
		// Dark marks need a light chip in dark mode, yellow ones a dark chip in light mode.
		expect(appearanceFor('sqlite').contrast).toBe(true);
		expect(appearanceFor('kafka').contrast).toBe(true);
		expect(appearanceFor('duckdb').lightHex).toBeTruthy();
		expect(appearanceFor('postgresql').contrast).toBeFalsy();
		expect(appearanceFor('postgresql').lightHex).toBeUndefined();
	});

	it('shares the Databricks mark between Databricks projects', () => {
		expect(appearanceFor('delta').mark).toEqual(appearanceFor('unity').mark);
		expect(appearanceFor('loki').mark).toBeDefined();
	});

	// #1035: a vendored official SVG, for a brand the package lacks.
	it('shows a vendored mark as an image and chips the dark ones', () => {
		const { container } = render(KindIcon, { props: { kind: 'pinot' } });
		expect(container.querySelector('span img')).not.toBeNull();
		expect(container.querySelector('span')?.className).toContain('dark:bg-white/90');
		expect(appearanceFor('exasol').contrast).toBe(true);
		expect(appearanceFor('redshift').brand).toBeTruthy();
	});

	it('keeps a line glyph for a kind without a brand', () => {
		for (const kind of ['memory', 'blackhole', 'jmx', 'tpch', 'tpcds', 'system', 'hudi']) {
			const { container, unmount } = render(KindIcon, { props: { kind } });
			expect(container.querySelector('img')).toBeNull();
			expect(container.querySelector('svg[stroke="currentColor"]')).not.toBeNull();
			unmount();
		}
	});
});
