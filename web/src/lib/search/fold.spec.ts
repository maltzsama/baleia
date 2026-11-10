import { describe, expect, it } from 'vitest';
import { foldText, startsWithFolded } from './fold';

describe('foldText', () => {
	it('ignores case and accents', () => {
		expect(foldText('Região Comercial')).toBe('regiao comercial');
		expect(foldText('AÇÃO')).toBe('acao');
		expect(foldText('região')).toBe('regiao');
	});
});

describe('startsWithFolded', () => {
	it('matches a prefix typed without accents or in another case', () => {
		expect(startsWithFolded('região_vendas', 'REGIA')).toBe(true);
		expect(startsWithFolded('orders', 'ord')).toBe(true);
		expect(startsWithFolded('orders', 'rders')).toBe(false);
	});
});
