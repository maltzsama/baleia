import { beforeEach, describe, expect, it } from 'vitest';
import {
	SPLIT_DEFAULT,
	SPLIT_MAX,
	SPLIT_MIN,
	clampSplit,
	loadSplit,
	saveSplit,
	splitFromKey,
	splitFromPointer
} from './split';

describe('workbench split (#395)', () => {
	beforeEach(() => localStorage.clear());

	it('keeps the results share within bounds', () => {
		expect(clampSplit(5)).toBe(SPLIT_MIN);
		expect(clampSplit(99)).toBe(SPLIT_MAX);
		expect(clampSplit(50.4)).toBe(50);
		expect(clampSplit(Number.NaN)).toBe(SPLIT_DEFAULT);
	});

	it('maps the pointer to the share below it', () => {
		const box = { top: 100, height: 800 };
		expect(splitFromPointer(500, box)).toBe(50);
		expect(splitFromPointer(260, box)).toBe(80);
		expect(splitFromPointer(890, box)).toBe(SPLIT_MIN);
	});

	it('moves by a step with the arrows and to the ends with Home/End', () => {
		expect(splitFromKey(46, 'ArrowUp')).toBe(51);
		expect(splitFromKey(46, 'ArrowDown')).toBe(41);
		expect(splitFromKey(46, 'Home')).toBe(SPLIT_MAX);
		expect(splitFromKey(46, 'End')).toBe(SPLIT_MIN);
		expect(splitFromKey(46, 'a')).toBeNull();
	});

	it('remembers the share and falls back to the default', () => {
		expect(loadSplit()).toBe(SPLIT_DEFAULT);
		saveSplit(62);
		expect(loadSplit()).toBe(62);
		localStorage.setItem('baleia.workbench.results-height', 'garbage');
		expect(loadSplit()).toBe(SPLIT_DEFAULT);
	});
});
