import { fireEvent, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import type { components } from '$lib/api/schema';
import { http, server } from '../../../../test/server';
import { makeEngine, makeMe } from '../../../../test/factories';
import { renderPage } from '../../../../test/render';
import WorkloadPage from './+page.svelte';

type WorkloadRule = components['schemas']['WorkloadRule'];
type EngineQueues = components['schemas']['EngineQueues'];

const ENGINE = '00000000-0000-0000-0000-000000000004';

const ruleA: WorkloadRule = {
	id: '00000000-0000-0000-0000-0000000000a1',
	position: 1,
	enabled: true,
	match: { origin: 'retriever' },
	class: 'background',
	max_class: null,
	weight: 2,
	created_at: '2026-01-01T00:00:00Z',
	updated_at: '2026-01-01T00:00:00Z'
};

const ruleB: WorkloadRule = {
	id: '00000000-0000-0000-0000-0000000000b2',
	position: 2,
	enabled: true,
	match: {},
	class: 'interactive',
	max_class: null,
	weight: 1,
	created_at: '2026-01-01T00:00:00Z',
	updated_at: '2026-01-01T00:00:00Z'
};

function makeQueues(overrides: Partial<components['schemas']['EngineQueue']> = {}): EngineQueues {
	return {
		engine_id: ENGINE,
		max_running_total: null,
		classes: [
			{
				class: 'interactive',
				max_running: 20,
				max_queued: 200,
				queue_timeout_ms: 300000,
				runtime_timeout_ms: 300000,
				running: 1,
				queued: 0,
				oldest_wait_ms: null,
				...overrides
			},
			{
				class: 'heavy',
				max_running: 3,
				max_queued: 50,
				queue_timeout_ms: 1800000,
				runtime_timeout_ms: 1800000,
				running: 0,
				queued: 0,
				oldest_wait_ms: null
			},
			{
				class: 'background',
				max_running: 2,
				max_queued: 100,
				queue_timeout_ms: 86400000,
				runtime_timeout_ms: 86400000,
				running: 0,
				queued: 0,
				oldest_wait_ms: null
			}
		]
	};
}

describe('settings workload page', () => {
	beforeEach(() => {
		localStorage.clear();
		window.history.replaceState(null, '', '/settings/workload');
	});

	it('renders the capacities, the rules and the live queue', async () => {
		server.use(
			http.get('/engines', ({ response }) => response(200).json([makeEngine({ id: ENGINE })])),
			http.get('/workload-rules', ({ response }) => response(200).json([ruleA, ruleB])),
			http.get('/engines/{id}/queues', ({ response }) => response(200).json(makeQueues()))
		);
		const me = makeMe();
		me.global_actions = [...(me.global_actions ?? []), 'manage_workload'];
		renderPage(WorkloadPage, { me, url: '/settings/workload' });

		expect(await screen.findByText('Interactive')).toBeTruthy();
		const maxRunning = (await screen.findByLabelText(
			'Max running Interactive'
		)) as HTMLInputElement;
		expect(maxRunning.value).toBe('20');
		expect(screen.getByText(/Origin: Retriever/)).toBeTruthy();
		expect(screen.getAllByText('Background').length).toBeGreaterThan(0);
	});

	it('reorders a rule by patching the new positions', async () => {
		const patches: { id: string; body: Record<string, unknown> }[] = [];
		server.use(
			http.get('/engines', ({ response }) => response(200).json([makeEngine({ id: ENGINE })])),
			http.get('/workload-rules', ({ response }) => response(200).json([ruleA, ruleB])),
			http.get('/engines/{id}/queues', ({ response }) => response(200).json(makeQueues())),
			http.patch('/workload-rules/{rule_id}', async ({ params, request, response }) => {
				const body = (await request.json()) as Record<string, unknown>;
				patches.push({ id: params.rule_id, body });
				const rule = params.rule_id === ruleA.id ? ruleA : ruleB;
				return response(200).json({ ...rule, ...body });
			})
		);
		const me = makeMe();
		me.global_actions = [...(me.global_actions ?? []), 'manage_workload'];
		renderPage(WorkloadPage, { me, url: '/settings/workload' });

		const down = await screen.findAllByRole('button', { name: 'Move down' });
		await fireEvent.click(down[0]);

		await waitFor(() => expect(patches.length).toBeGreaterThanOrEqual(2));
		expect(patches).toContainEqual({
			id: ruleA.id,
			body: expect.objectContaining({ position: 2 })
		});
		expect(patches).toContainEqual({
			id: ruleB.id,
			body: expect.objectContaining({ position: 1 })
		});
	});

	it('asks before closing a class that still has queued runs', async () => {
		let saved = 0;
		server.use(
			http.get('/engines', ({ response }) => response(200).json([makeEngine({ id: ENGINE })])),
			http.get('/workload-rules', ({ response }) => response(200).json([])),
			http.get('/engines/{id}/queues', ({ response }) =>
				response(200).json(makeQueues({ queued: 2 }))
			),
			http.put('/engines/{id}/queues', ({ response }) => {
				saved++;
				return response(200).json(makeQueues());
			})
		);
		const me = makeMe();
		me.global_actions = [...(me.global_actions ?? []), 'manage_workload'];
		renderPage(WorkloadPage, { me, url: '/settings/workload' });

		const maxRunning = await screen.findByLabelText('Max running Interactive');
		await fireEvent.input(maxRunning, { target: { value: '0' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Save capacities' }));

		expect(
			await screen.findByText(/Closing it \(max running 0\) leaves them waiting/)
		).toBeTruthy();
		expect(saved).toBe(0);
	});
});

describe('Trino resource-group handover (#635)', () => {
	async function saveFor(kind: string): Promise<Record<string, unknown>> {
		let body: Record<string, unknown> = {};
		server.use(
			http.get('/engines', ({ response }) =>
				response(200).json([makeEngine({ id: ENGINE, kind })])
			),
			http.get('/workload-rules', ({ response }) => response(200).json([])),
			http.get('/engines/{id}/queues', ({ response }) =>
				response(200).json({ ...makeQueues(), trino_resource_groups_enabled: false })
			),
			http.put('/engines/{id}/queues', async ({ request, response }) => {
				body = (await request.json()) as Record<string, unknown>;
				return response(200).json(makeQueues());
			})
		);
		const me = makeMe();
		me.global_actions = [...(me.global_actions ?? []), 'manage_workload'];
		renderPage(WorkloadPage, { me, url: '/settings/workload' });

		await screen.findByLabelText('Max running Interactive');
		await fireEvent.click(screen.getByRole('button', { name: 'Save capacities' }));
		await waitFor(() => expect(body.classes).toBeTruthy());
		return body;
	}

	it('offers and sends the switch for a Trino engine', async () => {
		const body = await saveFor('trino');
		expect(screen.getByRole('checkbox', { name: /resource groups/i })).toBeTruthy();
		expect(body.trino_resource_groups_enabled).toBe(false);
	});

	it('hides and omits the switch for any other engine', async () => {
		const body = await saveFor('postgresql');
		expect(screen.queryByRole('checkbox', { name: /resource groups/i })).toBeNull();
		expect('trino_resource_groups_enabled' in body).toBe(false);
	});
});

it('shows an error when the queues cannot be loaded', async () => {
	server.use(
		http.get('/engines', ({ response }) => response(200).json([makeEngine({ id: ENGINE })])),
		http.get('/workload-rules', ({ response }) => response(200).json([])),
		http.get('/engines/{id}/queues', ({ response }) =>
			response(403).json({ code: 'forbidden', message: 'not allowed' })
		)
	);
	const me = makeMe();
	me.global_actions = [...(me.global_actions ?? []), 'manage_workload'];
	renderPage(WorkloadPage, { me, url: '/settings/workload' });

	expect(await screen.findByText('Could not load the workload settings')).toBeTruthy();
});
