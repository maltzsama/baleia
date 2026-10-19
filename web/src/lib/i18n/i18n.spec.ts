import { beforeEach, describe, expect, it } from 'vitest';
import { localizedError } from '$lib/api/errors';
import { currentLocale, initLocale, setLocale, t } from './locale.svelte';

describe('i18n', () => {
	beforeEach(() => {
		localStorage.clear();
		setLocale('en');
	});

	it('translates and switches locale', () => {
		expect(t('nav_settings')).toBe('Settings');
		setLocale('pt-BR');
		expect(t('nav_settings')).toBe('Configurações');
		expect(currentLocale()).toBe('pt-BR');
	});

	it('resolves preference → stored → browser', () => {
		initLocale('pt-BR');
		expect(currentLocale()).toBe('pt-BR');
		initLocale(null);
		expect(currentLocale()).toBe('pt-BR');
	});

	it('localizes API error codes and falls back to the server message', () => {
		setLocale('pt-BR');
		expect(localizedError({ code: 'not_found' })).toBe('Não encontrado.');
		expect(localizedError({ code: 'brand_new', message: 'English fallback' })).toBe(
			'English fallback'
		);
	});
});
