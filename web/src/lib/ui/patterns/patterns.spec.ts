import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

import ConfirmDialog from './ConfirmDialog.svelte';
import DataTable from './DataTable.svelte';
import KeyValueEditor from './KeyValueEditor.svelte';
import MarkdownView from './MarkdownView.svelte';
import OneTimeSecret from './OneTimeSecret.svelte';
import StatusBadge from './StatusBadge.svelte';

describe('StatusBadge', () => {
	it('renders a label for every mapped status', () => {
		render(StatusBadge, { props: { kind: 'statement', status: 'skipped' } });
		expect(screen.getByText('Skipped')).toBeTruthy();
	});

	it('renders the raw value for an unknown status', () => {
		render(StatusBadge, { props: { kind: 'binding', status: 'mystery' } });
		expect(screen.getByText('mystery')).toBeTruthy();
	});
});

describe('MarkdownView', () => {
	it('strips scripts and never renders images', () => {
		const source =
			'<script>alert(1)</script>\n\n[bad](javascript:alert(1))\n\n![alt](https://example.com/x.png)';
		const { container } = render(MarkdownView, { props: { source } });
		expect(container.querySelector('script')).toBeNull();
		expect(container.querySelector('img')).toBeNull();
		expect(container.querySelector('a[href^="javascript:"]')).toBeNull();
	});
});

describe('OneTimeSecret', () => {
	it('requires acknowledgement and removes the secret after closing', async () => {
		render(OneTimeSecret, { props: { open: true, secret: 's3cret-value' } });
		const done = screen.getByRole('button', { name: 'Done' }) as HTMLButtonElement;
		expect(done.disabled).toBe(true);

		const acknowledge = screen.getByLabelText('I have copied this value') as HTMLInputElement;
		await fireEvent.click(acknowledge);
		expect(done.disabled).toBe(false);

		await fireEvent.click(done);
		expect(screen.queryByText('s3cret-value')).toBeNull();
	});
});

describe('KeyValueEditor', () => {
	it('renders protected keys read-only', () => {
		render(KeyValueEditor, {
			props: { value: { region: 'eu-west-1' }, protectedKeys: ['region'] }
		});
		const key = screen.getByPlaceholderText('key') as HTMLInputElement;
		expect(key.readOnly).toBe(true);
	});
});

describe('DataTable', () => {
	it('calls onLoadMore once and renders row links as anchors', async () => {
		const onloadmore = vi.fn();
		render(DataTable, {
			props: {
				columns: [{ key: 'name', label: 'Name' }],
				rows: [{ id: '1', name: 'orders' }],
				rowKey: (row: Record<string, unknown>) => String(row.id),
				rowHref: (row: Record<string, unknown>) => `/orders/${row.id}`,
				hasMore: true,
				onloadmore
			}
		});
		expect(screen.getByRole('link', { name: 'orders' }).getAttribute('href')).toBe('/orders/1');
		await fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
		expect(onloadmore).toHaveBeenCalledOnce();
	});
});

describe('ConfirmDialog', () => {
	it('gates the confirm button behind type-to-confirm and keeps errors inline', async () => {
		const onconfirm = vi.fn(async () => {
			throw new Error('Server rejected the delete');
		});
		render(ConfirmDialog, {
			props: {
				open: true,
				title: 'Delete table',
				resourceName: 'orders',
				confirmLabel: 'Delete',
				typeToConfirm: true,
				onconfirm
			}
		});

		const confirm = screen.getByRole('button', { name: 'Delete' }) as HTMLButtonElement;
		expect(confirm.disabled).toBe(true);

		await fireEvent.input(screen.getByLabelText('Type "orders" to confirm'), {
			target: { value: 'orders' }
		});
		expect(confirm.disabled).toBe(false);

		await fireEvent.click(confirm);
		expect(onconfirm).toHaveBeenCalledOnce();
		expect(await screen.findByText('Server rejected the delete')).toBeTruthy();
		expect(screen.getByLabelText('Type "orders" to confirm')).toBeTruthy();
	});
});
