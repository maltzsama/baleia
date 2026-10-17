<script lang="ts">
	import { onMount } from 'svelte';
	import { basicSetup } from 'codemirror';
	import { EditorView } from '@codemirror/view';
	import { EditorState } from '@codemirror/state';
	import { sql } from '@codemirror/lang-sql';
	import CopyButton from './CopyButton.svelte';

	let {
		code,
		language = 'text',
		maxHeight = '360px'
	}: {
		code: string;
		language?: 'sql' | 'json' | 'text';
		maxHeight?: string;
	} = $props();

	let host: HTMLDivElement;
	let view: EditorView | undefined;

	onMount(() => {
		const extensions = [
			basicSetup,
			EditorView.editable.of(false),
			EditorState.readOnly.of(true),
			...(language === 'sql' ? [sql()] : [])
		];
		view = new EditorView({
			state: EditorState.create({ doc: code, extensions }),
			parent: host
		});
		return () => view?.destroy();
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
