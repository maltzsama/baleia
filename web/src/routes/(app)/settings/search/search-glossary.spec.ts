import { fireEvent, screen, waitFor, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { http, server } from '../../../../test/server';
import { makeGlossaryEntry, makeMe, makeWorkspace } from '../../../../test/factories';
import { renderPage } from '../../../../test/render';
import SearchGlossaryPage from './+page.svelte';

const ENTRY = makeGlossaryEntry();

function mockWorkspace() {
	server.use(http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace()])));
}

describe('Settings · Search glossary (#943)', () => {
	beforeEach(() => {
		workspaceStore.select(makeWorkspace());
	});

	it('lets an editor add a set of synonyms and shows it', async () => {
		let entries = [ENTRY];
		const created: unknown[] = [];
		mockWorkspace();
		server.use(
			http.get('/workspaces/{id}/search/glossary', ({ response }) =>
				response(200).json({ entries, allowed_actions: ['view', 'edit'] })
			),
			http.post('/workspaces/{id}/search/glossary', async ({ request, response }) => {
				created.push(await request.json());
				const added = makeGlossaryEntry({
					id: '00000000-0000-0000-0000-0000000000e2',
					term: 'churn',
					synonyms: ['cancelamento']
				});
				entries = [...entries, added];
				return response(201).json(added);
			})
		);
		renderPage(SearchGlossaryPage, { me: makeMe() });

		expect(await screen.findByText('faturamento, revenue')).toBeTruthy();
		await fireEvent.input(screen.getByLabelText('Term'), { target: { value: 'Churn' } });
		await fireEvent.input(screen.getByLabelText('Synonyms'), {
			target: { value: 'cancelamento,  ' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Add' }));

		await waitFor(() => expect(created).toEqual([{ term: 'Churn', synonyms: ['cancelamento'] }]));
		expect(await screen.findByText('cancelamento')).toBeTruthy();
		expect((screen.getByLabelText('Term') as HTMLInputElement).value).toBe('');
	});

	it('says a term that already has an entry', async () => {
		mockWorkspace();
		server.use(
			http.get('/workspaces/{id}/search/glossary', ({ response }) =>
				response(200).json({ entries: [ENTRY], allowed_actions: ['view', 'edit'] })
			),
			http.post('/workspaces/{id}/search/glossary', ({ response }) =>
				response(409).json({ code: 'conflict', message: 'this term already has an entry' })
			)
		);
		renderPage(SearchGlossaryPage, { me: makeMe() });

		await screen.findByText('faturamento, revenue');
		await fireEvent.input(screen.getByLabelText('Term'), { target: { value: 'receita' } });
		await fireEvent.input(screen.getByLabelText('Synonyms'), { target: { value: 'x1' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Add' }));

		expect((await screen.findByRole('alert')).textContent).toBe('This term already has an entry.');
	});

	it('asks to confirm, with what changes, before removing an entry', async () => {
		let entries = [ENTRY];
		let removed = '';
		mockWorkspace();
		server.use(
			http.get('/workspaces/{id}/search/glossary', ({ response }) =>
				response(200).json({ entries, allowed_actions: ['view', 'edit'] })
			),
			http.delete('/workspaces/{id}/search/glossary/{entry_id}', ({ params, response }) => {
				removed = String(params.entry_id);
				entries = [];
				return response(204).empty();
			})
		);
		renderPage(SearchGlossaryPage, { me: makeMe() });

		await screen.findByText('faturamento, revenue');
		await fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
		const dialog = await screen.findByRole('dialog');
		expect(
			within(dialog).getByText(
				/Searching for “receita” will no longer also look for faturamento, revenue/
			)
		).toBeTruthy();
		expect(removed).toBe('');
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Remove' }));

		await waitFor(() => expect(removed).toBe(ENTRY.id));
		expect(await screen.findByText('No synonyms yet')).toBeTruthy();
	});

	it('only reads the glossary for a member who cannot edit', async () => {
		mockWorkspace();
		server.use(
			http.get('/workspaces/{id}/search/glossary', ({ response }) =>
				response(200).json({ entries: [ENTRY], allowed_actions: ['view'] })
			)
		);
		renderPage(SearchGlossaryPage, { me: makeMe() });

		expect(await screen.findByText('faturamento, revenue')).toBeTruthy();
		expect(screen.getByText('You can read the glossary. Editors can change it.')).toBeTruthy();
		expect(screen.queryByLabelText('Term')).toBeNull();
		expect(screen.queryByRole('button', { name: 'Remove' })).toBeNull();
	});

	it('shows an error with a retry when the glossary does not load', async () => {
		let fail = true;
		mockWorkspace();
		server.use(
			http.get('/workspaces/{id}/search/glossary', ({ response }) =>
				fail
					? response(404).json({ code: 'not_found', message: 'workspace not found' })
					: response(200).json({ entries: [], allowed_actions: ['view'] })
			)
		);
		renderPage(SearchGlossaryPage, { me: makeMe() });

		expect(await screen.findByRole('button', { name: /try again/i })).toBeTruthy();
		fail = false;
		await fireEvent.click(screen.getByRole('button', { name: /try again/i }));
		expect(await screen.findByText('No synonyms yet')).toBeTruthy();
	});
});
