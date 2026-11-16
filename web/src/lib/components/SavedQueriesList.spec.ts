import { render, screen } from '@testing-library/svelte';
import { HttpResponse } from 'msw';
import { beforeEach, describe, expect, it } from 'vitest';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { http, server } from '../../test/server';
import { makeSavedQuery, makeSpace, makeWorkspace } from '../../test/factories';
import SavedQueriesList from './SavedQueriesList.svelte';

function mockContent(spaceRole: 'viewer' | 'editor' | 'manager') {
	server.use(
		http.get('/workspaces/{id}/spaces', ({ response }) =>
			response(200).json([
				makeSpace({ id: 's1', name: 'Finance', my_role: spaceRole, is_default: false })
			])
		),
		http.get('/workspaces/{id}/queries', ({ response }) =>
			response(200).json([makeSavedQuery({ space_id: 's1', name: 'Revenue' })])
		),
		http.get('/workspaces/{id}/folders', ({ response }) => response(200).json([]))
	);
}

describe('SavedQueriesList', () => {
	beforeEach(() => {
		workspaceStore.select(makeWorkspace());
	});

	it('groups saved queries by space and shows the manager menu', async () => {
		mockContent('manager');
		render(SavedQueriesList);
		expect(await screen.findByText('Finance')).toBeTruthy();
		expect(await screen.findByText('Revenue')).toBeTruthy();
		expect(screen.getByLabelText('Manage Finance')).toBeTruthy();
	});

	it('hides the actions of a query the caller cannot edit', async () => {
		mockContent('viewer');
		render(SavedQueriesList);
		expect(await screen.findByText('Revenue')).toBeTruthy();
		expect(screen.queryByLabelText('Actions for Revenue')).toBeNull();
		expect(screen.queryByLabelText('Manage Finance')).toBeNull();
	});

	// #1084: a failed load is a retryable error state, not a bare red line.
	it('shows a retryable error when the list fails', async () => {
		server.use(
			http.get('/workspaces/{id}/spaces', () =>
				HttpResponse.json(
					{ code: 'service_unavailable', message: 'content store unreachable' },
					{ status: 503 }
				)
			),
			http.get('/workspaces/{id}/queries', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/folders', ({ response }) => response(200).json([]))
		);
		render(SavedQueriesList);

		expect(await screen.findByText('Could not load saved queries.')).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
		expect(screen.queryByText(/content store unreachable/)).toBeNull();
	});
});
