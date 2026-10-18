// Typed API client generated from api/swagger.yaml (#28).
//
// This is the single place that knows the API base URL and the auth/refresh
// dance. Call sites use the typed `client` (or the `api` helpers) and never
// build URLs by hand.
import createClient, { type Middleware } from 'openapi-fetch';
import type { paths } from './schema';
import { API_BASE, API_ROOT } from './base';
import { clear, getAccessToken, refresh, setAccessToken } from '$lib/auth/session';

export { API_BASE, API_ROOT };
export { getAccessToken, setAccessToken };
export const clearSession = clear;

export type AuthHandlers = {
	/** Called once when a refresh fails and the session is gone. */
	onSessionExpired?: () => void;
	/** Called after a successful refresh with the new access token. */
	onTokenRefreshed?: (token: string) => void;
};

let handlers: AuthHandlers = {};

export function configureAuth(next: AuthHandlers): void {
	handlers = next;
}

// A request body can only be read once. Keep an unconsumed clone so a 401 retry
// can replay POST/PATCH requests after the original was sent.
const replayable = new WeakMap<Request, Request>();

const authMiddleware: Middleware = {
	onRequest({ request }) {
		// Required by the API on refresh/logout (#275); harmless elsewhere.
		request.headers.set('X-Requested-With', 'baleia');
		const token = getAccessToken();
		if (token) request.headers.set('Authorization', `Bearer ${token}`);
		if (request.method !== 'GET' && request.method !== 'HEAD') {
			replayable.set(request, request.clone());
		}
		return request;
	},
	async onResponse({ request, response }) {
		if (response.status !== 401) return response;

		// Do not attempt to refresh the refresh call itself.
		if (request.url.includes('/auth/refresh') || request.url.includes('/auth/login')) {
			return response;
		}

		const refreshed = await refresh(true);
		if (!refreshed) {
			handlers.onSessionExpired?.();
			return response;
		}

		const token = getAccessToken();
		if (token) handlers.onTokenRefreshed?.(token);
		const retry = (replayable.get(request) ?? request).clone();
		if (token) retry.headers.set('Authorization', `Bearer ${token}`);
		return fetch(retry);
	}
};

export const client = createClient<paths>({
	baseUrl: API_ROOT,
	// Send the HttpOnly refresh cookie on the auth routes (#51).
	credentials: 'include',
	// Resolve fetch at call time so test interceptors (MSW) and stubs apply.
	fetch: (...args) => fetch(...args)
});
client.use(authMiddleware);

/**
 * Escape hatch for responses that are not JSON (Arrow result streams) and for
 * the legacy explorer routes still served outside the OpenAPI contract (#23
 * replaces them with `/metadata/*`). Everything documented should use `client`.
 */
export async function apiFetch(path: string, init: RequestInit = {}): Promise<Response> {
	const headers = new Headers(init.headers);
	const token = getAccessToken();
	if (token) headers.set('Authorization', `Bearer ${token}`);
	return fetch(`${API_ROOT}${path}`, { ...init, headers });
}

/** Error body shared by the API (`Error` / `ValidationError`). */
export type ApiError = {
	code?: string;
	message?: string;
	errors?: { field?: string; message?: string }[];
};

/** Normalizes an unknown thrown value or API error body into a message. */
export function apiErrorMessage(error: unknown, fallback = 'Request failed'): string {
	if (typeof error === 'string') return error;
	if (error && typeof error === 'object') {
		const body = error as ApiError;
		if (body.errors?.length) return body.errors[0]?.message ?? body.message ?? fallback;
		if (body.message) return body.message;
	}
	if (error instanceof Error) return error.message;
	return fallback;
}
