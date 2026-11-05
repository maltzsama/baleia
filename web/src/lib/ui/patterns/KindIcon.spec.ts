import { render } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import KindIcon from './KindIcon.svelte';
import { appearanceFor } from './kindIcon';

describe('KindIcon', () => {
	it('marks the yellow brands as light marks', () => {
		expect(appearanceFor('hive').lightMark).toBe(true);
		expect(appearanceFor('clickhouse').lightMark).toBe(true);
		expect(appearanceFor('trino').lightMark).toBeFalsy();
	});

	// #698: the Hive mark is bright yellow and disappears on the light theme.
	it('gives a light mark a dark chip in light mode', () => {
		const { container } = render(KindIcon, { props: { kind: 'hive' } });
		expect(container.querySelector('span')?.className).toContain('bg-brand-dark');
	});

	it('leaves a contrasting brand mark on its own', () => {
		const { container } = render(KindIcon, { props: { kind: 'trino' } });
		const chip = container.querySelector('span')?.className ?? '';
		expect(chip).not.toContain('bg-brand-dark');
		expect(chip).not.toContain('dark:bg-white/90');
	});
});
