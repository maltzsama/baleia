import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import { makeProvider } from '../../test/factories';
import ProviderForm from './ProviderForm.svelte';

describe('ProviderForm', () => {
	it('renders the fields of the provider config schema', () => {
		render(ProviderForm, { props: { provider: makeProvider() } });

		expect(screen.getByLabelText('Host *')).toBeTruthy();
		expect(screen.getByLabelText('Port')).toBeTruthy();
		expect(screen.getByLabelText('Database *')).toBeTruthy();
		expect(screen.getByLabelText('Password *')).toBeTruthy();
	});

	it('never prefills a secret and renders it as a password input', () => {
		render(ProviderForm, { props: { provider: makeProvider() } });

		const secret = screen.getByLabelText('Password *') as HTMLInputElement;
		expect(secret.type).toBe('password');
		expect(secret.value).toBe('');
	});

	it('renders select and bool fields from the schema', () => {
		const provider = makeProvider({
			config_schema: [
				{
					key: 'mode',
					label: 'SSL mode',
					type: 'select',
					required: false,
					secret: false,
					options: ['disable', 'require'],
					default: 'disable'
				},
				{ key: 'tls', label: 'Verify TLS', type: 'bool', required: false, secret: false }
			]
		});
		render(ProviderForm, { props: { provider } });

		expect((screen.getByLabelText('SSL mode') as HTMLSelectElement).tagName).toBe('SELECT');
		expect((screen.getByLabelText('Verify TLS') as HTMLInputElement).type).toBe('checkbox');
	});

	it('turns a secret into a reference input when the toggle is used', async () => {
		render(ProviderForm, { props: { provider: makeProvider(), allowReferences: true } });

		const secret = screen.getByLabelText('Password *') as HTMLInputElement;
		expect(secret.type).toBe('password');

		await fireEvent.click(screen.getByLabelText('Use an env/file reference'));
		expect(secret.type).toBe('text');
		expect(secret.placeholder).toBe('@baleia-secret[env:NAME]');
	});
});
