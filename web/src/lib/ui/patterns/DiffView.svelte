<script lang="ts">
	import { onMount } from 'svelte';
	import { MergeView } from '@codemirror/merge';
	import { sql } from '@codemirror/lang-sql';

	let {
		before,
		after,
		language = 'text'
	}: {
		before: string;
		after: string;
		language?: 'sql' | 'json' | 'text';
	} = $props();

	let host: HTMLDivElement;
	let merge: MergeView | undefined;

	onMount(() => {
		const extensions = language === 'sql' ? [sql()] : [];
		merge = new MergeView({
			parent: host,
			a: { doc: before, extensions },
			b: { doc: after, extensions },
			collapseUnchanged: { margin: 3, minSize: 4 }
		});
		return () => merge?.destroy();
	});
</script>

<div bind:this={host} class="overflow-auto rounded-lg border border-border"></div>
