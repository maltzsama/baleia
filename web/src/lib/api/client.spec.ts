import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

function json(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

function urlOf(input: RequestInfo | URL): string {
	return input instanceof Request ? input.url : String(input);
}

describe('api client', () => {
	// The tests import the client fresh (resetModules). Its first import
	// transforms the module and its generated types, which under a loaded
	// full run took more than a test's 5 s budget: pay it once, here.
	beforeAll(async () => {
		await import('./client');
	}, 30_000);

	beforeEach(() => {
		vi.resetModules();
		localStorage.clear();
	});

	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it('refreshes from the HttpOnly cookie on 401 and retries the request', async () => {
		let workspaceCalls = 0;
		const fetchMock = vi.fn(
			async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
				const url = urlOf(input);
				if (url.includes('/auth/refresh')) {
					// The SPA never sends the refresh token in the body; the
					// browser attaches the HttpOnly cookie instead (#51).
					expect(init?.credentials).toBe('include');
					expect(new Headers(init?.headers).get('X-Requested-With')).toBe('baleia');
					expect(String(init?.body)).not.toContain('refresh_token');
					return json({ token: 'access-2', expires_at: '2030-01-01T00:00:00Z' });
				}
				if (url.includes('/workspaces')) {
					workspaceCalls += 1;
					if (workspaceCalls === 1) {
						return json({ code: 'unauthorized', message: 'expired' }, 401);
					}
					return json([{ id: 'ws-1' }]);
				}
				return json({}, 404);
			}
		);
		vi.stubGlobal('fetch', fetchMock);

		const { client, setAccessToken } = await import('./client');
		setAccessToken('access-1');
		const { data } = await client.GET('/workspaces');

		expect(data).toEqual([{ id: 'ws-1' }]);
		expect(fetchMock.mock.calls.some(([input]) => urlOf(input).includes('/auth/refresh'))).toBe(
			true
		);
		expect(workspaceCalls).toBe(2);
	});

	it('emits onSessionExpired when the refresh fails', async () => {
		const onSessionExpired = vi.fn();
		vi.stubGlobal(
			'fetch',
			vi.fn(async (input: RequestInfo | URL) => {
				const url = urlOf(input);
				if (url.includes('/auth/refresh')) return json({}, 401);
				return json({ code: 'unauthorized', message: 'expired' }, 401);
			})
		);

		const { client, configureAuth, setAccessToken } = await import('./client');
		configureAuth({ onSessionExpired });
		setAccessToken('access-1');
		await client.GET('/workspaces');

		expect(onSessionExpired).toHaveBeenCalledOnce();
	});

	describe('apiFetch (#1034)', () => {
		it('refreshes on 401 and retries once with the new token', async () => {
			const seen: (string | null)[] = [];
			vi.stubGlobal(
				'fetch',
				vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
					const url = urlOf(input);
					if (url.includes('/auth/refresh')) {
						return json({ token: 'access-2', expires_at: '2030-01-01T00:00:00Z' });
					}
					seen.push(new Headers(init?.headers).get('Authorization'));
					return seen.length === 1 ? json({ code: 'unauthorized' }, 401) : json({ ok: true });
				})
			);
			const onTokenRefreshed = vi.fn();
			const { apiFetch, configureAuth, setAccessToken } = await import('./client');
			configureAuth({ onTokenRefreshed });
			setAccessToken('access-1');

			const response = await apiFetch('/results/1');

			expect(response.status).toBe(200);
			expect(seen).toEqual(['Bearer access-1', 'Bearer access-2']);
			expect(onTokenRefreshed).toHaveBeenCalledWith('access-2');
		});

		it('returns the 401 and signals expiry when the refresh fails', async () => {
			let calls = 0;
			vi.stubGlobal(
				'fetch',
				vi.fn(async (input: RequestInfo | URL) => {
					if (urlOf(input).includes('/auth/refresh')) return json({}, 401);
					calls += 1;
					return json({ code: 'unauthorized' }, 401);
				})
			);
			const onSessionExpired = vi.fn();
			const { apiFetch, configureAuth, setAccessToken } = await import('./client');
			configureAuth({ onSessionExpired });
			setAccessToken('access-1');

			const response = await apiFetch('/results/1');

			expect(response.status).toBe(401);
			expect(calls).toBe(1);
			expect(onSessionExpired).toHaveBeenCalledOnce();
		});

		it('does not refresh when the response is not a 401', async () => {
			const fetchMock = vi.fn(async () => json({ code: 'unavailable' }, 503));
			vi.stubGlobal('fetch', fetchMock);
			const { apiFetch, setAccessToken } = await import('./client');
			setAccessToken('access-1');

			const response = await apiFetch('/results/1');

			expect(response.status).toBe(503);
			expect(fetchMock).toHaveBeenCalledOnce();
		});
	});

	it('keeps access tokens out of localStorage', async () => {
		const { setAccessToken, getAccessToken, clearSession } = await import('./client');
		setAccessToken('access-1');
		expect(getAccessToken()).toBe('access-1');
		expect(localStorage.getItem('token')).toBeNull();
		expect(localStorage.getItem('refresh_token')).toBeNull();
		clearSession();
		expect(getAccessToken()).toBeNull();
	});
});
