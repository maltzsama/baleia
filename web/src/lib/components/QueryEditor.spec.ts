import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { editor } from '$lib/stores/editor.svelte';
import { clearEnginesCache } from '$lib/engines/client';
import { clearMetadataCache } from '$lib/metadata/client';
import { http, server } from '../../test/server';
import { makeSavedQuery, makeSpace, makeWorkspace } from '../../test/factories';
import QueryEditor from './QueryEditor.svelte';

const WS = '00000000-0000-0000-0000-000000000001';
const BINDING = '00000000-0000-0000-0000-0000000000b1';
const ENGINE = '00000000-0000-0000-0000-000000000004';
const CATALOG = '00000000-0000-0000-0000-0000000000c1';
const EXECUTION = '00000000-0000-0000-0000-0000000000e1';

class FakeWebSocket {
	onmessage: ((event: MessageEvent) => void) | null = null;
	onerror: (() => void) | null = null;
	constructor(readonly url: string) {}
	close() {}
}

function mockWorkbench() {
	server.use(
		http.get('/workspaces', ({ response }) =>
			response(200).json([
				makeWorkspace({ id: WS, default_binding_id: BINDING, default_schema: 'sales' })
			])
		),
		http.get('/workspaces/{id}/targets', ({ response }) =>
			response(200).json([
				{
					binding_id: BINDING,
					catalog_id: CATALOG,
					catalog_name: 'lake',
					engine_id: ENGINE,
					engine_name: 'trino',
					engine_kind: 'trino',
					engine_catalog_name: 'lake_t',
					mode: 'adopted',
					permission: 'write',
					is_default: true
				}
			])
		),
		http.get('/engines', ({ response }) =>
			response(200).json([
				{
					id: ENGINE,
					name: 'trino',
					kind: 'trino',
					class: 'federated',
					connection_id: '00000000-0000-0000-0000-000000000005',
					identity_mode: 'connection',
					enabled: true,
					created_at: '2026-01-01T00:00:00Z',
					version: '483',
					allowed_actions: ['view'],
					dialect: { name: 'trino', formatter_language: 'trino', codemirror_base: 'StandardSQL' },
					capabilities: {
						dialect: 'trino',
						identity_modes: ['connection'],
						read_only: 'enforced',
						supports_cancel: true,
						cancel_scope: 'any_process',
						transactions: false,
						multiple_result_sets: false,
						multiple_catalogs: true,
						namespace_depth: 1,
						session_schema: true,
						supports_explain: true,
						supports_time_travel: false,
						supports_catalog_ddl: false,
						monitoring: true,
						iceberg_maintenance: false,
						arrow_native: true,
						max_identifier_length: 0,
						identifier_case: 'preserve'
					}
				}
			])
		),
		http.get('/dialects/{name}', ({ response }) =>
			response(200).json({
				name: 'trino',
				formatter_language: 'trino',
				codemirror_base: 'StandardSQL',
				keywords: ['SELECT', 'FROM'],
				functions: ['count']
			})
		),
		http.get('/workspaces/{id}/metadata/catalogs/{catalog_id}/namespaces', ({ response }) =>
			response(200).json({
				freshness: { metadata_synced_at: null },
				items: [{ path: ['sales'] }]
			})
		),
		http.post('/ws-tickets', ({ response }) =>
			response(201).json({ ticket: 'ticket-1', expires_at: '2030-01-01T00:00:00Z' })
		)
	);
}

