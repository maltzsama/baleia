<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Tabs from '../Tabs.svelte';
	import Textarea from '../Textarea.svelte';
	import MarkdownView from './MarkdownView.svelte';

	let {
		value = $bindable(''),
		label,
		maxLength = 20000
	}: {
		value?: string;
		/** What the text is (e.g. Description); it names the editor (#475). */
		label: string;
		maxLength?: number;
	} = $props();

	const uid = $props.id();
	const labelId = `${uid}-label`;

	let tab = $state('write');
</script>

<div class="flex flex-col gap-2">
	<span id={labelId} class="text-[11px] font-semibold text-text-muted">{label}</span>
	<Tabs
		tabs={[
			{ id: 'write', label: m.markdowneditor_write() },
			{ id: 'preview', label: m.identity_identityproviderform_preview() }
		]}
		bind:active={tab}
	/>
	{#if tab === 'write'}
		<Textarea bind:value rows={8} ariaLabelledby={labelId} />
		<div class="text-right text-[11px] text-text-muted">
			{value.length} / {maxLength}
		</div>
	{:else}
		<div class="min-h-32 rounded-md border border-border bg-bg-panel p-3">
			<MarkdownView source={value} />
		</div>
	{/if}
</div>
