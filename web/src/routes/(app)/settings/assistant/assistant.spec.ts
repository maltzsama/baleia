import { fireEvent, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import type { components } from '$lib/api/schema';
import { http, server } from '../../../../test/server';
import { makeMe } from '../../../../test/factories';
import { renderPage } from '../../../../test/render';
import AssistantPage from './+page.svelte';

type Settings = components['schemas']['AssistantSettings'];

const settings: Settings = {
	enabled: false,
	provider: null,
	base_url: null,
	model: null,
	api_key_set: false,
	data_sharing: 'metadata_only',
	max_turn_seconds: 180,
	max_tool_calls_per_turn: 10,
	max_output_tokens_per_turn: 4096,
	daily_token_budget_per_user: 500000
};

describe('assistant settings', () => {
	beforeEach(() => {
		localStorage.clear();
		window.history.replaceState(null, '', '/settings/assistant');
	});

	it('saves the provider and never re-sends an empty key', async () => {
		let saved: Record<string, unknown> | null = null;
		server.use(
			http.get('/admin/assistant/settings', ({ response }) => response(200).json(settings)),
			http.put('/admin/assistant/settings', async ({ request, response }) => {
				saved = (await request.json()) as Record<string, unknown>;
				return response(200).json({ ...settings, ...saved, api_key_set: true });
			})
		);
		renderPage(AssistantPage, { me: makeMe(), url: '/settings/assistant' });
		await screen.findByRole('heading', { name: 'Provider' });

		await fireEvent.click(screen.getByLabelText('Enable the assistant'));
		await fireEvent.change(screen.getByLabelText('Provider'), {
			target: { value: 'anthropic' }
		});
		await fireEvent.input(screen.getByLabelText('Model'), {
			target: { value: 'claude-test' }
		});
		await fireEvent.input(screen.getByLabelText('API key'), {
			target: { value: 'sk-once' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Save settings' }));

		await waitFor(() =>
			expect(saved).toMatchObject({
				enabled: true,
				provider: 'anthropic',
				model: 'claude-test',
				api_key: 'sk-once',
				data_sharing: 'metadata_only'
			})
		);
	});
});