describe('QueryEditor', () => {
	beforeEach(() => {
		clearEnginesCache();
		clearMetadataCache();
		localStorage.clear();
		editor.resetTabs();
		vi.stubGlobal('WebSocket', FakeWebSocket);
	});

	it('selects the workspace default target and shows the engine status', async () => {
		mockWorkbench();
		render(QueryEditor);

		expect(await screen.findByText('trino · trino')).toBeTruthy();
		expect(screen.getByText('483')).toBeTruthy();

		const engine = screen.getByLabelText('Engine') as HTMLSelectElement;
		const catalog = screen.getByLabelText('Catalog') as HTMLSelectElement;
		await waitFor(() => expect(engine.value).toBe(ENGINE));
		await waitFor(() => expect(catalog.value).toBe(BINDING));

		const schema = screen.getByLabelText('Schema') as HTMLSelectElement;
		await waitFor(() => expect(schema.value).toBe('sales'));
		expect(screen.getByText('Ln 1, Col 1')).toBeTruthy();
	});

	it('runs with the selected target and schema', async () => {
		mockWorkbench();
		type RunBody = { target?: unknown };
		const captured: { body?: RunBody } = {};
		server.use(
			http.post('/workspaces/{id}/executions', async ({ request, response }) => {
				captured.body = (await request.json()) as RunBody;
				return response(202).json({
					id: EXECUTION,
					workspace_id: WS,
					status: 'running',
					sql: 'SELECT 1;',
					started_at: '2026-01-01T00:00:00Z',
					target: {
						engine_id: ENGINE,
						binding_id: BINDING,
						catalog_id: CATALOG,
						engine_kind: 'trino',
						engine_name: 'trino',
						catalog_name: 'lake',
						engine_catalog: 'lake_t',
						schema: 'sales',
						read_only: false
					}
				});
			})
		);
		render(QueryEditor);
		await screen.findByText('trino · trino');

		await fireEvent.click(screen.getByTestId('run-all'));

		await waitFor(() => expect(captured.body).toBeDefined());
		expect(captured.body?.target).toEqual({ binding_id: BINDING, schema: 'sales' });
	});

	// #395: Run is the statement at the cursor (or the selection), Run all the
	// whole script, as ⌘↵ and ⇧⌘↵.
	function captureRuns(sent: string[]) {
		server.use(
			http.post('/dialects/{name}/split', ({ response }) =>
				response(200).json([
					{ start: 0, end: 9 },
					{ start: 10, end: 18 }
				])
			),
			http.post('/workspaces/{id}/executions', async ({ request, response }) => {
				const body = (await request.json()) as { sql: string };
				sent.push(body.sql);
				return response(202).json({
					id: EXECUTION,
					workspace_id: WS,
					status: 'running',
					sql: body.sql,
					started_at: '2026-01-01T00:00:00Z'
				});
			})
		);
	}

	it('runs only the statement at the cursor with Run', async () => {
		mockWorkbench();
		const sent: string[] = [];
		captureRuns(sent);
		editor.updateSql(editor.activeId, 'SELECT 1;\nSELECT 2');
		render(QueryEditor);
		await screen.findByText('trino · trino');

		// The cursor starts at the top: the first statement.
		await fireEvent.click(screen.getByTestId('run-current'));
		await waitFor(() => expect(sent).toEqual(['SELECT 1;']));
	});

	// CodeRabbit on #396: Run must not fall back to the whole script.
	it('runs one statement with Run even when the engine splitter fails', async () => {
		mockWorkbench();
		const sent: string[] = [];
		captureRuns(sent);
		server.use(
			// The splitter refuses the script (unterminated string, …).
			http.post('/dialects/{name}/split', ({ response }) =>
				response(422).json({ code: 'validation_error', message: 'cannot split', errors: [] })
			)
		);
		editor.updateSql(editor.activeId, 'SELECT 1;\nSELECT 2');
		render(QueryEditor);
		await screen.findByText('trino · trino');

		await fireEvent.click(screen.getByTestId('run-current'));
		await waitFor(() => expect(sent).toEqual(['SELECT 1;']));
	});

	it('runs the whole script with Run all', async () => {
		mockWorkbench();
		const sent: string[] = [];
		captureRuns(sent);
		editor.updateSql(editor.activeId, 'SELECT 1;\nSELECT 2');
		render(QueryEditor);
		await screen.findByText('trino · trino');

		await fireEvent.click(screen.getByTestId('run-all'));
		await waitFor(() => expect(sent).toEqual(['SELECT 1;\nSELECT 2']));
	});

	it('keeps the target on the tab', async () => {
		mockWorkbench();
		render(QueryEditor);
		await screen.findByText('trino · trino');

		const tab = editor.activeTab;
		expect(tab.bindingId).toBe(BINDING);
		expect(tab.schema).toBe('sales');
	});

	function mockSavedQueries() {
		server.use(
			http.get('/workspaces/{id}/spaces', ({ response }) =>
				response(200).json([makeSpace({ id: 'sp1', my_role: 'manager' })])
			),
			http.get('/workspaces/{id}/queries', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/folders', ({ response }) => response(200).json([]))
		);
	}

	// A second, dirty tab on a saved query, opened in the background.
	function dirtySavedTab() {
		const first = editor.activeId;
		const tab = editor.openSavedQuery(
			makeSavedQuery({ id: 'q1', name: 'Revenue', version: 1, sql: 'SELECT 1', space_id: 'sp1' })
		);
		editor.updateSql(tab.id, 'SELECT 42');
		editor.activate(first);
		return tab.id;
	}

	// The modal is found by its heading: its <dialog> carries no name.
	async function dialogTitled(title: string) {
		const heading = await screen.findByRole('heading', { name: title });
		return heading.closest('dialog') as HTMLElement;
	}

	async function closeWithSave(tabId: string) {
		const idx = editor.tabs.findIndex((tab) => tab.id === tabId);
		await fireEvent.click(screen.getAllByLabelText('Close tab')[idx]);
		const closeDialog = await dialogTitled('Close this tab?');
		await fireEvent.click(within(closeDialog).getByRole('button', { name: 'Save' }));
		return dialogTitled('Save query');
	}

	// #411: Save on the close dialog saves the tab being closed, then closes it.
	it('saves a dirty tab before closing it', async () => {
		mockWorkbench();
		mockSavedQueries();
		const patched: { sql?: string } = {};
		server.use(
			http.patch('/workspaces/{id}/queries/{query_id}', async ({ request, response }) => {
				patched.sql = ((await request.json()) as { sql: string }).sql;
				return response(200).json(
					makeSavedQuery({ id: 'q1', name: 'Revenue', version: 2, sql: 'SELECT 42' })
				);
			})
		);
		const tabId = dirtySavedTab();
		render(QueryEditor);
		await screen.findByText('trino · trino');

		const saveDialog = await closeWithSave(tabId);
		// The tab stays open, and active, until it is saved.
		expect(editor.activeId).toBe(tabId);
		await fireEvent.click(within(saveDialog).getByRole('button', { name: 'Save' }));

		await waitFor(() => expect(editor.tabs.some((tab) => tab.id === tabId)).toBe(false));
		expect(patched.sql).toBe('SELECT 42');
	});

	it('keeps the tab and its SQL when the save is cancelled', async () => {
		mockWorkbench();
		mockSavedQueries();
		const tabId = dirtySavedTab();
		render(QueryEditor);
		await screen.findByText('trino · trino');

		const saveDialog = await closeWithSave(tabId);
		await fireEvent.click(within(saveDialog).getByRole('button', { name: 'Cancel' }));

		await waitFor(() => expect(screen.queryByRole('heading', { name: 'Save query' })).toBeNull());
		const tab = editor.tabs.find((item) => item.id === tabId);
		expect(tab?.sql).toBe('SELECT 42');
		expect(tab?.dirty).toBe(true);
	});

	// #413: a run belongs to its tab.
	it('keeps a run in its own tab', async () => {
		mockWorkbench();
		const sent: string[] = [];
		captureRuns(sent);
		editor.updateSql(editor.activeId, 'SELECT 1');
		const first = editor.activeId;
		render(QueryEditor);
		await screen.findByText('trino · trino');

		await fireEvent.click(screen.getByTestId('run-current'));
		await waitFor(() => expect(sent).toHaveLength(1));
		await waitFor(() =>
			expect((screen.getByTestId('run-current') as HTMLButtonElement).disabled).toBe(true)
		);
		expect(screen.getByTestId('results-area')).toBeTruthy();

		await fireEvent.click(screen.getByText('+'));
		// The new tab can run, and shows no run of the other tab.
		await waitFor(() =>
			expect((screen.getByTestId('run-current') as HTMLButtonElement).disabled).toBe(false)
		);
		expect(screen.queryByTestId('results-area')).toBeNull();

		const idx = editor.tabs.findIndex((tab) => tab.id === first);
		await fireEvent.click(screen.getAllByText(editor.tabs[idx].name)[0]);
		await waitFor(() => expect(screen.getByTestId('results-area')).toBeTruthy());
		expect((screen.getByTestId('run-current') as HTMLButtonElement).disabled).toBe(true);
	});

	// CodeRabbit on #440: a tab closed while its run starts gets no stream.
	it('opens no stream for a tab closed while its run starts', async () => {
		mockWorkbench();
		let release = () => {};
		const gate = new Promise<void>((resolve) => (release = resolve));
		const seen = { started: false, tickets: 0 };
		server.use(
			http.post('/dialects/{name}/split', ({ response }) =>
				response(200).json([{ start: 0, end: 8 }])
			),
			http.post('/workspaces/{id}/executions', async ({ response }) => {
				seen.started = true;
				await gate;
				return response(202).json({
					id: EXECUTION,
					workspace_id: WS,
					status: 'running',
					sql: 'SELECT 1',
					started_at: '2026-01-01T00:00:00Z'
				});
			}),
			http.post('/ws-tickets', ({ response }) => {
				seen.tickets++;
				return response(201).json({ ticket: 'ticket-1', expires_at: '2030-01-01T00:00:00Z' });
			})
		);
		const tab = editor.addTab();
		editor.updateSql(tab.id, 'SELECT 1');
		render(QueryEditor);
		await screen.findByText('trino · trino');

		await fireEvent.click(screen.getByTestId('run-current'));
		await waitFor(() => expect(seen.started).toBe(true));
		const idx = editor.tabs.findIndex((item) => item.id === tab.id);
		await fireEvent.click(screen.getAllByLabelText('Close tab')[idx]);
		const closeDialog = await dialogTitled('Close this tab?');
		await fireEvent.click(within(closeDialog).getByRole('button', { name: 'Discard' }));
		expect(editor.tabs.some((item) => item.id === tab.id)).toBe(false);

		release();
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(seen.tickets).toBe(0);
		expect(screen.queryByTestId('results-area')).toBeNull();
	});
});
