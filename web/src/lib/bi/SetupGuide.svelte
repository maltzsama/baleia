<script lang="ts">
	import CheckIcon from '@lucide/svelte/icons/check';
	import { resolve } from '$app/paths';
	import type { components } from '$lib/api/schema';
	import { m } from '$lib/paraglide/messages';

	type Readiness = components['schemas']['BIGatewayReadiness'];
	type WorkspaceBI = components['schemas']['WorkspaceBIStatus'];
	type Blocker = components['schemas']['BIGatewayBlocker'];

	let {
		gateway,
		workspace = null,
		hasKeys = false,
		workspaceHref,
		globalHref = '/settings/bi-gateway',
		canManageWorkspace = true
	}: {
		gateway: Readiness;
		/** The workspace readiness, when the guide is shown inside one. */
		workspace?: WorkspaceBI | null;
		/** Whether the workspace already has a BI key. */
		hasKeys?: boolean;
		/** The workspace BI access screen (Settings > Workspaces > BI). */
		workspaceHref: string;
		globalHref?: string;
		/** Whether the caller may enable BI for the workspace. */
		canManageWorkspace?: boolean;
	} = $props();

	// The deployment guide explains the listener, public URL, TLS and secret
	// (docs/install.md#bi-gateway, linked as a static external URL below).

	type Step = {
		key: string;
		label: string;
		detail: string;
		href?: string;
		/** The operator step links the deployment guide, an external URL. */
		docs?: boolean;
		done: boolean;
	};

	function blockerLabel(blocker: Blocker): string {
		switch (blocker) {
			case 'listener_not_configured':
				return m.workspace_bi_blocker_listener_not_configured();
			case 'secret_not_configured':
				return m.workspace_bi_blocker_secret_not_configured();
			case 'public_url_not_configured':
				return m.workspace_bi_blocker_public_url_not_configured();
			case 'public_url_invalid':
				return m.workspace_bi_blocker_public_url_invalid();
			default:
				return m.workspace_bi_blocker_gateway_disabled();
		}
	}

	const steps = $derived.by(() => {
		const list: Step[] = [];
		list.push({
			key: 'operator',
			label: m.bi_setup_step_operator(),
			detail: gateway.configured
				? m.bi_setup_step_operator_detail()
				: [m.bi_setup_step_operator_blocked(), ...gateway.blocked_by.map(blockerLabel)].join(' '),
			docs: true,
			done: gateway.configured
		});
		list.push({
			key: 'global',
			label: m.bi_setup_step_global(),
			detail: m.bi_setup_step_global_detail(),
			href: globalHref,
			done: gateway.enabled
		});
		list.push({
			key: 'workspace',
			label: m.bi_setup_step_workspace(),
			detail: canManageWorkspace
				? m.bi_setup_step_workspace_detail()
				: m.bi_setup_step_workspace_ask(),
			href: canManageWorkspace ? workspaceHref : undefined,
			done: workspace?.workspace_bi_enabled ?? false
		});
		list.push({
			key: 'key',
			label: m.bi_setup_step_key(),
			detail: m.bi_setup_step_key_detail(),
			href: workspaceHref,
			done: hasKeys
		});
		list.push({
			key: 'connect',
			label: m.bi_setup_step_connect(),
			detail: m.bi_setup_step_connect_detail(),
			done: false
		});
		return list;
	});

	const current = $derived(steps.find((step) => !step.done)?.key ?? '');
</script>

<section class="rounded-xl border bg-bg-panel p-4 shadow-sm">
	<h2 class="text-[13px] font-semibold">{m.bi_setup_title()}</h2>
	<p class="mt-1 text-[11px] text-slate-600 dark:text-slate-300">{m.bi_setup_description()}</p>

	<ol class="mt-4 flex flex-col gap-3">
		{#each steps as step, index (step.key)}
			{@const isCurrent = step.key === current}
			<li class="flex items-start gap-3" aria-current={isCurrent ? 'step' : undefined}>
				<span
					class="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full border text-[11px] font-semibold
						{step.done
						? 'border-green-300 bg-green-50 text-green-700 dark:border-green-800 dark:bg-[#0f231a] dark:text-green-300'
						: isCurrent
							? 'border-primary bg-primary/10 text-primary'
							: 'border-border text-text-muted'}"
					aria-hidden="true"
				>
					{#if step.done}
						<CheckIcon class="h-3.5 w-3.5" />
					{:else}
						{index + 1}
					{/if}
				</span>
				<div class="min-w-0 flex-1">
					<div class="flex flex-wrap items-center gap-2">
						<h3 class="text-[12px] font-semibold">{step.label}</h3>
						{#if isCurrent}
							<span class="text-[10px] font-semibold uppercase tracking-wide text-primary">
								{m.bi_setup_current()}
							</span>
						{/if}
					</div>
					<p class="mt-0.5 text-[11px] leading-[1.5] text-slate-600 dark:text-slate-300">
						{step.detail}
					</p>
					{#if step.docs}
						<a
							class="mt-1 inline-block text-[11px] font-medium text-primary hover:underline"
							href="https://github.com/maltzsama/baleia/blob/main/docs/install.md#bi-gateway"
							target="_blank"
							rel="noreferrer"
						>
							{m.bi_setup_open()}
						</a>
					{:else if step.href}
						<a
							class="mt-1 inline-block text-[11px] font-medium text-primary hover:underline"
							href={resolve(step.href as '/settings/bi-gateway')}
						>
							{m.bi_setup_open()}
						</a>
					{/if}
				</div>
			</li>
		{/each}
	</ol>

	<p class="mt-4 border-t border-border pt-3 text-[11px] text-slate-600 dark:text-slate-300">
		{m.bi_setup_protocol()}
	</p>
</section>
