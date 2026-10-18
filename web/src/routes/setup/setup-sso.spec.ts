import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import SetupPage from './+page.svelte';

describe('setup wizard', () => {
	it('has no inert SSO step', () => {
		render(SetupPage);
		expect(screen.getByText('Admin account')).toBeTruthy();
		expect(screen.queryByText(/single sign-on|SSO/i)).toBeNull();
	});
});
