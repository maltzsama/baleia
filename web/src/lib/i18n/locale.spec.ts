import { afterEach, describe, expect, it } from 'vitest';
import { getLocale, localStorageKey } from '$lib/paraglide/runtime';
import { initLocale, localeStore, setAppLocale } from './locale.svelte';

afterEach(() => {
	localStorage.clear();
	setAppLocale('en');
	document.documentElement.lang = 'en';
});

describe('localeStore', () => {
	it('switches the active locale and re-renders from getLocale()', () => {
		setAppLocale('pt-BR');
		expect(localeStore.current).toBe('pt-BR');
		expect(getLocale()).toBe('pt-BR');
	});

	it('persists the choice for anonymous pages', () => {
		setAppLocale('pt-BR');
		expect(localStorage.getItem(localStorageKey)).toBe('pt-BR');
	});

	it('keeps <html lang> in sync', () => {
		setAppLocale('pt-BR');
		expect(document.documentElement.lang).toBe('pt-BR');
		initLocale();
		expect(document.documentElement.lang).toBe('pt-BR');
	});
});
