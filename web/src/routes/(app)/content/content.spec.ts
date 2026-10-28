import { fireEvent, render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { http, server } from '../../../test/server';
import { makeSpace, makeWorkspace } from '../../../test/factories';
import ContentPage from './+page.svelte';

const WS = '00000000-0000-0000-0000-000000000001';

// #476: the content search says when it is too short and when nothing
// matched, apart from the spaces below it.
describe('content search states', () => {
	beforeEach(() => {
		workspaceStore.select(makeWorkspace({ id: WS }));
		server.use(
			http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace({ id: WS })])),
			http.get('/workspaces/{id}/spaces', ({ response }) =>
				response(200).json([makeSpace({ name: 'General' })])
			),
			http.get('/workspaces/{id}/queries', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/folders', ({ response }) => response(200).json([])),
			http.get('/workspaces/{id}/content/search', ({ response }) => response(200).json([]))
		);
	});

	async function searchFor(text: string) {
		render(ContentPage);
		await screen.findByText('General');
		await fireEvent.input(screen.getByLabelText('Search content'), { target: { value: text } });
		await fireEvent.click(screen.getByRole('button', { name: 'Search' }));
	}

	it('explains the minimum length', async () => {
		await searchFor('a');
		expect(await screen.findByText('Type at least two characters to search.')).toBeTruthy();
	});

	it('says nothing matched and clears the search', async () => {
		await searchFor('nothing');
		expect(await screen.findByText('No content matches “nothing”.')).toBeTruthy();

		await fireEvent.click(screen.getByRole('button', { name: 'Clear search' }));
		expect(screen.queryByText('No content matches “nothing”.')).toBeNull();
		expect((screen.getByLabelText('Search content') as HTMLInputElement).value).toBe('');
	});
});
