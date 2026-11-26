<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { SvelteURLSearchParams } from 'svelte/reactivity';
	import { client } from '$lib/api/client';
	import { apiErrorMessage } from '$lib/api/errors';
	import AttributeFreshness from '$lib/governance/AttributeFreshness.svelte';
	import GuaranteeNotice from '$lib/governance/GuaranteeNotice.svelte';
	import SubjectPicker from '$lib/governance/SubjectPicker.svelte';
	import {
		DataPolicyError,
		listCatalogPolicies,
		listManageableCatalogs,
		policyDialect,
		type DataPolicy,
		type DataPolicySubject
	} from '$lib/governance/api';
	import {
		emptyDirectory,
		loadDirectory,
		subjectLabel,
		type Directory
	} from '$lib/governance/directory';
	import { combinedFilter, effectsFor, subjectFromKey, subjectKey } from '$lib/governance/policies';
	import { m } from '$lib/paraglide/messages';
	import Badge from '$lib/ui/Badge.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import CodeBlock from '$lib/ui/patterns/CodeBlock.svelte';

	type CatalogPolicies = {
		id: string;
		name: string;
		policies: DataPolicy[];
		/** The enforcing engine's CodeMirror base, for the policy SQL. */
		dialect: string | null;
	};

	let directory = $state.raw<Directory>(emptyDirectory);
	let catalogs = $state.raw<CatalogPolicies[]>([]);
	let enabled = $state(true);
	let subject = $state.raw<DataPolicySubject | null>(
		subjectFromKey(page.url.searchParams.get('subject') ?? '')
	);
	let teamKeys = $state.raw<string[]>([]);
	let teamsLoading = $state(false);
	let teamsError = $state('');
	let loading = $state(true);
	let error = $state('');
	// Each team lookup takes a number: one for an earlier subject, finishing
	// late, does not replace the current subject's teams.
	let teamsRequest = 0;

	// Who the subject is known as: itself, and for a user its teams. Identity-
	// provider groups come from the user's sign-ins, which this screen cannot
	// read, so they are chosen as subjects of their own.
	const refs = $derived(
		new Set(subject && !subject.everyone ? [subjectKey(subject), ...teamKeys] : teamKeys)
	);
	const effects = $derived(
		subject
			? catalogs
					.map((c) => ({ catalog: c, tables: effectsFor(c.policies, refs) }))
					.filter((c) => c.tables.length > 0)
			: []
	);

	async function load() {
		loading = true;
		error = '';
		try {
			const [managed, dir] = await Promise.all([listManageableCatalogs(), loadDirectory()]);
			directory = dir;
			const lists = await Promise.all(
				managed.map((c) => Promise.all([listCatalogPolicies(c.id ?? ''), policyDialect(c)]))
			);
			enabled = lists.every(([l]) => l.enabled);
			catalogs = managed.map((c, i) => ({
				id: c.id ?? '',
				name: c.name ?? c.id ?? '',
				policies: lists[i][0].policies,
				dialect: lists[i][1]
			}));
		} catch (e) {
			error = apiErrorMessage(e instanceof DataPolicyError ? e.body : e);
		} finally {
			loading = false;
		}
		if (!error) await loadTeams();
	}

	// A user's teams, from the teams the caller can read. A failed lookup is an
	// error, not "member of none": the effects would leave out what the teams
	// grant.
	async function loadTeams() {
		const request = ++teamsRequest;
		teamKeys = [];
		teamsError = '';
		teamsLoading = false;
		const user = subject?.user;
		if (!user) return;
		if (directory.teamsFailed) {
			teamsError = m.governance_teams_unavailable();
			return;
		}
		teamsLoading = true;
		try {
			const found = await Promise.all(
				directory.teams.map(async (team) => {
					const { data, error: failed } = await client.GET('/teams/{id}/members', {
						params: { path: { id: team.id } }
					});
					if (failed || !data) throw failed ?? new Error(team.id);
					return data.some((member) => member.user.id === user) ? `team:${team.id}` : null;
				})
			);
			if (request !== teamsRequest) return;
			teamKeys = found.filter((key): key is string => key !== null);
		} catch (e) {
			if (request !== teamsRequest) return;
			teamsError = apiErrorMessage(e);
		} finally {
			if (request === teamsRequest) teamsLoading = false;
		}
	}

	function choose(next: DataPolicySubject) {
		subject = next;
		const params = new SvelteURLSearchParams(page.url.searchParams);
		params.set('subject', subjectKey(next));
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		void goto(`${page.url.pathname}?${params}`, {
			replaceState: true,
			keepFocus: true,
			noScroll: true
		});
		void loadTeams();
	}

	onMount(load);
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4">
		<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.governance_by_subject_title()}</h1>
		<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
			{m.governance_by_subject_description()}
		</p>
	</div>

	<div class="mb-4"><GuaranteeNotice /></div>

	{#if loading}
		<div class="flex flex-col gap-2" role="status" aria-label={m.governance_loading()}>
			{#each [0, 1, 2] as item (item)}
				<Skeleton class="h-12 w-full" />
			{/each}
		</div>
	{:else if error}
		<ErrorState title={m.governance_could_not_load_policies()} message={error} onretry={load} />
	{:else if !enabled}
		<EmptyState
			title={m.governance_disabled_title()}
			description={m.governance_disabled_description()}
		/>
	{:else if catalogs.length === 0}
		<EmptyState
			title={m.governance_no_catalogs_title()}
			description={m.governance_no_catalogs_description()}
		/>
	{:else}
		<div class="mb-4 rounded-lg border border-border bg-bg-panel p-3">
			<SubjectPicker
				{directory}
				legend={m.governance_choose_subject()}
				addLabel={m.governance_show_subject()}
				onadd={choose}
			/>
			{#if subject?.group !== undefined}
				<p class="mt-2 text-[12px] text-text-muted">{m.governance_group_note()}</p>
			{:else if subject?.user}
				<p class="mt-2 text-[12px] text-text-muted">{m.governance_user_note()}</p>
			{/if}
		</div>

		{#if !subject}
			<EmptyState
				title={m.governance_choose_subject()}
				description={m.governance_choose_subject_description()}
			/>
		{:else}
			<h2 class="mb-2 text-[15px] font-semibold">
				{m.governance_effects_for({ name: subjectLabel(subject, directory) })}
			</h2>
			<p class="mb-3 text-[12px] text-text-muted">{m.governance_effects_draft_note()}</p>
			{#if teamsLoading}
				<div class="flex flex-col gap-2" role="status" aria-label={m.governance_loading()}>
					{#each [0, 1] as item (item)}
						<Skeleton class="h-12 w-full" />
					{/each}
				</div>
			{:else if teamsError}
				<ErrorState
					title={m.governance_could_not_load_teams()}
					message={teamsError}
					onretry={loadTeams}
				/>
			{:else if effects.length === 0}
				<EmptyState
					title={m.governance_nothing_applies_title()}
					description={m.governance_nothing_applies_description()}
				/>
			{:else}
				<div class="flex flex-col gap-4">
					{#each effects as entry (entry.catalog.id)}
						<section
							aria-label={entry.catalog.name}
							class="rounded-lg border border-border bg-bg-panel"
						>
							<h3 class="border-b border-border px-3 py-2 text-[13px] font-semibold">
								{entry.catalog.name}
							</h3>
							<table class="w-full text-left text-[12px]">
								<thead class="text-[10px] uppercase tracking-wide text-text-muted">
									<tr>
										<th class="px-3 py-2">{m.governance_table()}</th>
										<th class="px-3 py-2">{m.governance_filtered()}</th>
										<th class="px-3 py-2">{m.governance_masked()}</th>
									</tr>
								</thead>
								<tbody>
									{#each entry.tables as effect (effect.key)}
										<tr class="border-t border-border/60 align-top">
											<td class="px-3 py-2 font-mono">{effect.table}</td>
											<td class="px-3 py-2">
												{#if !effect.filtered}
													<span class="text-text-muted">{m.governance_all_rows()}</span>
												{:else if effect.noRows}
													<Badge tone="danger">{m.governance_no_rows()}</Badge>
													<div class="mt-1 text-text-muted">{m.governance_default_deny()}</div>
												{:else}
													<CodeBlock
														code={combinedFilter(effect)}
														language="sql"
														dialect={entry.catalog.dialect}
														label={m.governance_filter_of({ table: effect.table })}
														maxHeight="120px"
													/>
													<div class="mt-1 text-text-muted">
														{[...effect.permissive, ...effect.restrictive]
															.map((p) => p.name)
															.join(', ')}
													</div>
												{/if}
											</td>
											<td class="px-3 py-2">
												{#if effect.masks.length === 0}
													<span class="text-text-muted">{m.governance_no_masks()}</span>
												{:else}
													<ul class="flex flex-col gap-1">
														{#each effect.masks as mask (mask.column)}
															<li>
																<span class="font-mono">{mask.column}</span>
																→
																<CodeBlock
																	code={mask.applied.expression}
																	language="sql"
																	dialect={entry.catalog.dialect}
																	label={m.governance_mask_of({
																		column: mask.column,
																		table: effect.table
																	})}
																	maxHeight="80px"
																/>
																<span class="text-text-muted">({mask.applied.name})</span>
																{#if mask.shadowed.length > 0}
																	<div class="text-text-muted">
																		{m.governance_shadowed({
																			names: mask.shadowed.map((p) => p.name).join(', ')
																		})}
																	</div>
																{/if}
															</li>
														{/each}
													</ul>
												{/if}
											</td>
										</tr>
									{/each}
								</tbody>
							</table>
						</section>
					{/each}
				</div>
			{/if}
		{/if}

		<div class="mt-4"><AttributeFreshness /></div>
	{/if}
</div>
