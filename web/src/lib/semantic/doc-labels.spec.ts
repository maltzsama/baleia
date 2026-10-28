import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import { workspaceStore } from '$lib/stores/workspace.svelte';
import { makeTableDetail, makeWorkspace } from '../../test/factories';
import DocumentColumns from './DocumentColumns.svelte';
import MarkdownEditor from '$lib/ui/patterns/MarkdownEditor.svelte';

// #475: every description and tags field has a name a screen reader reads.
describe('documentation field labels', () => {
	beforeEach(() => workspaceStore.select(makeWorkspace()));

	it('names the markdown editor by its label', () => {
		render(MarkdownEditor, { props: { label: 'Description', value: '' } });
		expect(screen.getByRole('textbox', { name: 'Description' })).toBeTruthy();
		expect(screen.getByRole('tab', { name: 'Write' })).toBeTruthy();
	});

	it('names each column field by its column and purpose', () => {
		render(DocumentColumns, {
			props: {
				open: true,
				catalogId: 'c1',
				namespace: ['sales'],
				table: 'orders',
				columns: makeTableDetail().columns
			}
		});

		expect(screen.getByRole('textbox', { name: 'Description of id' })).toBeTruthy();
		expect(screen.getByRole('textbox', { name: 'Tags of id' })).toBeTruthy();
		expect(screen.getByRole('textbox', { name: 'Description of order_note' })).toBeTruthy();
		expect(screen.getByRole('rowheader', { name: 'order_note' })).toBeTruthy();
		expect(screen.getAllByRole('columnheader')).toHaveLength(3);
	});
});
