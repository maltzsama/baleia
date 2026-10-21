<script lang="ts">
	import { onMount } from 'svelte';
	import { basicSetup } from 'codemirror';
	import { EditorView } from '@codemirror/view';
	import { Compartment, EditorState } from '@codemirror/state';
	import { sql } from '@codemirror/lang-sql';
	import { codemirrorDialect } from '$lib/engines/client';
	import { accessibleHighlight } from '$lib/ui/patterns/codeHighlight';

	// The SQL source of a model (#121), highlighted in the dialect of the
	// model's engine. It edits text only; the API validates the model.
	let {
		value = $bindable(''),
		label,
		dialect
	}: { value?: string; label: string; dialect?: string | null } = $props();

	const uid = $props.id();
	const grammar = new Compartment();
	let host: HTMLDivElement;
	let view: EditorView | undefined;

	const language = (base: string | null | undefined) =>
		sql({ dialect: codemirrorDialect(base), upperCaseKeywords: true });

	onMount(() => {
		view = new EditorView({
			state: EditorState.create({
				doc: value,
				extensions: [
					basicSetup,
					grammar.of(language(dialect)),
					accessibleHighlight,
					EditorView.contentAttributes.of({ 'aria-labelledby': `${uid}-label` }),
					EditorView.updateListener.of((update) => {
						if (update.docChanged) value = update.state.doc.toString();
					})
				]
			}),
			parent: host
		});
		return () => view?.destroy();
	});

	$effect(() => {
		const base = dialect;
		view?.dispatch({ effects: grammar.reconfigure(language(base)) });
	});

	$effect(() => {
		if (view && view.state.doc.toString() !== value) {
			view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value } });
		}
	});
</script>

<div class="flex flex-col gap-1">
	<span id={`${uid}-label`} class="text-[11px] font-semibold text-text-muted">{label}</span>
	<div
		bind:this={host}
		class="min-h-32 overflow-auto rounded-md border border-border bg-bg-panel font-mono text-[12px]"
	></div>
</div>
