import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { editor } from '$lib/stores/editor.svelte';
import { clearEnginesCache } from '$lib/engines/client';
import { clearMetadataCache } from '$lib/metadata/client';
import { http, server } from '../../test/server';
import { makeWorkspace } from '../../test/factories';
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
});
