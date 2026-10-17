// Typed reducer for the execution event stream (#111). `snapshot` replaces the
// state entirely; events with a `seq` already applied are ignored.
import type { components } from '$lib/api/schema';
import type { ExecutionEvent } from '$lib/results/stream';

type Execution = components['schemas']['Execution'];
type QueryStatement = components['schemas']['QueryStatement'];
export type StatementStatus = QueryStatement['status'];

export type StatementState = {
	position: number;
	sql: string;
	status: StatementStatus;
	errorCode?: string | null;
	errorMessage?: string | null;
	rowsReturned?: number | null;
	resultCount: number;
	truncated: boolean;
	durationMs?: number | null;
	startedAt?: string | null;
	finishedAt?: string | null;
};

export type RunState = {
	executionId: string;
	status: string;
	errorCode?: string | null;
	readOnly: boolean;
	target?: Execution['target'];
	statements: Record<number, StatementState>;
	lastSeq: number;
};

function emptyStatement(position: number): StatementState {
	return {
		position,
		sql: '',
		status: 'pending',
		resultCount: 0,
		truncated: false
	};
}

function fromStatement(statement: QueryStatement): StatementState {
	return {
		position: statement.position,
		sql: statement.sql,
		status: statement.status,
		errorCode: statement.error_code,
		errorMessage: statement.error_message,
		rowsReturned: statement.rows_returned,
		resultCount: statement.result_count ?? 0,
		truncated: statement.truncated ?? false,
		durationMs: statement.duration_ms,
		startedAt: statement.started_at,
		finishedAt: statement.finished_at
	};
}

export function initialRunState(executionId: string): RunState {
	return {
		executionId,
		status: 'running',
		readOnly: false,
		statements: {},
		lastSeq: 0
	};
}

export function applyEvent(state: RunState, event: ExecutionEvent): RunState {
	if (event.seq <= state.lastSeq) return state;

	switch (event.type) {
		case 'snapshot': {
			const statements: Record<number, StatementState> = {};
			for (const statement of event.statements) {
				statements[statement.position] = fromStatement(statement);
			}
			return {
				executionId: event.execution_id,
				status: event.execution.status,
				errorCode: event.execution.error_code ?? null,
				readOnly: event.execution.read_only ?? false,
				target: event.execution.target,
				statements,
				lastSeq: event.seq
			};
		}
		case 'execution_status':
			return {
				...state,
				status: event.status,
				errorCode: event.error_code ?? state.errorCode,
				lastSeq: event.seq
			};
		case 'statement_status': {
			const current = state.statements[event.position] ?? emptyStatement(event.position);
			return {
				...state,
				lastSeq: event.seq,
				statements: {
					...state.statements,
					[event.position]: {
						...current,
						status: event.status,
						errorCode: event.error_code ?? current.errorCode,
						errorMessage: event.error_message ?? current.errorMessage,
						rowsReturned: event.rows_returned ?? current.rowsReturned,
						resultCount: event.result_count ?? current.resultCount,
						truncated: event.truncated ?? current.truncated,
						durationMs: event.duration_ms ?? current.durationMs
					}
				}
			};
		}
		case 'statement_progress': {
			const current = state.statements[event.position] ?? emptyStatement(event.position);
			return {
				...state,
				lastSeq: event.seq,
				statements: {
					...state.statements,
					[event.position]: { ...current, rowsReturned: event.rows_so_far }
				}
			};
		}
		default:
			return state;
	}
}

export function orderedStatements(state: RunState): StatementState[] {
	return Object.values(state.statements).sort((a, b) => a.position - b.position);
}
