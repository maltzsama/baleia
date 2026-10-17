import { fireEvent, render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';
import {
	makeBindingSupport,
	makeCatalog,
	makeConnection,
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
		http.post('/catalog-connections', ({ response }) => response(201).json(makeConnection())),
		http.post('/engines', ({ response }) =>
			response(201).json(
				makeEngine({ id: ENGINE, name: 'analytics', kind: 'testengine', connection_id: 'x' })
			)
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
	await fireEvent.click(screen.getByRole('checkbox', { name: /Query it directly/ }));
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
});
