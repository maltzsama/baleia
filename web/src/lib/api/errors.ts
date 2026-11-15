// UI translation of API errors (#67).
//
// The API returns a stable, closed `code` (plus optional `params` and
// `fields`); the English `message` is for logs and non-UI clients. The UI maps
// the code to a compiled Paraglide message. An unknown code (a newer server)
// falls back to the English message instead of showing nothing.
import * as messages from '$lib/paraglide/messages';
import { m } from '$lib/paraglide/messages';

export type FieldError = {
	field: string;
	code: string;
	params?: Record<string, string>;
};

/** Error body shared by the API (`Error` / `ValidationError`). */
export type ApiErrorBody = {
	code?: string;
	message?: string;
	params?: Record<string, unknown>;
	fields?: FieldError[];
	errors?: { field?: string; message?: string }[];
};

type MessageFunction = (params?: Record<string, string>) => string;

const bundle = messages as unknown as Record<string, MessageFunction | undefined>;

function stringParams(params?: Record<string, unknown>): Record<string, string> | undefined {
	if (!params) return undefined;
	return Object.fromEntries(
		Object.entries(params).map(([key, value]) => [
			key,
			typeof value === 'string' ? value : String(value)
		])
	);
}

function localized(prefix: 'error' | 'field', code: string, params?: Record<string, unknown>) {
	return bundle[`${prefix}_${code}`]?.(stringParams(params));
}

/** Localized message for a field error, or undefined when the code is unknown. */
export function fieldErrorMessage(field: FieldError): string | undefined {
	return localized('field', field.code, field.params);
}

/** Localizes an API error body, a thrown value or a plain string. */
export function errorMessage(error: unknown, fallback: string = m.error_internal()): string {
	const safeFallback = fallback.trim() ? fallback : m.error_internal();

	if (typeof error === 'string') return error.trim() ? error : safeFallback;

	if (error && typeof error === 'object') {
		const body = error as ApiErrorBody;
		if (body.code) {
			const translated = localized('error', body.code, body.params);
			if (translated) return translated;
		}
		if (body.fields?.length) {
			const translated = fieldErrorMessage(body.fields[0]);
			if (translated) return translated;
		}
		const embedded = body.errors?.[0]?.message ?? body.message;
		if (typeof embedded === 'string' && embedded.trim()) return embedded;
	}

	if (error instanceof Error && error.message.trim()) return error.message;
	return safeFallback;
}

/**
 * UI alias for `errorMessage` (#1082). Every visible error goes through it so a
 * thrown API body never renders as `[object Object]` and the catalog decides
 * the copy when the code is known.
 */
export function apiErrorMessage(error: unknown, fallback?: string): string {
	return errorMessage(error, fallback);
}
