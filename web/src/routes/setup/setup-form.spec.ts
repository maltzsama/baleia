import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import { http, server } from '../../test/server';
import SetupPage from './+page.svelte';

async function fill() {
	await fireEvent.input(screen.getByLabelText('First name'), { target: { value: 'Ada' } });
	await fireEvent.input(screen.getByLabelText('Email'), { target: { value: 'ada@example.com' } });
	await fireEvent.input(screen.getByLabelText('Password'), { target: { value: 'correct-horse' } });
	await fireEvent.input(screen.getByLabelText('Confirm password'), {
		target: { value: 'correct-horse' }
	});
}

// #484: Enter submits the admin step once, and errors are announced
// without losing what was typed.
describe('setup admin step', () => {
	it('submits on Enter once, even while the request is slow', async () => {
		let calls = 0;
		let release: () => void = () => {};
		const gate = new Promise<void>((resolve) => (release = resolve));
		server.use(
			http.post('/setup/admin', async ({ response }) => {
				calls++;
				await gate;
				return response(409).json({ code: 'conflict', message: 'already set up' });
			})
		);
		render(SetupPage);
		await fill();

		const email = screen.getByLabelText('Email');
		await fireEvent.submit(email.closest('form')!);
		await fireEvent.submit(email.closest('form')!);
		release();

		expect(await screen.findByRole('alert')).toBeTruthy();
		expect(calls).toBe(1);
		expect((screen.getByLabelText('Email') as HTMLInputElement).value).toBe('ada@example.com');
	});

	it('announces a validation error', async () => {
		render(SetupPage);
		await fireEvent.submit(screen.getByLabelText('Email').closest('form')!);
		await waitFor(() => expect(screen.getByRole('alert')).toBeTruthy());
	});
});
