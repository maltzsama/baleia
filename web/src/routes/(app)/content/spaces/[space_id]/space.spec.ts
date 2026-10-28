import { HttpResponse } from 'msw';
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { http, server } from '../../../../../test/server';
import {
	makeSavedQuery,
	makeSemanticModel,
	makeSpace,
	makeWorkspace
} from '../../../../../test/factories';

const WS = '00000000-0000-0000-0000-000000000001';
const SPACE = '00000000-0000-0000-0000-000000000006';

vi.mock('$app/state', () => {
	const id = '00000000-0000-0000-0000-000000000006';
	return {
		page: { url: new URL(`http://localhost/content/spaces/${id}`), params: { space_id: id } }
	};
});

import SpacePage from './+page.svelte';

function useBackend(options: { metricsFail?: boolean; modelsFail?: boolean; queries?: boolean }) {
	let metricsFail = !!options.metricsFail;
	server.use(
		http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace({ id: WS })])),
		http.get('/workspaces/{id}/spaces', ({ response }) =>
			response(200).json([makeSpace({ id: SPACE })])
		),
		http.get('/workspaces/{id}/queries', ({ response }) =>
			response(200).json(
				options.queries ? [makeSavedQuery({ name: 'Weekly revenue', space_id: SPACE })] : []
			)
		),
		http.get('/workspaces/{id}/folders', ({ response }) => response(200).json([])),
		http.get('/workspaces/{id}/semantic/metrics', ({ response }) =>
			metricsFail ? HttpResponse.error() : response(200).json([])
		),
		http.get('/workspaces/{id}/semantic/models', ({ response }) =>
			options.modelsFail
				? HttpResponse.error()
				: response(200).json([makeSemanticModel({ label: 'Orders model' })])
		)
	);
	return {
		recoverMetrics() {
			metricsFail = false;
		}
	};
}

// #460: a failed metric or model lookup is shown, never read as empty.
describe('space page', () => {
	beforeEach(() => workspaceStore.select(makeWorkspace({ id: WS })));

	it('shows a metrics failure, keeps the models and retries', async () => {
		const backend = useBackend({ metricsFail: true });
		render(SpacePage);

		expect(await screen.findByText('Could not load the metrics')).toBeTruthy();
		expect(screen.getByText('Orders model')).toBeTruthy();
		expect(screen.queryByText('Nothing here')).toBeNull();

		backend.recoverMetrics();
		await fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
		await waitFor(() => expect(screen.queryByText('Could not load the metrics')).toBeNull());
	});

	it('shows a models failure next to the queries that loaded', async () => {
		useBackend({ modelsFail: true, queries: true });
		render(SpacePage);

		expect(await screen.findByText('Could not load the models')).toBeTruthy();
		expect(screen.getByText('Weekly revenue')).toBeTruthy();
	});

	it('says the space is empty only when everything loaded empty', async () => {
		useBackend({});
		server.use(
			http.get('/workspaces/{id}/semantic/models', ({ response }) => response(200).json([]))
		);
		render(SpacePage);

		expect(await screen.findByText('Nothing here')).toBeTruthy();
	});
});
