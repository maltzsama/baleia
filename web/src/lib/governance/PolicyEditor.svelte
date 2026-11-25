<script lang="ts">
	import { untrack } from 'svelte';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
	import XIcon from '@lucide/svelte/icons/x';
	import { apiErrorMessage } from '$lib/api/errors';
	import { m } from '$lib/paraglide/messages';
	import Button from '$lib/ui/Button.svelte';
	import Checkbox from '$lib/ui/Checkbox.svelte';
	import Drawer from '$lib/ui/Drawer.svelte';
	import Input from '$lib/ui/Input.svelte';
	import Select from '$lib/ui/Select.svelte';
	import Textarea from '$lib/ui/Textarea.svelte';
	import {
		createPolicy,
		updatePolicy,
		DataPolicyError,
		type CreateDataPolicyRequest,
		type DataPolicy,
		type DataPolicyKind,
		type DataPolicyMode,
		type DataPolicySubject,
		type DataPolicyTemplate
	} from './api';
	import { subjectLabel, type Directory } from './directory';
	import { maskExpression, subjectKey } from './policies';
	import SubjectPicker from './SubjectPicker.svelte';

	let {
		open = $bindable(false),
		catalogId,
		policy = null,
		table = null,
		directory,
		onsaved
	}: {
		open?: boolean;
		catalogId: string;
		/** The policy to edit; null adds one. */
		policy?: DataPolicy | null;
		/** Prefills schema and table when a policy is added to a known table. */
		table?: { schema: string; table: string } | null;
		directory: Directory;
		onsaved: (policy: DataPolicy) => void;
	} = $props();

	// The page re-creates the editor on every opening, so the form starts
	// from the policy or table it was opened for.
	const initial = untrack(() => ({ policy, table }));
	const from = initial.policy;
	let name = $state(from?.name ?? '');
	let description = $state(from?.description ?? '');
	let kind = $state<DataPolicyKind>(from?.kind ?? 'row_filter');
	let schema = $state(from?.schema ?? initial.table?.schema ?? '');
	let tableName = $state(from?.table ?? initial.table?.table ?? '');
	let column = $state(from?.column ?? '');
	let mode = $state<DataPolicyMode>(from?.mode ?? 'permissive');
	let priority = $state(String(from?.priority ?? 0));
	let subjects = $state<DataPolicySubject[]>(
		(from?.subjects ?? []).map((s) => ({ ...s, except: s.except?.slice() }))
	);
	let expression = $state(from?.expression ?? '');
	let template = $state<DataPolicyTemplate>(from?.template ?? 'custom');
	let enabled = $state((from?.state ?? 'enabled') === 'enabled');
	// Template inputs: they only fill the expression.
	let nullType = $state('varchar');
	let constant = $state('***');
	let keep = $state('4');
	let keepFrom = $state<'start' | 'end'>('end');
	let busy = $state(false);
	let error = $state('');

	const isMask = $derived(kind === 'column_mask');
	const everyone = $derived(subjects.find((s) => s.everyone));
	const valid = $derived(
		/^[a-z0-9][a-z0-9_-]{0,63}$/.test(name) &&
			schema.trim() !== '' &&
			tableName.trim() !== '' &&
			(!isMask || column.trim() !== '') &&
			subjects.length > 0 &&
			expression.trim() !== ''
	);

	function addSubject(subject: DataPolicySubject) {
		const key = subjectKey(subject);
		if (subjects.some((s) => subjectKey(s) === key)) return;
		subjects = [...subjects, subject];
	}

	function removeSubject(key: string) {
		subjects = subjects.filter((s) => subjectKey(s) !== key);
	}

	function addException(subject: DataPolicySubject) {
		subjects = subjects.map((s) => {
			if (!s.everyone) return s;
			const except = s.except ?? [];
			if (except.some((e) => subjectKey(e) === subjectKey(subject))) return s;
			return { ...s, except: [...except, subject] };
		});
	}

	function removeException(key: string) {
		subjects = subjects.map((s) =>
			s.everyone ? { ...s, except: (s.except ?? []).filter((e) => subjectKey(e) !== key) } : s
		);
	}

	function applyTemplate() {
		if (template === 'custom' || !column.trim()) return;
		expression = maskExpression(template, column.trim(), {
			type: nullType,
			value: constant,
			keep: Number(keep) || 1,
			from: keepFrom
		});
	}

	function body(): CreateDataPolicyRequest {
		const out: CreateDataPolicyRequest = {
			name,
			description,
			kind,
			schema: schema.trim(),
			table: tableName.trim(),
			subjects,
			expression: expression.trim(),
			state: enabled ? 'enabled' : 'disabled'
		};
		if (isMask) {
			out.column = column.trim();
			out.priority = Math.max(0, Math.floor(Number(priority) || 0));
			out.template = template;
		} else {
			out.mode = mode;
		}
		return out;
	}

	async function save() {
		if (!valid || busy) return;
		busy = true;
		error = '';
		try {
			const saved = policy
				? await updatePolicy(policy.id, { ...body(), version: policy.version })
				: await createPolicy(catalogId, body());
			open = false;
			onsaved(saved);
		} catch (e) {
			error = apiErrorMessage(e instanceof DataPolicyError ? e.body : e);
		} finally {
			busy = false;
		}
	}
