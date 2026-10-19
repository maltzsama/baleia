<script lang="ts">
	import { onMount } from 'svelte';
	import type { components } from '$lib/api/schema';
	import { apiErrorMessage, client } from '$lib/api/client';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import Checkbox from '$lib/ui/Checkbox.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import { toasts } from '$lib/ui/toast.svelte';

	type Settings = components['schemas']['AssistantSettings'];
	type TestResult = components['schemas']['AssistantTestResult'];

	const providerOptions = [
		{ value: 'anthropic', label: 'Anthropic' },
		{ value: 'openai_compatible', label: 'OpenAI-compatible' }
	];
	const sharingOptions = [
		{ value: 'metadata_only', label: 'Metadata only (recommended)' },
		{ value: 'metadata_and_samples', label: 'Metadata + result samples' }
	];

	let loading = $state(true);
	let error = $state('');
	let settings = $state<Settings | null>(null);

	let enabled = $state(false);
	let provider = $state('');
	let baseUrl = $state('');
	let model = $state('');
	let apiKey = $state('');
	let dataSharing = $state('metadata_only');
	let maxTurnSeconds = $state('180');
	let maxToolCalls = $state('10');
	let maxOutputTokens = $state('4096');
	let dailyBudget = $state('500000');

	let saving = $state(false);
	let saveError = $state('');
	let testing = $state(false);
	let testResult = $state<TestResult | null>(null);

	async function load() {
		loading = true;
		error = '';
		try {
			const { data, error: err } = await client.GET('/admin/assistant/settings');
			if (err || !data) {
				error = apiErrorMessage(err, 'Could not load assistant settings.');
				return;
			}
			settings = data;
			enabled = data.enabled;
			provider = data.provider ?? '';
			baseUrl = data.base_url ?? '';
			model = data.model ?? '';
			dataSharing = data.data_sharing;
			maxTurnSeconds = String(data.max_turn_seconds);
			maxToolCalls = String(data.max_tool_calls_per_turn);
			maxOutputTokens = String(data.max_output_tokens_per_turn);
			dailyBudget = String(data.daily_token_budget_per_user);
		} catch {
			error = 'Could not load assistant settings.';
		} finally {
			loading = false;
		}
	}

	onMount(load);

	async function save() {
		saving = true;
		saveError = '';
		try {
			const { data, error: err } = await client.PUT('/admin/assistant/settings', {
				body: {
					enabled,
					provider: provider ? (provider as 'anthropic' | 'openai_compatible') : undefined,
					base_url: baseUrl || undefined,
					model: model || undefined,
					api_key: apiKey || undefined,
					data_sharing: dataSharing as 'metadata_only' | 'metadata_and_samples',
					max_turn_seconds: Number(maxTurnSeconds),
					max_tool_calls_per_turn: Number(maxToolCalls),
					max_output_tokens_per_turn: Number(maxOutputTokens),
					daily_token_budget_per_user: Number(dailyBudget)
				}
			});
			if (err || !data) {
				saveError = apiErrorMessage(err, 'Could not save the settings.');
				return;
			}
			settings = data;
			apiKey = '';
			toasts.success('Assistant settings saved.');
		} finally {
			saving = false;
		}
	}

	async function test() {
		testing = true;
		testResult = null;
		try {
			const { data } = await client.POST('/admin/assistant/test');
			testResult = data ?? null;
		} finally {
			testing = false;
		}
	}
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="max-w-3xl">
		<h1 class="text-xl font-semibold tracking-[-0.3px]">Assistant</h1>
		<p class="mt-1 text-[13px] text-text-muted">
			The assistant is off by default. It answers only through the read-only MCP tools, under a
			per-user agent and the instance budgets.
		</p>

		{#if loading}
			<div class="mt-6 flex flex-col gap-3">
				<Skeleton height="6rem" />
				<Skeleton height="6rem" />
			</div>
		{:else if error}
			<div class="mt-6">
				<ErrorState title="Could not load settings" message={error} onretry={load} />
			</div>
		{:else}
			<div class="mt-6 flex flex-col gap-4">
				<section class="rounded-lg border border-border bg-bg-panel p-4">
					<div class="flex flex-wrap items-center justify-between gap-3">
						<div>
							<h2 class="text-[13px] font-semibold">Provider</h2>
							<p class="text-[12px] text-text-muted">
								The API process needs egress to the provider URL. Leave the key empty to keep the
								stored one.
							</p>
						</div>
						{#if settings?.api_key_set}<Badge tone="success">key set</Badge>{/if}
					</div>
					<div class="mt-3 grid gap-3 sm:grid-cols-2">
						<Checkbox bind:checked={enabled} label="Enable the assistant" />
						<Select
							label="Provider"
							bind:value={provider}
							placeholder="Choose a provider"
							options={providerOptions}
						/>
						<Input
							label="Base URL"
							bind:value={baseUrl}
							placeholder={provider === 'anthropic' ? 'https://api.anthropic.com' : 'https://…/v1'}
						/>
						<Input label="Model" bind:value={model} placeholder="claude-… or gpt-…" />
						<Input
							label="API key"
							type="password"
							bind:value={apiKey}
							placeholder={settings?.api_key_set ? 'Set — leave empty to keep' : 'Not set'}
						/>
						<Select label="Data sharing" bind:value={dataSharing} options={sharingOptions} />
					</div>
				</section>

				<section class="rounded-lg border border-border bg-bg-panel p-4">
					<h2 class="text-[13px] font-semibold">Limits</h2>
					<p class="text-[12px] text-text-muted">
						Bounds every turn; the daily budget is per user and counts input + output tokens.
					</p>
					<div class="mt-3 grid gap-3 sm:grid-cols-2">
						<Input label="Max turn seconds" type="number" bind:value={maxTurnSeconds} />
						<Input label="Max tool calls per turn" type="number" bind:value={maxToolCalls} />
						<Input label="Max output tokens per turn" type="number" bind:value={maxOutputTokens} />
						<Input label="Daily token budget per user" type="number" bind:value={dailyBudget} />
					</div>
				</section>

				{#if saveError}
					<p
						class="rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-[12px] text-danger"
					>
						{saveError}
					</p>
				{/if}
				<div class="flex flex-wrap items-center gap-2">
					<Button variant="primary" loading={saving} onclick={save}>Save settings</Button>
					<Button loading={testing} onclick={test}>Test connection</Button>
					{#if testResult}
						<span class="text-[12px] {testResult.ok ? 'text-success' : 'text-danger'}">
							{testResult.ok
								? `Connected in ${testResult.latency_ms ?? 0} ms`
								: (testResult.error ?? 'Connection failed')}
						</span>
					{/if}
				</div>
			</div>
		{/if}
	</div>
</div>
