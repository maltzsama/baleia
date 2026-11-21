import { expect, test } from './fixtures';

// #1064: a Kafka cluster is registered through the wizard with its topic set,
// the engine binds the catalog, and the seeded topic is read from the
// workbench as a table of the connector's internal columns. The broker and
// its e2e_orders topic (three JSON messages) come from
// deploy/dev/sources/kafka.yaml, which `make e2e` starts.
const BROKER = 'src-kafka.baleia-dev.svc.cluster.local:9092';

test('@issue-1064 a Kafka topic is registered and read from the workbench', async ({
	page,
	loginAs
}) => {
	test.setTimeout(180_000);
	// Each run registers its own connection, so the spec depends on no other.
	const suffix = Date.now().toString(36);
	const catalog = `e2e_kafka_${suffix}`;

	// The catalog is shared with a workspace of its own, so no other spec's
	// workspace gains a catalog. The token is read from the page's own login
	// response: a second login counts against the 10/min limit (#391).
	let token = '';
	await page.route('**/api/v1/auth/login', async (route) => {
		const response = await route.fetch();
		if (response.ok()) token = (await response.json()).token;
		await route.fulfill({ response });
	});
	await loginAs('admin');
	await page.unroute('**/api/v1/auth/login');
	const workspaceName = `Kafka e2e ${suffix}`;
	const created = await page.request.post('/api/v1/workspaces', {
		headers: { Authorization: `Bearer ${token}` },
		data: { name: workspaceName, type: 'personal' }
	});
	expect(created.status(), await created.text()).toBe(201);
	const workspace: { id: string } = await created.json();

	await page.goto('/connections/new');
	await page.locator('[data-testid="provider-card"]', { hasText: 'Apache Kafka' }).click();
	await expect(page.getByText('Step 2 of 4')).toBeVisible();
	await page.getByLabel('Connection name *').fill(`kafka-${suffix}`);
	await page.getByLabel('Bootstrap brokers *').fill(BROKER);
	await page.getByRole('button', { name: 'Continue' }).click();

	// Kafka is federated only: the mode is chosen, an engine and the topics
	// are not.
	await page.locator('main input[type="checkbox"]').first().check();
	await page.getByLabel('Warehouse or catalog name').fill(catalog);
	await page.getByLabel('Topics *').fill('e2e_orders');
	await page.getByRole('button', { name: 'Continue' }).click();

	// The bind check creates a short-lived catalog with the topics in the engine.
	await page.getByRole('button', { name: 'Test connection' }).click();
	await expect(page.getByText('Tested')).toBeVisible({ timeout: 60_000 });
	await page.getByRole('button', { name: 'Create', exact: true }).click();
	await expect(page.getByText('Created')).toBeVisible({ timeout: 60_000 });

	await page.getByLabel('Workspace').selectOption({ label: workspaceName });
	await page.getByRole('button', { name: 'Grant', exact: true }).click();
	await expect(page.getByText(/1 grant/)).toBeVisible();

	await page.goto(`/sql?ws=${workspace.id}`);
	const catalogSelect = page.getByTestId('catalog-select');
	const option = catalogSelect.locator('option', { hasText: catalog });
	await expect(option).toHaveCount(1, { timeout: 30_000 });
	await catalogSelect.selectOption({ label: (await option.textContent())?.trim() ?? catalog });

	const run = async (sql: string) => {
		await page.locator('.cm-content').click();
		await page.keyboard.press('Control+A');
		await page.keyboard.type(sql);
		await page.getByRole('button', { name: /^Run/ }).click();
	};

	// A topic is a table of the internal columns; its JSON is read with Trino's
	// JSON functions.
	await run(
		`SELECT count(*) AS n, sum(CAST(json_extract_scalar(_message, '$.id') AS integer)) AS ids FROM ${catalog}."default".e2e_orders`
	);
	await expect(page.getByText('Finished', { exact: true })).toBeVisible({ timeout: 60_000 });
	await expect(page.getByRole('gridcell', { name: '3', exact: true })).toBeVisible();
	await expect(page.getByRole('gridcell', { name: '6', exact: true })).toBeVisible();

	// Baleia does not write to Kafka: without table description files the
	// connector has no encoder, so an INSERT fails and the topic is unchanged.
	await run(`INSERT INTO ${catalog}."default".e2e_orders (_message) VALUES ('{"id":4}')`);
	await expect(page.getByText(/failed/i).first()).toBeVisible({ timeout: 60_000 });
	await expect(page.getByRole('gridcell', { name: '3', exact: true })).toHaveCount(0);
	await run(`SELECT count(*) AS n FROM ${catalog}."default".e2e_orders`);
	await expect(page.getByRole('gridcell', { name: '3', exact: true })).toBeVisible({
		timeout: 60_000
	});
});
