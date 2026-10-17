import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import type { RunState } from './reducer';
import RunPanel from './RunPanel.svelte';

function state(overrides: Partial<RunState> = {}): RunState {
	return {
		executionId: 'e1',
		status: 'failed',
		readOnly: false,
		lastSeq: 5,
		statements: {
			1: {
				position: 1,
				sql: 'SELECT 1',
				status: 'finished',
				rowsReturned: 1,
				resultCount: 0,
				truncated: false,
				durationMs: 5
			},
			2: {
				position: 2,
				sql: 'SELECT * FROM missing',
				status: 'failed',
				errorCode: 'not_found',
				errorMessage: 'table missing',
				resultCount: 0,
				truncated: false
			},
			3: {
				position: 3,
				sql: 'SELECT 3',
				status: 'skipped',
				resultCount: 0,
				truncated: false
			}
		},
		...overrides
	};
}

describe('RunPanel', () => {
	it('explains a failed statement and the skipped tail, and re-runs from a position', async () => {
		const onrerun = vi.fn();
		render(RunPanel, {
			props: {
				state: state(),
				executing: false,
				canCancel: true,
				oncancel: vi.fn(),
				onrerun
			}
		});

		expect(screen.getByText(/Statement 2 failed/)).toBeTruthy();
		expect(screen.getByText(/Statements 3–3 were not run/)).toBeTruthy();
		expect(screen.getByText('Skipped')).toBeTruthy();

		await fireEvent.click(screen.getByRole('button', { name: 'Re-run from statement 3' }));
		expect(onrerun).toHaveBeenCalledWith(3);
	});

	it('offers both safe re-run actions for an interrupted statement', async () => {
		const onrerun = vi.fn();
		render(RunPanel, {
			props: {
				state: state({
					statements: {
						1: {
							position: 1,
							sql: 'SELECT 1',
							status: 'failed',
							errorCode: 'interrupted',
							resultCount: 0,
							truncated: false
						}
					}
				}),
				executing: false,
				canCancel: true,
				oncancel: vi.fn(),
				onrerun
			}
		});

		expect(screen.getByText(/The run stopped because the Baleia worker stopped/)).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Re-run from statement 2' }));
		expect(onrerun).toHaveBeenCalledWith(2);
	});

	it('shows the read-only rejection banner', () => {
		render(RunPanel, {
			props: {
				state: state({
					readOnly: true,
					target: { catalog_name: 'lake' },
					statements: {
						1: {
							position: 1,
							sql: 'DELETE FROM t',
							status: 'failed',
							errorCode: 'permission_denied',
							resultCount: 0,
							truncated: false
						}
					}
				}),
				executing: false,
				canCancel: true,
				oncancel: vi.fn(),
				onrerun: vi.fn()
			}
		});

		expect(screen.getByText(/workspace has read access to lake/)).toBeTruthy();
	});
});
