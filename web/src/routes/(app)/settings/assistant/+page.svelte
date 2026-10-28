<script lang="ts">
	import { m } from '$lib/paraglide/messages';
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
		{ value: 'anthropic', label: m.settings_assistant_anthropic() },
		{ value: 'openai_compatible', label: m.settings_assistant_openai_compatible() }
	];
	const sharingOptions = [
		{ value: 'metadata_only', label: m.settings_assistant_metadata_only_recommended() },
		{ value: 'metadata_and_samples', label: m.settings_assistant_metadata_result_samples() }
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
				error = apiErrorMessage(err, m.settings_assistant_could_not_load_assistant_settings());
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
			error = m.settings_assistant_could_not_load_assistant_settings();
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
				saveError = apiErrorMessage(err, m.settings_assistant_could_not_save_the_settings());
				return;
			}
			settings = data;
			apiKey = '';
			toasts.success(m.settings_assistant_assistant_settings_saved());
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
		<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.settings_assistant_assistant()}</h1>
		<p class="mt-1 text-[13px] text-text-muted">
			{m.settings_assistant_the_assistant_is_off_by_default_it_answers()}
		</p>

		{#if loading}
			<div class="mt-6 flex flex-col gap-3">
				<Skeleton height="6rem" />
				<Skeleton height="6rem" />
			</div>
		{:else if error}
			<div class="mt-6">
				<ErrorState
					title={m.settings_assistant_could_not_load_settings()}
					message={error}
					onretry={load}
				/>
			</div>
		{:else}
			<div class="mt-6 flex flex-col gap-4">
				<section class="rounded-lg border border-border bg-bg-panel p-4">
					<div class="flex flex-wrap items-center justify-between gap-3">
						<div>
							<h2 class="text-[13px] font-semibold">{m.settings_assistant_provider()}</h2>
							<p class="text-[12px] text-text-muted">
								{m.settings_assistant_the_api_process_needs_egress_to()}
							</p>
						</div>
						{#if settings?.api_key_set}<Badge tone="success">{m.settings_assistant_key_set()}</Badge
							>{/if}
					</div>
					<div class="mt-3 grid gap-3 sm:grid-cols-2">
						<Checkbox bind:checked={enabled} label={m.settings_assistant_enable_the_assistant()} />
						<Select
							label={m.settings_assistant_provider()}
							bind:value={provider}
							placeholder={m.settings_assistant_choose_a_provider()}
							options={providerOptions}
						/>
						<Input
							label={m.settings_assistant_base_url()}
							bind:value={baseUrl}
							placeholder={provider === 'anthropic' ? 'https://api.anthropic.com' : 'https://…/v1'}
						/>
						<Input
							label={m.settings_assistant_model()}
							bind:value={model}
							placeholder={m.settings_assistant_claude_or_gpt()}
						/>
						<Input
							label={m.settings_assistant_api_key()}
							type="password"
							bind:value={apiKey}
							placeholder={settings?.api_key_set
								? m.settings_assistant_set_leave_empty_to_keep()
								: m.settings_assistant_not_set()}
						/>
						<Select
							label={m.settings_assistant_data_sharing()}
							bind:value={dataSharing}
							options={sharingOptions}
						/>
					</div>
				</section>

				<section class="rounded-lg border border-border bg-bg-panel p-4">
					<h2 class="text-[13px] font-semibold">{m.settings_assistant_limits()}</h2>
					<p class="text-[12px] text-text-muted">
						{m.settings_assistant_bounds_every_turn_the_daily_budget()}
					</p>
					<div class="mt-3 grid gap-3 sm:grid-cols-2">
						<Input
							label={m.settings_assistant_max_turn_seconds()}
							type="number"
							bind:value={maxTurnSeconds}
						/>
						<Input
							label={m.settings_assistant_max_tool_calls_per_turn()}
							type="number"
							bind:value={maxToolCalls}
						/>
						<Input
							label={m.settings_assistant_max_output_tokens_per_turn()}
							type="number"
							bind:value={maxOutputTokens}
						/>
						<Input
							label={m.settings_assistant_daily_token_budget_per_user()}
							type="number"
							bind:value={dailyBudget}
						/>
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
					<Button variant="primary" loading={saving} onclick={save}
						>{m.settings_assistant_save_settings()}</Button
					>
					<Button loading={testing} onclick={test}>{m.settings_assistant_test_connection()}</Button>
					{#if testResult}
						<span class="text-[12px] {testResult.ok ? 'text-success' : 'text-danger'}">
							{testResult.ok
								? m.settings_assistant_connected_in_ms({ value: testResult.latency_ms ?? 0 })
								: (testResult.error ?? m.settings_assistant_connection_failed())}
						</span>
					{/if}
				</div>
			</div>
		{/if}
	</div>
</div>
