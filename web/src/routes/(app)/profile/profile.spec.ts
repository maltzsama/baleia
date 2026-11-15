import { screen } from '@testing-library/svelte';
import { tick } from 'svelte';
import { describe, expect, it } from 'vitest';
import { authStore } from '../../../lib/stores/auth.svelte';
import { makeMe } from '../../../test/factories';
import { renderPage } from '../../../test/render';
import Page from './+page.svelte';

describe('profile page', () => {
	// The page mounts before /auth/me resolves on a cold load, so it must not
	// keep the empty values it was created with (#1078).
	it('fills the fields when the user arrives after the page mounts', async () => {
		authStore.set(null);
		renderPage(Page);

		authStore.set(makeMe({ name: 'Ada Lovelace', email: 'ada@example.com' }));
		await tick();

		expect((screen.getByLabelText('Name') as HTMLInputElement).value).toBe('Ada Lovelace');
		expect((screen.getByLabelText('Email') as HTMLInputElement).value).toBe('ada@example.com');
	});

	// The API sends auth_provider local|oidc|ldap; the page compared it with
	// "password", so no account could change its e-mail or password.
	it('lets a local account edit its e-mail and change its password', () => {
		renderPage(Page, { me: makeMe({ auth_provider: 'local' }) });
		expect((screen.getByLabelText('Email') as HTMLInputElement).disabled).toBe(false);
		expect(screen.getByRole('button', { name: /change password/i })).toBeTruthy();
	});

	it('leaves e-mail and password to the directory for an SSO account', () => {
		renderPage(Page, { me: makeMe({ auth_provider: 'oidc' }) });
		expect((screen.getByLabelText('Email') as HTMLInputElement).disabled).toBe(true);
		expect(screen.queryByRole('button', { name: /change password/i })).toBeNull();
	});
});
