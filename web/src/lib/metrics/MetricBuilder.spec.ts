import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { savedQueries } from '$lib/stores/savedQueries.svelte';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { http, server } from '../../test/server';
import {
	makeCatalogBinding,
	makeSearchResult,
	makeSpace,
	makeTableDetail,
	makeWorkspace
} from '../../test/factories';
import MetricBuilder from './MetricBuilder.svelte';

const WS = '00000000-0000-0000-0000-000000000001';
const VIEW_ONLY = '00000000-0000-0000-0000-0000000000f1';
const EDITABLE = '00000000-0000-0000-0000-0000000000f2';

function useSpaces(spaces: ReturnType<typeof makeSpace>[]) {
	server.use(
		http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace({ id: WS })])),
		http.get('/workspaces/{id}/spaces', ({ response }) => response(200).json(spaces)),
		http.get('/workspaces/{id}/queries', ({ response }) => response(200).json([])),
		http.get('/workspaces/{id}/folders', ({ response }) => response(200).json([]))
	);
}

const viewOnly = makeSpace({
	id: VIEW_ONLY,
	name: 'Finance',
	my_role: 'viewer',
	allowed_actions: ['view']
});
const editable = makeSpace({
	id: EDITABLE,
	name: 'Analytics',
	my_role: 'editor',
	allowed_actions: ['view', 'edit_content']
});

// #461: a new metric never defaults to a space the user cannot edit.
describe('MetricBuilder space', () => {
	beforeEach(() => workspaceStore.select(makeWorkspace({ id: WS })));

	it.each([
		['view-only first', [viewOnly, editable]],
		['editable first', [editable, viewOnly]]
	])('defaults to the first editable space (%s)', async (_, spaces) => {
		useSpaces(spaces);
		render(MetricBuilder, {});

		const select = (await screen.findByLabelText('Space')) as HTMLSelectElement;
		await waitFor(() => expect(select.value).toBe(EDITABLE));
		const options = [...select.options].map((option) => option.value);
		expect(options).not.toContain(VIEW_ONLY);
	});

	it('leaves the space empty when none is editable', async () => {
		useSpaces([viewOnly]);
		render(MetricBuilder, {});

		const select = (await screen.findByLabelText('Space')) as HTMLSelectElement;
		await waitFor(() => expect(savedQueries.spaces.map((space) => space.id)).toEqual([VIEW_ONLY]));
		expect(select.value).toBe('');
		expect([...select.options].map((option) => option.value)).not.toContain(VIEW_ONLY);
	});
});

// #462: "Valid on this engine." only stands for the definition it checked.
describe('MetricBuilder validation', () => {
	let renders = 0;

	beforeEach(() => {
		renders = 0;
		workspaceStore.select(makeWorkspace({ id: WS }));
		server.use(
			http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace({ id: WS })])),
			http.get('/workspaces/{id}/spaces', ({ response }) => response(200).json([makeSpace()])),
			http.get('/workspaces/{id}/queries', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/folders', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/metadata/search', ({ response }) =>
				response(200).json([makeSearchResult()])
			),
			http.get('/workspaces/{id}/metadata/tables/{table_id}', ({ response }) =>
				response(200).json(makeTableDetail())
			),
			http.post('/workspaces/{id}/semantic/metrics/render', ({ response }) => {
				renders++;
				return response(200).json({ sql: 'SELECT count(*) FROM orders' });
			}),
			http.get('/catalogs/{id}/bindings', ({ response }) =>
				response(200).json([makeCatalogBinding()])
			),
			http.post('/workspaces/{id}/semantic/metrics/validate', ({ response }) =>
				response(200).json({ ok: true })
			)
		);
	});

	it('drops the result when the definition changes and keeps it for the label', async () => {
		render(MetricBuilder, {});
		await fireEvent.input(await screen.findByLabelText('Search a table'), {
			target: { value: 'orders' }
		});
		await fireEvent.click(await screen.findByRole('button', { name: 'sales.orders' }));
		await waitFor(() =>
			expect((screen.getByRole('button', { name: 'Validate' }) as HTMLButtonElement).disabled).toBe(
				false
			)
		);

		// The default binding arrives with the table; validate on it.
		await screen.findByLabelText('Validation binding');
		await fireEvent.click(screen.getByRole('button', { name: 'Validate' }));
		expect(await screen.findByText('Valid on this engine.')).toBeTruthy();

		// The label is not part of the definition.
		await fireEvent.input(screen.getByLabelText('Label'), { target: { value: 'Orders' } });
		expect(screen.getByText('Valid on this engine.')).toBeTruthy();

		const before = renders;
		await fireEvent.click(screen.getByLabelText('Custom SQL expression'));
		await waitFor(() => expect(screen.queryByText('Valid on this engine.')).toBeNull());
		// Let the debounced preview of the new definition settle.
		await waitFor(() => expect(renders).toBeGreaterThan(before));
	});
});
