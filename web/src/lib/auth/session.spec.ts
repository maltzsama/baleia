import { readFileSync } from 'node:fs';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

class FakeBroadcastChannel {
	static instances: FakeBroadcastChannel[] = [];
	messages: unknown[] = [];
	onmessage: ((event: MessageEvent) => void) | null = null;
	constructor(public name: string) {
		FakeBroadcastChannel.instances.push(this);
	}
	postMessage(data: unknown) {
		this.messages.push(data);
	}
	close() {
		/* no-op */
	}
}

function json(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

describe('session client', () => {
	beforeEach(() => {
		vi.resetModules();
		vi.stubGlobal('BroadcastChannel', FakeBroadcastChannel);
		FakeBroadcastChannel.instances = [];
		localStorage.clear();
		sessionStorage.clear();
	});

	afterEach(() => {
		vi.unstubAllGlobals();
		vi.restoreAllMocks();
	});

	it('serializes concurrent refreshes into one network call', async () => {
		const request = vi.fn((_name: string, callback: () => Promise<boolean>) => callback());
		Object.defineProperty(window.navigator, 'locks', {
			value: { request },
			configurable: true
		});
		const fetchMock = vi.fn(async () =>
			json({ token: 'access-2', expires_at: '2030-01-01T00:00:00Z' })
		);
		vi.stubGlobal('fetch', fetchMock);

		const { refresh } = await import('./session');
		const results = await Promise.all([refresh(true), refresh(true)]);

		expect(results).toEqual([true, true]);
		expect(request).toHaveBeenCalledWith('baleia-refresh', expect.any(Function));
		expect(fetchMock).toHaveBeenCalledTimes(1);
	});

	it('broadcasts logout and clears the in-memory token', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async () => new Response(null, { status: 204 }))
		);
		const { logout, setAccessToken, getAccessToken } = await import('./session');

		setAccessToken('access-1');
		await logout();

		expect(getAccessToken()).toBeNull();
		const channel = FakeBroadcastChannel.instances.at(-1);
		expect(channel?.messages).toContainEqual({ type: 'logout' });
	});

	it('never writes tokens to web storage', async () => {
		const source = readFileSync(`${process.cwd()}/src/lib/auth/session.ts`, 'utf8');
		expect(source).not.toMatch(/localStorage|sessionStorage/);

		const { setAccessToken, getAccessToken } = await import('./session');
		setAccessToken('secret-jwt');
		expect(getAccessToken()).toBe('secret-jwt');
		expect(localStorage.getItem('token')).toBeNull();
		expect(localStorage.getItem('refresh_token')).toBeNull();
		expect(sessionStorage.getItem('token')).toBeNull();
	});

	it('ignores an absolute next path', async () => {
		const { safeNext } = await import('./session');
		expect(safeNext('//evil.com')).toBe('/sql');
		expect(safeNext('https://evil.com')).toBe('/sql');
		expect(safeNext('/sql?tab=1')).toBe('/sql?tab=1');
		expect(safeNext(null, '/connections')).toBe('/connections');
	});

	it('reports anonymous when the refresh cookie is gone', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async () => json({ code: 'unauthorized' }, 401))
		);
		const { bootstrap } = await import('./session');
		await expect(bootstrap()).resolves.toBe('anonymous');
	});
});
