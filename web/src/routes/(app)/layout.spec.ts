import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { http, server } from '../../test/server';
import { load } from './+layout';

const event = { url: new URL('http://localhost/sql?x=1') } as never;

describe('app layout guard', () => {
	beforeEach(() => {
		localStorage.clear();
	});

	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it('redirects to login when the session cannot be refreshed', async () => {
		server.use(
			http.get('/auth/me', ({ response }) =>
				response(401).json({ code: 'unauthorized', message: 'expired session' })
			),
			http.post('/auth/refresh', ({ response }) =>
				response(401).json({ code: 'unauthorized', message: 'expired session' })
			)
		);

		await expect(load(event)).rejects.toMatchObject({
			status: 307,
			location: '/login?next=%2Fsql%3Fx%3D1'
		});

		// Tokens are never persisted on the client (#51).
		expect(localStorage.getItem('token')).toBeNull();
		expect(localStorage.getItem('refresh_token')).toBeNull();
	});

	it('redirects to login when the API cannot be reached', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async () => {
				throw new TypeError('network down');
			})
		);

		await expect(load(event)).rejects.toMatchObject({
			status: 307,
			location: '/login?next=%2Fsql%3Fx%3D1'
		});
	});
});
