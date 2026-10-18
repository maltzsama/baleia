// Typed API client generated from api/swagger.yaml (#28).
//
// This is the single place that knows the API base URL and the auth/refresh
// dance. Call sites use the typed `client` (or the `api` helpers) and never
// build URLs by hand.
import createClient, { type Middleware } from 'openapi-fetch';
import type { paths } from './schema';

/** Configured origin for the API, without the `/api/v1` prefix. */
export const API_BASE = import.meta.env.VITE_API_BASE ?? '';

/** Root of the versioned API, used for raw requests (Arrow, WebSocket tickets). */
export const API_ROOT = `${API_BASE}/api/v1`;

// Access tokens live in memory only. The refresh token is an HttpOnly cookie
// scoped to /api/v1/auth (#51), so it is never readable from JavaScript.
const LEGACY_TOKEN_KEYS = ['token', 'refresh_token'];

let accessToken: string | null = null;
let refreshInFlight: Promise<boolean> | null = null;

// Drop tokens an older build left in localStorage so a deploy cleans them up.
if (typeof localStorage !== 'undefined') {
	for (const key of LEGACY_TOKEN_KEYS) localStorage.removeItem(key);
}

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

export function setAccessToken(token: string | null): void {
	accessToken = token;
}

export function getAccessToken(): string | null {
	return accessToken;
}

export function clearSession(): void {
	accessToken = null;
}

async function performRefresh(): Promise<boolean> {
	try {
		// The browser sends the HttpOnly cookie; API clients can still send a
		// body token, but the SPA never sees it.
		const res = await fetch(`${API_ROOT}/auth/refresh`, {
			method: 'POST',
			credentials: 'include',
			headers: { 'Content-Type': 'application/json' },
			body: '{}'
		});
		if (!res.ok) return false;
		const data = (await res.json()) as { token: string };
		setAccessToken(data.token);
		handlers.onTokenRefreshed?.(data.token);
		return true;
	} catch {
		return false;
	}
}

/** Serializes concurrent refreshes so a single token rotation is enough. */
async function refreshOnce(): Promise<boolean> {
	if (refreshInFlight) return refreshInFlight;
	refreshInFlight = performRefresh();
	try {
		return await refreshInFlight;
	} finally {
		refreshInFlight = null;
	}
}

// A request body can only be read once. Keep an unconsumed clone so a 401 retry
// can replay POST/PATCH requests after the original was sent.
const replayable = new WeakMap<Request, Request>();

const authMiddleware: Middleware = {
	onRequest({ request }) {
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

		const refreshed = await refreshOnce();
		if (!refreshed) {
			handlers.onSessionExpired?.();
			return response;
		}

		const retry = (replayable.get(request) ?? request).clone();
		const token = getAccessToken();
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
