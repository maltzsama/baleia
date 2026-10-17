import { beforeEach, describe, expect, it } from 'vitest';
import { http, server } from '../../test/server';
import { load } from './+layout';

const event = { url: new URL('http://localhost/sql?x=1') } as never;

describe('app layout guard', () => {
	beforeEach(() => {
		localStorage.clear();
	});

	it('clears a rejected session before redirecting to login', async () => {
		localStorage.setItem('token', 'stale-token-from-another-deploy');
		localStorage.setItem('refresh_token', 'stale-refresh');
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

		// The login page must not see a token and bounce straight back to /sql.
		expect(localStorage.getItem('token')).toBeNull();
		expect(localStorage.getItem('refresh_token')).toBeNull();
	});
});
