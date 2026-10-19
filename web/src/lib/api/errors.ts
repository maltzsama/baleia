import { t } from '$lib/i18n';
import type { MessageKey } from '$lib/i18n/messages';

// API errors carry a stable `code`; the client translates it and falls back to
// the English server message for codes it does not know (#67).
const codeKeys: Record<string, MessageKey> = {
	unauthorized: 'error_unauthorized',
	forbidden: 'error_forbidden',
	not_found: 'error_not_found',
	conflict: 'error_conflict',
	validation_error: 'error_validation_error',
	rate_limited: 'error_rate_limited',
	connection_error: 'error_connection_error',
	engine_error: 'error_engine_error',
	service_unavailable: 'error_service_unavailable',
	bad_request: 'error_bad_request',
	internal: 'error_internal'
};

/** localizedError maps an API error body to the active locale. */
export function localizedError(error: unknown, fallback: MessageKey = 'error_internal'): string {
	if (error && typeof error === 'object') {
		const body = error as { code?: string; message?: string };
		const key = body.code ? codeKeys[body.code] : undefined;
		if (key) return t(key);
		if (body.message) return body.message;
	}
	return t(fallback);
}
