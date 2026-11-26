import { HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { http, server } from '../../test/server';
import { DataPolicyError, deletePolicy } from './api';

const id = '00000000-0000-0000-0000-0000000000d1';

describe('deletePolicy', () => {
	it('resolves on 204', async () => {
		server.use(http.delete('/data-policies/{id}', () => new HttpResponse(null, { status: 204 })));
		await expect(deletePolicy(id)).resolves.toBeUndefined();
	});

	// An empty error body leaves openapi-fetch's error undefined: the status
	// still makes it a failure, so no "deleted" toast follows.
	it('rejects a failure with an empty body', async () => {
		server.use(http.delete('/data-policies/{id}', () => new HttpResponse(null, { status: 500 })));
		await expect(deletePolicy(id)).rejects.toBeInstanceOf(DataPolicyError);
	});
});
