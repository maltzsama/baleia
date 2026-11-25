<script lang="ts">
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
	import { apiErrorMessage } from '$lib/api/errors';
	import { canGlobal } from '$lib/authz/can';
	import EngineStates from '$lib/governance/EngineStates.svelte';
	import {
		DataPolicyError,
		getStatus,
		listRevisions,
		rollbackRevision,
		type DataPolicyProblem,
		type DataPolicyRevision,
		type DataPolicyStatus
	} from '$lib/governance/api';
	import { problemLabel, revisionReasonLabel } from '$lib/governance/labels';
	import { m } from '$lib/paraglide/messages';
	import { authStore } from '$lib/stores/auth.svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import ConfirmDialog from '$lib/ui/patterns/ConfirmDialog.svelte';
	import RelativeTime from '$lib/ui/patterns/RelativeTime.svelte';
	import { toasts } from '$lib/ui/toast.svelte';

	let status = $state.raw<DataPolicyStatus | null>(null);
	let revisions = $state.raw<DataPolicyRevision[]>([]);
	let loading = $state(true);
	let error = $state('');
	let target = $state.raw<DataPolicyRevision | null>(null);
	let rollbackOpen = $state(false);
	let problems = $state.raw<DataPolicyProblem[]>([]);

	// Revisions and engine state are a global admin's (FD9).
	const known = $derived(authStore.user !== null);
	const allowed = $derived(canGlobal(authStore.user, 'manage_data_policies'));

	async function load() {
		loading = true;
		error = '';
		try {
			[status, revisions] = await Promise.all([getStatus(), listRevisions()]);
		} catch (e) {
			error = apiErrorMessage(e instanceof DataPolicyError ? e.body : e);
		} finally {
			loading = false;
		}
	}

	function askRollback(revision: DataPolicyRevision) {
		target = revision;
		problems = [];
		rollbackOpen = true;
	}

	async function confirmRollback() {
		if (!target) return;
		try {
			const revision = await rollbackRevision(target.id);
			toasts.success(m.governance_rolled_back({ number: String(revision.number) }));
		} catch (e) {
			if (e instanceof DataPolicyError) {
				problems = e.problems;
				throw e.body;
			}
			throw e;
		}
		await load();
	}

	// /auth/me may arrive after the page mounts: load once the caller is
	// known to be allowed.
	let started = false;
	$effect(() => {
		if (allowed && !started) {
			started = true;
			void load();
		}
	});
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4">
		<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.governance_revisions_title()}</h1>
		<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
			{m.governance_revisions_description()}
		</p>
	</div>

	{#if !known || (allowed && loading)}
		<div class="flex flex-col gap-2" role="status" aria-label={m.governance_loading()}>
			{#each [0, 1, 2] as item (item)}
				<Skeleton class="h-12 w-full" />
			{/each}
		</div>
	{:else if !allowed}
		<EmptyState
			title={m.governance_revisions_admin_only_title()}
			description={m.governance_revisions_admin_only_description()}
		/>
	{:else if error}
		<ErrorState title={m.governance_could_not_load_revisions()} message={error} onretry={load} />
	{:else if status && !status.enabled}
		<EmptyState
			title={m.governance_disabled_title()}
			description={m.governance_disabled_description()}
		/>
	{:else}
		{#if status}
			<div class="mb-4 flex flex-col gap-3">
				<EngineStates {status} />
				{#if status.expected_policy_digest}
					<p class="text-[12px] text-text-muted">
						{m.governance_expected_digest()}
						<code class="font-mono text-[11px]">{status.expected_policy_digest}</code>
					</p>
				{/if}
			</div>
		{/if}

		{#if problems.length > 0}
			<section
				aria-labelledby="governance-rollback-problems"
				class="mb-4 rounded-lg border border-red-300 bg-red-50 p-3 text-[12px] text-red-800 dark:border-red-800 dark:bg-red-950 dark:text-red-200"
			>
				<h2 id="governance-rollback-problems" class="mb-1 text-[13px] font-semibold">
					{m.governance_rollback_refused()}
				</h2>
				<ul class="list-disc pl-5">
					{#each problems as problem, index (`${problem.code}-${problem.policy_id}-${problem.detail}-${index}`)}
						<li>{problemLabel(problem)}</li>
					{/each}
				</ul>
			</section>
		{/if}

		{#if revisions.length === 0}
			<EmptyState
				title={m.governance_no_revisions_title()}
				description={m.governance_no_revisions_description()}
			/>
		{:else}
			<div class="overflow-x-auto rounded-lg border border-border bg-bg-panel">
				<table class="w-full text-left text-[12px]">
					<caption class="sr-only">{m.governance_revisions_title()}</caption>
					<thead class="text-[10px] uppercase tracking-wide text-text-muted">
						<tr>
							<th class="px-3 py-2">{m.governance_revision()}</th>
							<th class="px-3 py-2">{m.common_reason()}</th>
							<th class="px-3 py-2">{m.governance_created()}</th>
							<th class="px-3 py-2">{m.governance_lease()}</th>
							<th class="px-3 py-2">{m.governance_digest()}</th>
							<th class="px-3 py-2"><span class="sr-only">{m.common_action()}</span></th>
						</tr>
					</thead>
					<tbody>
						{#each revisions as revision (revision.id)}
							<tr class="border-t border-border/60">
								<td class="px-3 py-2">
									<span class="font-medium">#{revision.number}</span>
									{#if revision.published}
										<Badge tone="success">{m.governance_published_badge()}</Badge>
									{/if}
								</td>
								<td class="px-3 py-2">{revisionReasonLabel(revision.reason)}</td>
								<td class="px-3 py-2"><RelativeTime at={revision.created_at} /></td>
								<td class="px-3 py-2"><RelativeTime at={revision.not_after} /></td>
								<td class="px-3 py-2">
									<code class="font-mono text-[11px]">{revision.digest.slice(0, 12)}</code>
								</td>
								<td class="px-3 py-2 text-right">
									{#if !revision.published}
										<Button
											size="sm"
											ariaLabel={m.governance_rollback_to({ number: String(revision.number) })}
											onclick={() => askRollback(revision)}
										>
											<RotateCcwIcon class="h-3.5 w-3.5" aria-hidden="true" />
											{m.governance_rollback()}
										</Button>
									{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	{/if}
</div>

{#if target}
	<ConfirmDialog
		bind:open={rollbackOpen}
		title={m.governance_rollback_to({ number: String(target.number) })}
		resourceName={`#${target.number}`}
		confirmLabel={m.governance_rollback()}
		consequences={[
			m.governance_rollback_consequence_every_catalog(),
			m.governance_rollback_consequence_subjects(),
			m.governance_rollback_consequence_drafts()
		]}
		onconfirm={confirmRollback}
	/>
{/if}
