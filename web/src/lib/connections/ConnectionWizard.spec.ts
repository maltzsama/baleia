import { fireEvent, render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import {
	makeBindingSupport,
	makeCatalog,
	makeCategory,
	makeCreatedConnection,
	makeEngine,
	makeProvider,
	makeTestResult,
	makeWorkspace
} from '../../test/factories';
import { http, server } from '../../test/server';
import ConnectionWizard from './ConnectionWizard.svelte';

const ENGINE = '00000000-0000-0000-0000-000000000004';
const OTHER_CONNECTION = '00000000-0000-0000-0000-000000000009';

function mockApi() {
	server.use(
		http.get('/connection-providers', ({ response }) => response(200).json([makeProvider()])),
		http.get('/connection-categories', ({ response }) => response(200).json([makeCategory()])),
		http.get('/engines', ({ response }) =>
			response(200).json([
				makeEngine({
					id: ENGINE,
					name: 'warehouse',
					kind: 'testengine',
					connection_id: OTHER_CONNECTION
				})
			])
		),
		http.get('/engines/{id}/binding-support', ({ response }) =>
			response(200).json({ testdb: makeBindingSupport() })
		),
		http.post('/catalog-connections/test', ({ response }) => response(200).json(makeTestResult())),
		// The creation request registers everything: no second request creates
		// the engine or the catalogs, and an unmocked one fails the test.
		http.post('/catalog-connections', ({ response }) =>
			response(201).json(makeCreatedConnection())
		),
		http.get('/connections/{connection_id}/catalogs', ({ response }) =>
			response(200).json([makeCatalog()])
		),
		http.get('/workspaces', ({ response }) => response(200).json([makeWorkspace()]))
	);
}

async function fillConfiguration() {
	await fireEvent.click(await screen.findByText('Test Database'));
	await fireEvent.input(screen.getByLabelText('Connection name *'), {
		target: { value: 'analytics' }
	});
	await fireEvent.input(screen.getByLabelText('Host *'), { target: { value: 'db' } });
	await fireEvent.input(screen.getByLabelText('Database *'), {
		target: { value: 'analytics' }
	});
	await fireEvent.input(screen.getByLabelText('Password *'), {
		target: { value: 'secret' }
	});
}

async function reachTestStep() {
	await fillConfiguration();
	await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
	await fireEvent.click(screen.getByRole('radio', { name: /^Direct/ }));
	await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
}

describe('ConnectionWizard', () => {
	beforeEach(() => {
		mockApi();
	});

	it('creates a connection only after a successful test', async () => {
		render(ConnectionWizard);
		await reachTestStep();

		const create = screen.getByRole('button', { name: 'Create' }) as HTMLButtonElement;
		expect(create.disabled).toBe(true);

		await fireEvent.click(screen.getByRole('button', { name: 'Test connection' }));
		expect(await screen.findByText('Tested')).toBeTruthy();
		expect(create.disabled).toBe(false);

		await fireEvent.click(create);
		expect(await screen.findByText('Created')).toBeTruthy();
		expect(screen.getByRole('heading', { name: 'analytics' })).toBeTruthy();
	});

	it('invalidates a successful test when the settings change', async () => {
		render(ConnectionWizard);
		await reachTestStep();

		await fireEvent.click(screen.getByRole('button', { name: 'Test connection' }));
		expect(await screen.findByText('Tested')).toBeTruthy();

		await fireEvent.click(screen.getByRole('button', { name: 'Back' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Back' }));
		await fireEvent.input(screen.getByLabelText('Connection name *'), {
			target: { value: 'analytics-2' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));

		const create = screen.getByRole('button', { name: 'Create' }) as HTMLButtonElement;
		expect(create.disabled).toBe(true);
		expect(screen.queryByText('Tested')).toBeNull();
	});

	it('tests the bind with the warehouse the catalog will be created with', async () => {
		let sent: { intents?: string[]; warehouse?: string } | undefined;
		server.use(
			http.post('/catalog-connections/test', async ({ request, response }) => {
				sent = await request.json();
				return response(200).json(makeTestResult());
			})
		);
		render(ConnectionWizard);
		await fillConfiguration();
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.click(screen.getByRole('radio', { name: /Through a query engine/ }));
		await fireEvent.click(await screen.findByRole('checkbox', { name: /warehouse/ }));
		await fireEvent.input(screen.getByLabelText('Database name'), { target: { value: 'sales' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));

		await fireEvent.click(screen.getByRole('button', { name: 'Test connection' }));
		expect(await screen.findByText('Tested')).toBeTruthy();
		expect(sent?.intents).toContain(`bind:${ENGINE}`);
		expect(sent?.warehouse).toBe('sales');
	});

	it('registers a direct connection with one request that names no engine', async () => {
		const bodies: Record<string, unknown>[] = [];
		server.use(
			http.post('/catalog-connections', async ({ request, response }) => {
				bodies.push((await request.json()) as Record<string, unknown>);
				return response(201).json(
					makeCreatedConnection({
						access_mode: 'direct',
						engine: makeEngine({ name: 'analytics', class: 'direct' }),
						catalogs: [makeCatalog()]
					})
				);
			})
		);
		render(ConnectionWizard);
		await reachTestStep();
		await fireEvent.click(screen.getByRole('button', { name: 'Test connection' }));
		expect(await screen.findByText('Tested')).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Create' }));
		expect(await screen.findByText('Created')).toBeTruthy();

		expect(bodies).toHaveLength(1);
		expect(bodies[0].access_mode).toBe('direct');
		expect(bodies[0].engine_ids).toBeUndefined();
		expect(bodies[0].catalog_registration).toBeUndefined();
	});

	it('registers a federated connection with its engines and catalogs in one request', async () => {
		const bodies: Record<string, unknown>[] = [];
		server.use(
			http.post('/catalog-connections', async ({ request, response }) => {
				bodies.push((await request.json()) as Record<string, unknown>);
				return response(201).json(
					makeCreatedConnection({ access_mode: 'federated', catalogs: [makeCatalog()] })
				);
			})
		);
		render(ConnectionWizard);
		await fillConfiguration();
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.click(screen.getByRole('radio', { name: /Through a query engine/ }));
		await fireEvent.click(await screen.findByRole('checkbox', { name: /warehouse/ }));
		await fireEvent.input(screen.getByLabelText('Database name'), { target: { value: 'sales' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Test connection' }));
		expect(await screen.findByText('Tested')).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Create' }));
		expect(await screen.findByText('Created')).toBeTruthy();

		expect(bodies).toHaveLength(1);
		expect(bodies[0].access_mode).toBe('federated');
		expect(bodies[0].engine_ids).toEqual([ENGINE]);
		expect(bodies[0].catalog_registration).toMatchObject({ warehouse: 'sales' });
	});

	it('says so when a committed catalog is not applied by the engine yet', async () => {
		server.use(
			http.post('/catalog-connections', ({ response }) =>
				response(201).json(
					makeCreatedConnection({
						access_mode: 'federated',
						catalogs: [makeCatalog({ name: 'analytics', sync_status: 'error' })]
					})
				)
			)
		);
		render(ConnectionWizard);
		await fillConfiguration();
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.click(screen.getByRole('radio', { name: /Through a query engine/ }));
		await fireEvent.click(await screen.findByRole('checkbox', { name: /warehouse/ }));
		await fireEvent.input(screen.getByLabelText('Database name'), { target: { value: 'sales' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Test connection' }));
		expect(await screen.findByText('Tested')).toBeTruthy();
		await fireEvent.click(screen.getByRole('button', { name: 'Create' }));

		expect(await screen.findByText(/has not applied analytics yet/)).toBeTruthy();
		expect(screen.queryByText('The connection is ready.')).toBeNull();
		expect(screen.getByRole('link', { name: 'Review the connection' })).toBeTruthy();
	});

	it('does not continue a federated connection without an engine', async () => {
		render(ConnectionWizard);
		await fillConfiguration();
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		await fireEvent.click(screen.getByRole('radio', { name: /Through a query engine/ }));
		const next = screen.getByRole('button', { name: 'Continue' }) as HTMLButtonElement;
		expect(next.disabled).toBe(true);
		await fireEvent.click(await screen.findByRole('checkbox', { name: /warehouse/ }));
		expect(next.disabled).toBe(false);
	});
});
