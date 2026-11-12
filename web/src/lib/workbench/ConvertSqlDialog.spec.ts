import { describe, expect, it } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/svelte';
import ConvertSqlDialog from './ConvertSqlDialog.svelte';
import { kindForDialect } from '$lib/ui/patterns/kindIcon';

describe('kindForDialect', () => {
	it('uses the dialect name, except T-SQL which is SQL Server', () => {
		expect(kindForDialect('postgresql')).toBe('postgresql');
		expect(kindForDialect('mysql')).toBe('mysql');
		expect(kindForDialect('tsql')).toBe('sqlserver');
	});
});

describe('ConvertSqlDialog', () => {
	function open(target: string) {
		render(ConvertSqlDialog, {
			props: { open: true, mode: 'convert', sql: 'select 1', sourceDialect: 'trino', target }
		});
		return screen.getByRole('combobox', { name: /target dialect/i });
	}

	it('is a dropdown that shows the selected dialect with its icon', () => {
		const dropdown = open('trino');
		expect(dropdown.textContent).toContain('Trino');
		expect(dropdown.querySelector('img')).not.toBeNull();
		expect(screen.queryByRole('listbox')).toBeNull();
	});

	it('lists every dialect with its icon and picks one', async () => {
		const dropdown = open('trino');
		await fireEvent.click(dropdown);

		const list = screen.getByRole('listbox');
		const items = within(list).getAllByRole('option');
		expect(items.length).toBeGreaterThan(5);
		expect(list.querySelectorAll('li img').length).toBeGreaterThan(5);

		await fireEvent.click(within(list).getByRole('option', { name: 'PostgreSQL' }));
		expect(screen.queryByRole('listbox')).toBeNull();
		expect(dropdown.textContent).toContain('PostgreSQL');
	});

	it('moves with the arrow keys and picks with Enter', async () => {
		const dropdown = open('trino');
		// The first ArrowDown opens the list on Trino; two more reach PostgreSQL.
		for (let i = 0; i < 3; i++) await fireEvent.keyDown(dropdown, { key: 'ArrowDown' });
		await fireEvent.keyDown(dropdown, { key: 'Enter' });
		expect(dropdown.textContent).toContain('PostgreSQL');
	});
});
