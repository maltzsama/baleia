import { describe, expect, it } from 'vitest';
import { toCsv } from './csv';

describe('toCsv', () => {
	it('writes a header row and a line per row', () => {
		expect(
			toCsv(
				['a', 'b'],
				[
					[1, 2],
					[3, 4]
				]
			)
		).toBe('a,b\r\n1,2\r\n3,4');
	});

	it('quotes commas, quotes and newlines and leaves gaps empty', () => {
		expect(
			toCsv(
				['a', 'b'],
				[
					['x,y', 'say "hi"'],
					[null, 'line\nbreak']
				]
			)
		).toBe('a,b\r\n"x,y","say ""hi"""\r\n,"line\nbreak"');
	});
});
