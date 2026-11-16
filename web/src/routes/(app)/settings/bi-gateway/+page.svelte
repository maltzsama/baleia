<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import type { components } from '$lib/api/schema';
	import { apiErrorMessage, client } from '$lib/api/client';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import { toasts } from '$lib/ui/toast.svelte';

	type GatewayStatus = components['schemas']['BIGatewayStatus'];
	type GatewayState = GatewayStatus['state'];
	type Blocker = components['schemas']['BIGatewayBlocker'];

	// The deployment guide explains the listener, public URL, TLS and secret a
	// BI gateway needs before this switch matters (docs/install.md#bi-gateway).
	const deploymentGuide =
		'https://github.com/maltzsama/baleia/blob/main/docs/install.md#bi-gateway';

	let status = $state<GatewayStatus | null>(null);
	let loading = $state(true);
	let error = $state('');
	let confirmOpen = $state(false);
	// readSeq orders the reads: a refresh that resolves after a confirmed switch
	// must not overwrite the switch result with its older snapshot.
	let readSeq = 0;

	const statLabel = 'text-[10px] uppercase tracking-wide text-text-muted';

	const stateTone: Record<GatewayState, 'success' | 'warning' | 'danger'> = {
		ready: 'success',
		disabled: 'warning',
		blocked: 'danger'
	};

	function stateLabel(state: GatewayState): string {
		switch (state) {
			case 'ready':
				return m.settings_bi_gateway_state_ready();
			case 'disabled':
				return m.settings_bi_gateway_state_disabled();
			default:
				return m.settings_bi_gateway_state_blocked();
		}
	}

	function yesNo(value: boolean): string {
		return value ? m.settings_bi_gateway_yes() : m.settings_bi_gateway_no();
	}

	function healthLabel(health: GatewayStatus['health']['status']): string {
		return health === 'ok'
			? m.settings_bi_gateway_health_ok()
			: m.settings_bi_gateway_health_unknown();
	}

	// Blocker codes are a closed contract: each one has its own remediation.
	function blockerLabel(blocker: Blocker): string {
		switch (blocker) {
			case 'listener_not_configured':
				return m.settings_bi_gateway_blocker_listener_not_configured();
			case 'secret_not_configured':
				return m.settings_bi_gateway_blocker_secret_not_configured();
			case 'public_url_not_configured':
				return m.settings_bi_gateway_blocker_public_url_not_configured();
			case 'public_url_invalid':
				return m.settings_bi_gateway_blocker_public_url_invalid();
			default:
				return m.settings_bi_gateway_blocker_gateway_disabled();
		}
	}

	async function load() {
		const seq = ++readSeq;
		loading = true;
		error = '';
		try {
			const { data, error: err } = await client.GET('/admin/bi-gateway');
			if (seq !== readSeq) return;
			if (err || !data) {
				error = apiErrorMessage(err, m.settings_bi_gateway_load_failed());
				return;
			}
			status = data;
		} catch {
			if (seq === readSeq) error = m.settings_bi_gateway_load_failed();
		} finally {
			if (seq === readSeq) loading = false;
		}
	}

	// setEnabled flips the global switch and replaces the whole status, so the
	// readiness shown after the change is the backend's, never an optimistic
	// guess. Bumping readSeq drops any refresh already in flight, so it cannot
	// restore the pre-switch snapshot once this resolves. ConfirmDialog shows a
	// thrown API body in its own footer.
	async function setEnabled(enabled: boolean) {
		readSeq++;
		loading = false;
		const { data, error: err } = await client.PUT('/admin/bi-gateway', { body: { enabled } });
		if (err || !data) throw err ?? new Error(m.settings_bi_gateway_update_failed());
		status = data;
		toasts.success(
			enabled ? m.settings_bi_gateway_enabled_toast() : m.settings_bi_gateway_disabled_toast()
		);
	}

	onMount(load);
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="w-full">
		<div class="flex flex-wrap items-end justify-between gap-3">
			<div>
				<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.settings_bi_gateway_title()}</h1>
				<p class="mt-1 text-[13px] text-text-muted">{m.settings_bi_gateway_description()}</p>
			</div>
			<div class="flex items-center gap-3">
				{#if status}
					<Badge tone={stateTone[status.state]}>{stateLabel(status.state)}</Badge>
				{/if}
				<Button {loading} onclick={load}>{m.settings_bi_gateway_refresh()}</Button>
			</div>
		</div>

		{#if loading && !status}
			<div class="mt-6 grid grid-cols-[repeat(auto-fill,minmax(min(100%,300px),1fr))] gap-3">
				<Skeleton height="7rem" />
				<Skeleton height="7rem" />
				<Skeleton height="7rem" />
			</div>
		{:else if error}
			<div class="mt-6">
				<ErrorState title={m.settings_bi_gateway_load_failed()} message={error} onretry={load} />
			</div>
		{:else if status}
			<section class="mt-6">
				<div
					class="grid grid-cols-[repeat(auto-fill,minmax(min(100%,300px),1fr))] items-start gap-3"
				>
					<article class="rounded-xl border bg-bg-panel p-4 shadow-sm">
						<h2 class="text-[13px] font-semibold">{m.settings_bi_gateway_configured()}</h2>
						<p class="mt-2 text-[13px]">{yesNo(status.configured)}</p>
						<dl class="mt-3 grid grid-cols-2 gap-x-4 gap-y-2.5">
							<div>
								<dt class={statLabel}>{m.settings_bi_gateway_listener()}</dt>
								<dd class="text-[13px]">{yesNo(status.listener_configured)}</dd>
							</div>
							<div>
								<dt class={statLabel}>{m.settings_bi_gateway_secret()}</dt>
								<dd class="text-[13px]">{yesNo(status.secret_configured)}</dd>
							</div>
						</dl>
					</article>

					<article class="rounded-xl border bg-bg-panel p-4 shadow-sm">
						<h2 class="text-[13px] font-semibold">{m.settings_bi_gateway_public_url()}</h2>
						<p class="mt-2 text-[13px]">
							{yesNo(status.public_url_ready)}
						</p>
						{#if status.public_url}
							<code class="mt-3 block truncate rounded-md bg-bg-toolbar px-2 py-1 text-[12px]"
								>{status.public_url}</code
							>
						{:else}
							<p class="mt-3 text-[12px] text-text-muted">{m.settings_bi_gateway_none()}</p>
						{/if}
					</article>

					<article class="rounded-xl border bg-bg-panel p-4 shadow-sm">
						<h2 class="text-[13px] font-semibold">{m.settings_bi_gateway_health()}</h2>
						<p class="mt-2 text-[13px]">{healthLabel(status.health.status)}</p>
						<p class="mt-1 text-[11px] text-text-muted">
							{m.settings_bi_gateway_checked()}
							<RelativeTime at={status.health.checked_at} />
						</p>
					</article>

					<article class="rounded-xl border bg-bg-panel p-4 shadow-sm">
						<h2 class="text-[13px] font-semibold">{m.settings_bi_gateway_concurrency()}</h2>
						<dl class="mt-3 grid grid-cols-2 gap-x-4 gap-y-2.5">
							<div>
								<dt class={statLabel}>{m.settings_bi_gateway_max_per_key()}</dt>
								<dd class="font-mono text-[13px]">{status.concurrency.max_per_key}</dd>
							</div>
							<div>
								<dt class={statLabel}>{m.settings_bi_gateway_max_per_workspace()}</dt>
								<dd class="font-mono text-[13px]">{status.concurrency.max_per_workspace}</dd>
							</div>
							<div>
								<dt class={statLabel}>{m.settings_bi_gateway_running()}</dt>
								<dd class="font-mono text-[13px]">
									{status.concurrency.running ?? m.settings_bi_gateway_none()}
								</dd>
							</div>
						</dl>
					</article>
				</div>
			</section>

			<section class="mt-6">
				<h2 class="mb-2 text-[11px] font-semibold uppercase tracking-wide text-text-muted">
					{m.settings_bi_gateway_blockers()}
				</h2>
				{#if status.blocked_by.length > 0}
					<ul
						class="list-disc rounded-xl border border-amber-300 bg-bg-panel py-3 pl-8 pr-4 text-[13px] leading-[1.55] text-text-main dark:border-amber-700"
					>
						{#each status.blocked_by as blocker (blocker)}
							<li>{blockerLabel(blocker)}</li>
						{/each}
					</ul>
				{:else}
					<p class="text-[13px] text-text-muted">{m.settings_bi_gateway_no_blockers()}</p>
				{/if}
			</section>

			<section class="mt-6 rounded-xl border bg-bg-panel p-4 shadow-sm">
				<h2 class="text-[13px] font-semibold">{m.settings_bi_gateway_switch()}</h2>
				<p class="mt-1 text-[13px] text-text-muted">
					{m.settings_bi_gateway_switch_description()}
				</p>
				<div class="mt-4 flex flex-wrap items-center gap-3">
					<Button
						variant={status.enabled ? 'danger' : 'primary'}
						onclick={() => (confirmOpen = true)}
					>
						{status.enabled ? m.settings_bi_gateway_disable() : m.settings_bi_gateway_enable()}
					</Button>
					<p class="text-[12px] text-text-muted">
						{m.settings_bi_gateway_operator_hint()}
						<a
							class="text-primary hover:underline"
							href={deploymentGuide}
							target="_blank"
							rel="noreferrer">{m.settings_bi_gateway_deployment_guide()}</a
						>
					</p>
				</div>
			</section>

			<ConfirmDialog
				bind:open={confirmOpen}
				title={status.enabled
					? m.settings_bi_gateway_disable_title()
					: m.settings_bi_gateway_enable_title()}
				resourceName={m.settings_bi_gateway_title()}
				consequences={status.enabled
					? [
							m.settings_bi_gateway_disable_consequence_1(),
							m.settings_bi_gateway_disable_consequence_2()
						]
					: [m.settings_bi_gateway_enable_consequence()]}
				confirmLabel={status.enabled
					? m.settings_bi_gateway_disable()
					: m.settings_bi_gateway_enable()}
				danger={status.enabled}
				onconfirm={async () => {
					if (!status) return;
					await setEnabled(!status.enabled);
				}}
			/>
		{/if}
	</div>
</div>
