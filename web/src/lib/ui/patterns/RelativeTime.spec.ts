import { render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import RelativeTime from './RelativeTime.svelte';

const T0 = new Date('2026-01-01T12:00:00Z');

describe('RelativeTime', () => {
	afterEach(() => {
		vi.useRealTimers();
	});

	// #695: the system status page sets "Checked" to now on every refresh. A
	// frozen reference clock turned that into "in N minutes" once the page had
	// been open for a while. A live timestamp must track the clock instead.
	it('ages a live timestamp and never reads as future after a refresh', async () => {
		vi.useFakeTimers();
		vi.setSystemTime(T0);
		const { rerender } = render(RelativeTime, { props: { at: T0, live: true } });
		expect(screen.getByText('just now')).toBeTruthy();

		await vi.advanceTimersByTimeAsync(42 * 60_000);
		expect(screen.getByText('42 minutes ago')).toBeTruthy();

		await rerender({ at: new Date() });
		expect(screen.getByText('just now')).toBeTruthy();
	});
});
