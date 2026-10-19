<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Tabs from '../Tabs.svelte';
	import Textarea from '../Textarea.svelte';
	import MarkdownView from './MarkdownView.svelte';

	let {
		value = $bindable(''),
		maxLength = 20000
	}: {
		value?: string;
		maxLength?: number;
	} = $props();

	let tab = $state('write');
</script>

<div class="flex flex-col gap-2">
	<Tabs
		tabs={[
			{ id: 'write', label: 'Write' },
			{ id: 'preview', label: m.identity_identityproviderform_preview() }
		]}
		bind:active={tab}
	/>
	{#if tab === 'write'}
		<Textarea bind:value rows={8} />
		<div class="text-right text-[11px] text-text-muted">
			{value.length} / {maxLength}
		</div>
	{:else}
		<div class="min-h-32 rounded-md border border-border bg-bg-panel p-3">
			<MarkdownView source={value} />
		</div>
	{/if}
</div>
