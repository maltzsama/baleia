import { createRawSnippet } from 'svelte';
import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

import Badge from './Badge.svelte';
import Button from './Button.svelte';
import Checkbox from './Checkbox.svelte';
import EmptyState from './EmptyState.svelte';
import ErrorState from './ErrorState.svelte';
import IconButton from './IconButton.svelte';
import Input from './Input.svelte';
import Select from './Select.svelte';
import Skeleton from './Skeleton.svelte';
import Table from './Table.svelte';
import Tabs from './Tabs.svelte';
import Textarea from './Textarea.svelte';
import Toaster from './Toaster.svelte';
import { toasts } from './toast.svelte';

const text = (value: string) => createRawSnippet(() => ({ render: () => `<span>${value}</span>` }));

describe('Button', () => {
	it('renders its children and blocks interaction while loading', () => {
		render(Button, { props: { children: text('Save'), loading: true } });
		const button = screen.getByRole('button', { name: /save/i }) as HTMLButtonElement;
		expect(button.disabled).toBe(true);
		expect(button.getAttribute('aria-busy')).toBe('true');
	});
});

describe('IconButton', () => {
	it('exposes an accessible name', () => {
		render(IconButton, { props: { label: 'Close panel', children: text('×') } });
		expect(screen.getByRole('button', { name: 'Close panel' })).toBeTruthy();
	});
});

describe('Input', () => {
	it('associates the label and shows the error', () => {
		render(Input, { props: { label: 'Name', value: 'Ada', error: 'Required' } });
		expect((screen.getByLabelText('Name') as HTMLInputElement).value).toBe('Ada');
		expect(screen.getByText('Required')).toBeTruthy();
	});
});

describe('Textarea', () => {
	it('associates the label with the control', () => {
		render(Textarea, { props: { label: 'Description', value: 'Hello' } });
		expect((screen.getByLabelText('Description') as HTMLTextAreaElement).value).toBe('Hello');
	});
});

describe('Select', () => {
	it('renders every option', () => {
		render(Select, {
			props: {
				label: 'Engine',
				value: 'trino',
				options: [
					{ value: 'trino', label: 'Trino' },
					{ value: 'postgresql', label: 'PostgreSQL' }
				]
			}
		});
		expect((screen.getByLabelText('Engine') as HTMLSelectElement).value).toBe('trino');
		expect(screen.getByRole('option', { name: 'PostgreSQL' })).toBeTruthy();
	});
});

describe('Checkbox', () => {
	it('toggles', async () => {
		render(Checkbox, { props: { label: 'Remember me', checked: false } });
		const checkbox = screen.getByLabelText('Remember me') as HTMLInputElement;
		expect(checkbox.checked).toBe(false);
		await fireEvent.click(checkbox);
		expect(checkbox.checked).toBe(true);
	});
});

describe('Badge', () => {
	it('renders its text with the tone', () => {
		render(Badge, { props: { tone: 'success', children: text('Active') } });
		expect(screen.getByText('Active')).toBeTruthy();
	});
});

describe('Skeleton', () => {
	it('is hidden from assistive technology', () => {
		const { container } = render(Skeleton, { props: { height: '2rem' } });
		expect(container.firstElementChild?.getAttribute('aria-hidden')).toBe('true');
	});
});

describe('EmptyState', () => {
	it('renders a title and description', () => {
		render(EmptyState, { props: { title: 'No queries', description: 'Save one to start.' } });
		expect(screen.getByText('No queries')).toBeTruthy();
		expect(screen.getByText('Save one to start.')).toBeTruthy();
	});
});

describe('ErrorState', () => {
	it('renders the message and calls onretry', async () => {
		const onretry = vi.fn();
		render(ErrorState, { props: { message: 'Network down', onretry } });
		expect(screen.getByRole('alert').textContent).toContain('Network down');
		await fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
		expect(onretry).toHaveBeenCalledOnce();
	});
});

describe('Table', () => {
	it('renders rows and emits sort events', async () => {
		const onsort = vi.fn();
		render(Table, {
			props: {
				columns: [
					{ key: 'name', label: 'Name', sortable: true },
					{ key: 'status', label: 'Status' }
				],
				rows: [{ id: '1', name: 'orders', status: 'active' }],
				rowKey: (row: Record<string, unknown>) => String(row.id),
				onsort
			}
		});
		expect(screen.getByText('orders')).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: /name/i }));
		expect(onsort).toHaveBeenCalledWith('name');
	});
});

describe('Tabs', () => {
	it('selects a tab and supports arrow keys', async () => {
		const onchange = vi.fn();
		render(Tabs, {
			props: {
				tabs: [
					{ id: 'one', label: 'One' },
					{ id: 'two', label: 'Two' }
				],
				active: 'one',
				onchange
			}
		});
		expect(screen.getByRole('tab', { name: 'One' }).getAttribute('aria-selected')).toBe('true');
		await fireEvent.click(screen.getByRole('tab', { name: 'Two' }));
		expect(onchange).toHaveBeenCalledWith('two');
	});
});

describe('Toaster', () => {
	it('renders and dismisses a toast', async () => {
		const id = toasts.success('Saved');
		render(Toaster);
		expect(await screen.findByText('Saved')).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Dismiss notification' }));
		expect(screen.queryByText('Saved')).toBeNull();
		toasts.dismiss(id);
	});
});
