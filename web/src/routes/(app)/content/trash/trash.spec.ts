import { render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { setAppLocale } from '$lib/i18n/locale.svelte';
import { restorableKinds } from '$lib/content/kinds';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { http, server } from '../../../../test/server';
import { makeWorkspace } from '../../../../test/factories';
import TrashPage from './+page.svelte';

const WS = '00000000-0000-0000-0000-000000000001';

// #479: the copy names every kind the trash restores, from one list.
describe('trash copy', () => {
	beforeEach(() => {
		workspaceStore.select(makeWorkspace({ id: WS }));
		server.use(
			http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace({ id: WS })])),
			http.get('/workspaces/{id}/trash', ({ response }) => response(200).json([]))
		);
	});
	afterEach(() => setAppLocale('en'));

	it('mentions queries, metrics and models in English', async () => {
		render(TrashPage);
		expect(
			await screen.findByText(
				'Deleted queries, metrics, and models appear here and can be restored.'
			)
		).toBeTruthy();
		expect(
			screen.getByText('Deleted queries, metrics, and models can be restored with their history.')
		).toBeTruthy();
	});

	it('mentions them in Portuguese', async () => {
		setAppLocale('pt-BR');
		render(TrashPage);
		expect(
			await screen.findByText(
				'Itens excluídos (queries, métricas e modelos) aparecem aqui e podem ser restaurados.'
			)
		).toBeTruthy();
	});

	it('filters by exactly the restorable kinds', async () => {
		render(TrashPage);
		const select = (await screen.findByLabelText('Kind')) as HTMLSelectElement;
		const values = [...select.options].map((option) => option.value).filter(Boolean);
		expect(values).toEqual(restorableKinds.map((entry) => entry.kind));
	});
});
