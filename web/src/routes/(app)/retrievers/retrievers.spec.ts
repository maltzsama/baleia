import { screen } from '@testing-library/svelte';
import { HttpResponse } from 'msw';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { http, server } from '../../../test/server';
import { makeMe, makeWorkspace } from '../../../test/factories';
import { renderPage } from '../../../test/render';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));

import RetrieversPage from './+page.svelte';

const WS = '00000000-0000-0000-0000-000000000001';

describe('retrievers page', () => {
	beforeEach(() => {
		workspaceStore.select(makeWorkspace({ id: WS }));
		server.use(
			http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace({ id: WS })]))
		);
	});

	// #1084: a failing list renders the catalog copy with a retry, never the
	// raw server message.
	it('shows the localized load failure with retry', async () => {
		server.use(
			http.get('/workspaces/{id}/retrievers', () =>
				HttpResponse.json(
					{ code: 'service_unavailable', message: 'scheduler unavailable' },
					{ status: 503 }
				)
			)
		);
		renderPage(RetrieversPage, { me: makeMe(), url: '/retrievers' });

		expect(await screen.findByText('Could not load retrievers')).toBeTruthy();
		expect(screen.getByText('The service is unavailable.')).toBeTruthy();
		expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
		expect(screen.queryByText(/scheduler unavailable/)).toBeNull();
	});
});
