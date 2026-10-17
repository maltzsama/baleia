import { describe, expect, it } from 'vitest';
import { can, canGlobal } from './can';
import type { AuthUser } from '../stores/auth.svelte';

describe('can', () => {
	it('returns true when the resource carries the action', () => {
		expect(can({ allowed_actions: ['view', 'update'] }, 'update')).toBe(true);
	});

	it('returns false when the action is absent', () => {
		expect(can({ allowed_actions: ['view'] }, 'delete')).toBe(false);
	});

	it('returns false for an absent resource or action list', () => {
		expect(can(null, 'view')).toBe(false);
		expect(can({}, 'view')).toBe(false);
	});
});

describe('canGlobal', () => {
	const user: AuthUser = {
		id: 'u1',
		name: 'Ada',
		email: 'ada@example.com',
		role: 'admin',
		auth_provider: 'password',
		created_at: '2026-01-01T00:00:00Z',
		global_actions: ['manage_users']
	};

	it('reads global actions from /auth/me', () => {
		expect(canGlobal(user, 'manage_users')).toBe(true);
		expect(canGlobal(user, 'view_system')).toBe(false);
	});

	it('returns false without a user', () => {
		expect(canGlobal(null, 'manage_users')).toBe(false);
	});
});
