import { fireEvent, render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { editor } from '$lib/stores/editor.svelte';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { http, server } from '../../test/server';
import { makeWorkspace } from '../../test/factories';
import RightSidebar from './RightSidebar.svelte';

describe('RightSidebar', () => {
	beforeEach(() => {
		editor.resetTabs();
		editor.setReferenceScope(null);
		workspaceStore.select(makeWorkspace());
		server.use(
			http.get('/assistant/status', ({ response }) =>
				response(200).json({ enabled: false, data_sharing: 'metadata_only' })
			)
		);
	});

	it('has the Inspector and the SQL reference as tabs (#979)', () => {
		render(RightSidebar, { props: { collapsed: false } });
		const tabs = screen.getAllByRole('tab').map((tab) => tab.textContent?.trim());
		expect(tabs).toEqual(['Inspector', 'SQL reference']);
		expect(screen.getByRole('tab', { name: 'Inspector' }).getAttribute('aria-selected')).toBe(
			'true'
		);
	});

	it('asks for a target in the SQL reference when the editor has none', async () => {
		render(RightSidebar, { props: { collapsed: false } });
		await fireEvent.click(screen.getByRole('tab', { name: 'SQL reference' }));
		expect(screen.getByText(/Choose a catalog in the editor/)).toBeTruthy();
	});

	it('lists the functions of the editor target in the SQL reference (#979)', async () => {
		server.use(
			http.get('/workspaces/{id}/metadata/functions', ({ response }) =>
				response(200).json([{ name: 'date_trunc', kind: 'function', call: true }])
			)
		);
		editor.setReferenceScope({ workspaceId: 'w', catalogId: 'c', bindingId: 'b' });
		render(RightSidebar, { props: { collapsed: false } });
		await fireEvent.click(screen.getByRole('tab', { name: 'SQL reference' }));
		await fireEvent.click(await screen.findByRole('button', { name: /date_trunc/ }));
		expect(editor.pendingInsert).toMatchObject({ text: 'date_trunc()', caret: 11 });
	});
});
