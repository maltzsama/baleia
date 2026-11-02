import { describe, expect, it } from 'vitest';
import { parseSearchFilters } from './searchFilters';

describe('search filters grammar', () => {
	it('parses tag, type and free text', () => {
		expect(parseSearchFilters('tag:finance type:table orders')).toMatchObject({
			text: 'orders',
			kind: 'table',
			tags: ['finance']
		});
	});

	it('keeps a quoted value together', () => {
		expect(parseSearchFilters('tag:"finance ops" namespace:sales orders')).toMatchObject({
			text: 'orders',
			tags: ['finance ops'],
			namespace: ['sales']
		});
	});

	it('keeps an unknown key as free text', () => {
		expect(parseSearchFilters('foo:bar orders').text).toBe('foo:bar orders');
	});

	it('parses exclusions and certified', () => {
		const filters = parseSearchFilters('-tag:draft certified:yes orders');
		expect(filters.excludeTags).toEqual(['draft']);
		expect(filters.certified).toBe(true);
	});

	it('keeps an unknown certified value as free text', () => {
		expect(parseSearchFilters('certified:maybe orders').certified).toBeUndefined();
		expect(parseSearchFilters('certified:maybe orders').text).toBe('certified:maybe orders');
	});

	it('splits an unquoted namespace and keeps a quoted one whole', () => {
		expect(parseSearchFilters('namespace:sales.sub orders').namespace).toEqual(['sales', 'sub']);
		expect(parseSearchFilters('namespace:"sales.sub" orders').namespace).toEqual(['sales.sub']);
	});
});
