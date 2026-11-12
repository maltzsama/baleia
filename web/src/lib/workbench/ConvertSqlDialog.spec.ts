import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/svelte';
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
	it('lists every target dialect as an option with its icon, Trino selected', () => {
		render(ConvertSqlDialog, {
			props: {
				open: true,
				mode: 'convert',
				sql: 'select 1',
				sourceDialect: 'postgresql',
				target: 'trino'
			}
		});
		const group = screen.getByRole('group', { name: /target dialect/i });
		const radios = screen.getAllByRole('radio');
		expect(radios.length).toBeGreaterThan(5);
		expect(group.querySelectorAll('label img').length).toBeGreaterThan(5);
		expect((screen.getByRole('radio', { name: 'Trino' }) as HTMLInputElement).checked).toBe(true);
		expect((screen.getByRole('radio', { name: 'Presto' }) as HTMLInputElement).checked).toBe(false);
	});
});
