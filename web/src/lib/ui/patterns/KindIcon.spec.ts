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

	it('adapts a brand colour that is hard to see on a theme', () => {
		// Too dark for the dark theme: lightened there, never given a backdrop.
		for (const kind of ['sqlite', 'kafka', 'mysql', 'postgresql']) {
			expect(appearanceFor(kind).darkHex, kind).toBeTruthy();
			expect(appearanceFor(kind).contrast, kind).toBeFalsy();
		}
		// Too light for the light theme: darkened there.
		for (const kind of ['hive', 'duckdb', 'clickhouse', 'druid']) {
			expect(appearanceFor(kind).lightHex, kind).toBeTruthy();
		}
		// Visible on both: left as the brand has it.
		expect(appearanceFor('redis').lightHex).toBeUndefined();
		expect(appearanceFor('redis').darkHex).toBeUndefined();
	});

	it('holds contrast: dark shades on white, light shades on the dark theme', () => {
		const lum = (hex: string) => {
			const [r, g, b] = [1, 3, 5].map((i) => {
				const c = parseInt(hex.slice(i, i + 2), 16) / 255;
				return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
			});
			return 0.2126 * r + 0.7152 * g + 0.0722 * b;
		};
		expect(lum('#' + appearanceFor('hive').lightHex!)).toBeLessThanOrEqual(0.12);
		expect(lum('#' + appearanceFor('mysql').darkHex!)).toBeGreaterThanOrEqual(0.35);
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