</script>

<Drawer
	bind:open
	size="lg"
	title={policy ? m.governance_edit_policy({ name: policy.name }) : m.governance_new_policy()}
>
	<form
		class="flex flex-col gap-4"
		onsubmit={(event) => {
			event.preventDefault();
			void save();
		}}
	>
		<div class="grid gap-3 sm:grid-cols-2">
			<Input label={m.common_name()} bind:value={name} required placeholder="eu-only" />
			<Select
				label={m.common_kind()}
				bind:value={kind}
				options={[
					{ value: 'row_filter', label: m.governance_kind_row_filter() },
					{ value: 'column_mask', label: m.governance_kind_column_mask() }
				]}
			/>
			<Input label={m.governance_schema()} bind:value={schema} required />
			<Input label={m.governance_table()} bind:value={tableName} required />
			{#if isMask}
				<Input label={m.common_column()} bind:value={column} required />
				<Input label={m.governance_priority()} type="number" bind:value={priority} />
			{:else}
				<Select
					label={m.governance_mode()}
					bind:value={mode}
					options={[
						{ value: 'permissive', label: m.governance_mode_permissive() },
						{ value: 'restrictive', label: m.governance_mode_restrictive() }
					]}
				/>
			{/if}
		</div>
		<p class="text-[12px] text-text-muted">
			{isMask ? m.governance_mask_help() : m.governance_filter_help()}
		</p>
		<Input label={m.common_description()} bind:value={description} />

		<section aria-labelledby="policy-subjects" class="flex flex-col gap-2">
			<h3 id="policy-subjects" class="text-[13px] font-semibold">{m.governance_subjects()}</h3>
			{#if subjects.length === 0}
				<p class="text-[12px] text-text-muted">{m.governance_no_subjects_yet()}</p>
			{:else}
				<ul class="flex flex-wrap gap-1.5">
					{#each subjects as subject (subjectKey(subject))}
						{@const label = subjectLabel(subject, directory)}
						<li
							class="inline-flex items-center gap-1 rounded-full border border-border px-2 py-0.5 text-[12px]"
						>
							{label}
							<button
								type="button"
								class="rounded p-0.5 text-text-muted hover:text-text-main"
								aria-label={m.governance_remove_subject({ name: label })}
								onclick={() => removeSubject(subjectKey(subject))}
							>
								<XIcon class="h-3 w-3" aria-hidden="true" />
							</button>
						</li>
					{/each}
				</ul>
			{/if}
			<SubjectPicker
				{directory}
				allowEveryone={!everyone}
				legend={m.governance_add_subject()}
				addLabel={m.governance_add_subject()}
				onadd={addSubject}
			/>
			{#if everyone}
				<div class="flex flex-col gap-2 rounded-md border border-border p-3">
					<h4 class="text-[12px] font-semibold">{m.governance_exceptions()}</h4>
					{#if (everyone.except ?? []).length > 0}
						<ul class="flex flex-wrap gap-1.5">
							{#each everyone.except ?? [] as except (subjectKey(except))}
								{@const label = subjectLabel(except, directory)}
								<li
									class="inline-flex items-center gap-1 rounded-full border border-border px-2 py-0.5 text-[12px]"
								>
									{label}
									<button
										type="button"
										class="rounded p-0.5 text-text-muted hover:text-text-main"
										aria-label={m.governance_remove_exception({ name: label })}
										onclick={() => removeException(subjectKey(except))}
									>
										<XIcon class="h-3 w-3" aria-hidden="true" />
									</button>
								</li>
							{/each}
						</ul>
					{/if}
					<SubjectPicker
						{directory}
						allowEveryone={false}
						legend={m.governance_add_exception()}
						addLabel={m.governance_add_exception()}
						onadd={addException}
					/>
				</div>
			{/if}
		</section>

		{#if isMask}
			<section aria-labelledby="policy-template" class="flex flex-col gap-2">
				<h3 id="policy-template" class="text-[13px] font-semibold">{m.governance_template()}</h3>
				<div class="flex flex-wrap items-end gap-2">
					<Select
						label={m.governance_template()}
						bind:value={template}
						options={[
							{ value: 'custom', label: m.governance_template_custom() },
							{ value: 'null_value', label: m.governance_template_null() },
							{ value: 'constant', label: m.governance_template_constant() },
							{ value: 'partial', label: m.governance_template_partial() },
							{ value: 'sha256', label: m.governance_template_sha256() }
						]}
					/>
					{#if template === 'null_value'}
						<Input label={m.governance_column_type()} bind:value={nullType} />
					{:else if template === 'constant'}
						<Input label={m.governance_constant_value()} bind:value={constant} />
					{:else if template === 'partial'}
						<Input label={m.governance_keep_characters()} type="number" bind:value={keep} />
						<Select
							label={m.governance_keep_from()}
							bind:value={keepFrom}
							options={[
								{ value: 'end', label: m.governance_keep_last() },
								{ value: 'start', label: m.governance_keep_first() }
							]}
						/>
					{/if}
					{#if template !== 'custom'}
						<Button size="sm" onclick={applyTemplate} disabled={!column.trim()}>
							{m.governance_fill_expression()}
						</Button>
					{/if}
				</div>
				<p class="text-[12px] text-text-muted">{m.governance_template_help()}</p>
				{#if template === 'sha256'}
					<p
						role="note"
						class="flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 p-2 text-[12px] text-amber-900 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-100"
					>
						<TriangleAlertIcon class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
						{m.governance_sha256_warning()}
					</p>
				{/if}
			</section>
		{/if}

		<Textarea
			label={m.governance_expression()}
			bind:value={expression}
			rows={4}
			required
			placeholder={isMask ? "'***'" : "region = 'EU'"}
		/>
		<p class="text-[12px] text-text-muted">{m.governance_expression_help()}</p>
		<Checkbox label={m.governance_policy_enabled()} bind:checked={enabled} />

		{#if error}
			<p role="alert" class="text-[12px] text-danger">{error}</p>
		{/if}
		<p class="text-[12px] text-text-muted">{m.governance_saved_as_draft()}</p>
		<div class="flex justify-end gap-2">
			<Button onclick={() => (open = false)}>{m.common_cancel()}</Button>
			<Button type="submit" variant="primary" loading={busy} disabled={!valid}>
				{m.common_save()}
			</Button>
		</div>
	</form>
</Drawer>
