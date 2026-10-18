import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import PasswordInput from './PasswordInput.svelte';

describe('PasswordInput', () => {
	it('toggles the input type and updates the accessible label', async () => {
		render(PasswordInput, { label: 'Password', value: 'secret' });

		const input = screen.getByLabelText('Password') as HTMLInputElement;
		const toggle = screen.getByRole('button', { name: 'Show password' });
		expect(input.type).toBe('password');
		expect(toggle.getAttribute('aria-pressed')).toBe('false');

		await fireEvent.click(toggle);
		expect(input.type).toBe('text');
		expect(toggle.getAttribute('aria-pressed')).toBe('true');
		expect(screen.getByRole('button', { name: 'Hide password' })).toBe(toggle);
		// Revealing never changes the value.
		expect(input.value).toBe('secret');

		await fireEvent.click(toggle);
		expect(input.type).toBe('password');
		expect(toggle.getAttribute('aria-pressed')).toBe('false');
	});

	it('uses a non-submitting button, so Enter on it cannot submit a form', () => {
		render(PasswordInput, { label: 'Password' });
		const toggle = screen.getByRole('button', { name: 'Show password' });
		expect(toggle.getAttribute('type')).toBe('button');
	});

	it('returns focus to the field after toggling', async () => {
		render(PasswordInput, { label: 'Password' });
		const input = screen.getByLabelText('Password') as HTMLInputElement;
		await fireEvent.click(screen.getByRole('button', { name: 'Show password' }));
		expect(document.activeElement).toBe(input);
	});
});
