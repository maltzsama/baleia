// Session client (#263). The access token lives in memory only; the refresh
// token is the HttpOnly `baleia_refresh` cookie. Refreshes are serialized with
// the Web Locks API so two tabs never present the same rotating token at the
// same time, and logout propagates across tabs through BroadcastChannel.
import { API_ROOT } from '$lib/api/base';
import type { components } from '$lib/api/schema';

type LoginRequest = components['schemas']['LoginRequest'];
type LoginResponse = components['schemas']['LoginResponse'];
type RefreshResponse = components['schemas']['RefreshResponse'];
type UserPublic = components['schemas']['UserPublic'];

export type LoginResult =
	{ ok: true; user: UserPublic } | { ok: false; status: number; code: string; retryAfter?: number };

export type SessionHandlers = {
	/** Another tab logged out; clear this tab's state and go to /login. */
	onLogout?: () => void;
};

const REFRESH_LOCK = 'baleia-refresh';
const AUTH_CHANNEL = 'baleia-auth';
const DEFAULT_TTL_MS = 15 * 60 * 1000;

let accessToken: string | null = null;
let expiresAtMs = 0;
let refreshInFlight: Promise<boolean> | null = null;
let handlers: SessionHandlers = {};
let channel: BroadcastChannel | null = null;

export function configureSession(next: SessionHandlers): void {
	handlers = next;
}

function hasFreshToken(): boolean {
	return accessToken !== null && expiresAtMs > Date.now() + 5_000;
}

function setToken(token: string | null, expiresAt?: string): void {
	accessToken = token;
	if (!token) {
		expiresAtMs = 0;
		return;
	}
	const parsed = expiresAt ? Date.parse(expiresAt) : NaN;
	expiresAtMs = Number.isNaN(parsed) ? Date.now() + DEFAULT_TTL_MS : parsed;
}

export function setAccessToken(token: string | null): void {
	setToken(token);
}

export function getAccessToken(): string | null {
	return accessToken;
}

export function clear(): void {
	accessToken = null;
	expiresAtMs = 0;
}

function authChannel(): BroadcastChannel | null {
	if (typeof BroadcastChannel === 'undefined') return null;
	if (!channel) {
		channel = new BroadcastChannel(AUTH_CHANNEL);
		channel.onmessage = (event: MessageEvent) => {
			const message = event.data as { type?: string } | null;
			if (message?.type === 'logout') {
				clear();
				handlers.onLogout?.();
			}
		};
	}
	return channel;
}

type LockManagerLike = {
	request<T>(name: string, callback: () => Promise<T>): Promise<T>;
};

function withRefreshLock<T>(callback: () => Promise<T>): Promise<T> {
	const locks =
		typeof navigator !== 'undefined'
			? (navigator as unknown as { locks?: LockManagerLike }).locks
			: undefined;
	if (locks?.request) return locks.request(REFRESH_LOCK, callback);
	return callback();
}

async function performRefresh(): Promise<boolean> {
	try {
		const response = await fetch(`${API_ROOT}/auth/refresh`, {
			method: 'POST',
			credentials: 'include',
			headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'baleia' },
			body: '{}'
		});
		if (!response.ok) {
			clear();
			return false;
		}
		const data = (await response.json()) as RefreshResponse;
		setToken(data.token, data.expires_at);
		authChannel()?.postMessage({ type: 'refreshed', at: Date.now() });
		return true;
	} catch {
		return false;
	}
}

/**
 * Rotates the refresh cookie once, serialized across tabs. `force` is used
 * after a 401: the server rejected the token even if our clock still looks
 * valid, so the middleware must not trust the local expiry.
 */
export async function refresh(force = false): Promise<boolean> {
	if (!force && hasFreshToken()) return true;
	if (refreshInFlight) return refreshInFlight;

	refreshInFlight = withRefreshLock(async () => {
		// A tab that waited for the lock may already have a valid token.
		if (!force && hasFreshToken()) return true;
		return performRefresh();
	}).catch(() => false);

	try {
		return await refreshInFlight;
	} finally {
		refreshInFlight = null;
	}
}

/** Restores the session from the HttpOnly cookie on app boot. */
export async function bootstrap(): Promise<'authenticated' | 'anonymous'> {
	if (hasFreshToken()) return 'authenticated';
	return (await refresh()) ? 'authenticated' : 'anonymous';
}

function parseRetryAfter(raw: string | null): number | undefined {
	if (!raw) return undefined;
	let wait = parseInt(raw, 10);
	if (Number.isNaN(wait)) {
		const at = Date.parse(raw);
		wait = Number.isNaN(at) ? NaN : Math.ceil((at - Date.now()) / 1000);
	}
	if (Number.isNaN(wait)) return undefined;
	return Math.min(Math.max(wait, 0), 3600);
}

function codeFor(status: number, body: unknown): string {
	const code = (body as { code?: unknown } | null)?.code;
	if (typeof code === 'string' && code) return code;
	switch (status) {
		case 401:
			return 'invalid_credentials';
		case 403:
			return 'forbidden';
		case 429:
			return 'rate_limited';
		case 0:
			return 'network_error';
		default:
			return `http_${status}`;
	}
}

/**
 * Authenticates and stores the access token in memory. Local and LDAP logins
 * share this path; the caller decides which fields to send.
 */
export async function login(request: LoginRequest): Promise<LoginResult> {
	try {
		const response = await fetch(`${API_ROOT}/auth/login`, {
			method: 'POST',
			credentials: 'include',
			headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'baleia' },
			body: JSON.stringify(request)
		});
		const body = await response.json().catch(() => null);
		if (!response.ok) {
			return {
				ok: false,
				status: response.status,
				code: codeFor(response.status, body),
				retryAfter: parseRetryAfter(response.headers.get('Retry-After'))
			};
		}
		const data = body as LoginResponse;
		setToken(data.token, data.expires_at);
		authChannel()?.postMessage({ type: 'refreshed', at: Date.now() });
		return { ok: true, user: data.user };
	} catch {
		return { ok: false, status: 0, code: 'network_error' };
	}
}

/** Ends the session in this tab and tells every other tab to do the same. */
export async function logout(): Promise<void> {
	try {
		await fetch(`${API_ROOT}/auth/logout`, {
			method: 'POST',
			credentials: 'include',
			headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'baleia' },
			body: '{}'
		});
	} catch {
		/* best effort: log out locally even if the API is unreachable */
	}
	clear();
	authChannel()?.postMessage({ type: 'logout' });
}

/** Only same-origin relative paths are honoured after login (#263). */
export function safeNext(next: string | null | undefined, fallback = '/sql'): string {
	if (!next || !next.startsWith('/') || next.startsWith('//')) return fallback;
	return next;
}
