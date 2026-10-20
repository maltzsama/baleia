import { describe, expect, it } from 'vitest';
import { setAppLocale } from '$lib/i18n/locale.svelte';
import { errorMessage, fieldErrorMessage } from './errors';

describe('errorMessage', () => {
	it('localizes a known code in both locales', () => {
		setAppLocale('en');
		expect(errorMessage({ code: 'not_found' })).toBe('The resource was not found.');

		setAppLocale('pt-BR');
		expect(errorMessage({ code: 'not_found' })).toBe('O recurso não foi encontrado.');
	});

	it('falls back to the English message for an unknown code', () => {
		setAppLocale('pt-BR');
		expect(errorMessage({ code: 'brand_new_code', message: 'English only' })).toBe('English only');
	});

	it('keeps field messages until a field code is known', () => {
		setAppLocale('en');
		expect(errorMessage({ errors: [{ message: 'name is required' }] })).toBe('name is required');
	});
});

describe('fieldErrorMessage', () => {
	it('translates a field code', () => {
		setAppLocale('en');
		expect(fieldErrorMessage({ field: 'locale', code: 'not_allowed' })).toBe(
			'This value is not allowed.'
		);

		setAppLocale('pt-BR');
		expect(fieldErrorMessage({ field: 'locale', code: 'not_allowed' })).toBe(
			'Este valor não é permitido.'
		);
	});

	it('returns undefined for an unknown field code', () => {
		expect(fieldErrorMessage({ field: 'x', code: 'brand_new' })).toBeUndefined();
	});
});
