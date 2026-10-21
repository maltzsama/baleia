<script lang="ts">
	import { onMount } from 'svelte';
	import { basicSetup } from 'codemirror';
	import { EditorView } from '@codemirror/view';
	import { Compartment, EditorState } from '@codemirror/state';
	import { sql } from '@codemirror/lang-sql';
	import { codemirrorDialect } from '$lib/engines/client';
	import CopyButton from './CopyButton.svelte';
	import { accessibleHighlight } from './codeHighlight';

	let {
		code,
		language = 'text',
		dialect,
		label,
		maxHeight = '360px'
	}: {
		code: string;
		language?: 'sql' | 'json' | 'text';
		/** Accessible name of the code region; defaults to the language. */
		label?: string;
		/** The engine's CodeMirror base (`codemirror_base`), for SQL highlighting. */
		dialect?: string | null;
		maxHeight?: string;
	} = $props();

	const grammar = new Compartment();
	const sqlGrammar = (base: string | null | undefined) =>
		language === 'sql' ? [sql({ dialect: codemirrorDialect(base), upperCaseKeywords: true })] : [];

	let host: HTMLDivElement;
	let view: EditorView | undefined;

	onMount(() => {
		const extensions = [
			basicSetup,
			EditorView.editable.of(false),
			EditorState.readOnly.of(true),
			grammar.of(sqlGrammar(dialect)),
			accessibleHighlight,
			// Read-only content is still a focusable, named region so keyboard
			// users can scroll it (axe: aria-input-field-name, scrollable-region-focusable).
			EditorView.contentAttributes.of({ 'aria-label': label ?? language, tabindex: '0' })
		];
		view = new EditorView({
			state: EditorState.create({ doc: code, extensions }),
			parent: host
		});
		return () => view?.destroy();
	});

	$effect(() => {
		const base = dialect;
		view?.dispatch({ effects: grammar.reconfigure(sqlGrammar(base)) });
	});

	$effect(() => {
		if (view && view.state.doc.toString() !== code) {
			view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: code } });
		}
	});
</script>

<div class="overflow-hidden rounded-lg border border-border bg-bg-panel">
	<div class="flex items-center justify-between border-b border-border bg-bg-toolbar px-3 py-1.5">
		<span class="text-[10px] font-bold uppercase tracking-wide text-text-muted">{language}</span>
		<CopyButton value={code} label={language} />
	</div>
	<div bind:this={host} class="overflow-auto" style="max-height: {maxHeight}"></div>
</div>
