<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { onMount } from 'svelte';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import Badge from '$lib/ui/Badge.svelte';
	import Button from '$lib/ui/Button.svelte';
	import EmptyState from '$lib/ui/EmptyState.svelte';
	import ErrorState from '$lib/ui/ErrorState.svelte';
	import Skeleton from '$lib/ui/Skeleton.svelte';
	import MarkdownView from '$lib/ui/patterns/MarkdownView.svelte';
	import { assistantStore } from './store.svelte';

	let input = $state('');
	let composer = $state<HTMLTextAreaElement | null>(null);
	let lastWorkspace = '';
	const workspaceId = $derived(workspaceStore.current?.id ?? '');

	onMount(async () => {
		await assistantStore.loadStatus();
		if (workspaceId) await assistantStore.load(workspaceId);
	});

	$effect(() => {
		const id = workspaceId;
		if (!id || id === lastWorkspace) return;
		lastWorkspace = id;
		if (assistantStore.enabled) void assistantStore.load(id);
	});

	function text(content: unknown): string {
		if (content && typeof content === 'object' && 'text' in content) {
			return String((content as { text?: unknown }).text ?? '');
		}
		return typeof content === 'string' ? content : '';
	}

	function field(content: unknown, key: string): unknown {
		if (content && typeof content === 'object' && key in content) {
			return (content as Record<string, unknown>)[key];
		}
		return undefined;
	}

	function pretty(value: unknown): string {
		try {
			return JSON.stringify(value, null, 2);
		} catch {
			return String(value);
		}
	}

	async function send() {
		const value = input.trim();
		if (!value || assistantStore.state === 'running') return;
		input = '';
		await assistantStore.send(value);
	}

	function onKeydown(event: KeyboardEvent) {
		if (event.key === 'Enter' && !event.shiftKey) {
			event.preventDefault();
			void send();
		}
	}
</script>

<div class="flex h-full min-h-0 flex-col">
	<div class="flex items-center gap-1.5 border-b border-border bg-bg-toolbar px-2 py-1.5">
		<select
			class="min-w-0 flex-1 rounded border border-border bg-bg-panel px-1.5 py-1 text-[11px]"
			aria-label={m.assistant_panel_conversation()}
			value={assistantStore.active?.id ?? ''}
			onchange={(event) => assistantStore.open((event.currentTarget as HTMLSelectElement).value)}
		>
			{#if assistantStore.conversations.length === 0}
				<option value="">{m.assistant_panel_no_conversations()}</option>
			{/if}
			{#each assistantStore.conversations as conversation (conversation.id)}
				<option value={conversation.id}>
					{conversation.title || m.assistant_panel_new_conversation()}
				</option>
			{/each}
		</select>
		<Button size="sm" variant="ghost" onclick={() => assistantStore.create()}>+</Button>
		<Button
			size="sm"
			variant="ghost"
			disabled={!assistantStore.active}
			onclick={() => assistantStore.active && assistantStore.remove(assistantStore.active.id)}
		>
			×
		</Button>
	</div>
	<p class="border-b border-border px-2.5 py-1 text-[10px] text-text-muted">
		{assistantStore.dataSharing === 'metadata_and_samples'
			? m.assistant_panel_model_sees_metadata_samples()
			: m.assistant_panel_model_sees_metadata_only()}
	</p>

	{#if !assistantStore.enabled}
		<div class="p-3">
			<EmptyState
				title={m.assistant_panel_assistant_is_off()}
				description={m.assistant_panel_an_administrator_can_enable_the_assistant()}
			/>
		</div>
	{:else if assistantStore.loading}
		<div class="flex flex-col gap-2 p-3">
			<Skeleton height="3rem" />
			<Skeleton height="3rem" />
		</div>
	{:else if assistantStore.error}
		<div class="p-3">
			<ErrorState
				title={m.assistant_panel_assistant_unavailable()}
				message={assistantStore.error}
				onretry={() => assistantStore.load(workspaceId)}
			/>
		</div>
	{:else}
		<div class="flex-1 overflow-y-auto p-2.5">
			{#if !assistantStore.active || assistantStore.messages.length === 0}
				<EmptyState
					title={m.assistant_panel_ask_a_data_question()}
					description={m.assistant_panel_the_assistant_explores_metadata_and_runs()}
				>
					<Button size="sm" variant="primary" onclick={() => assistantStore.create()}>
						{m.assistant_panel_new_conversation()}
					</Button>
				</EmptyState>
			{/if}
			{#each assistantStore.messages as message (message.seq)}
				{#if message.role === 'user'}
					<div class="mb-2 ml-6 rounded-lg bg-bg-toolbar px-2.5 py-1.5 text-[12px]">
						{text(message.content)}
					</div>
				{:else if message.role === 'assistant'}
					<div class="mb-2 mr-2">
						<MarkdownView source={text(message.content)} />
					</div>
				{:else if message.role === 'notice'}
					<div
						class="mb-2 rounded-md border border-warning/40 bg-warning/5 px-2 py-1.5 text-[11px] text-text-muted"
					>
						{String(
							field(message.content, 'detail') ?? m.assistant_panel_the_turn_was_interrupted()
						)}
					</div>
				{:else if message.role === 'tool_call'}
					<details class="mb-1 rounded-md border border-border bg-bg-panel px-2 py-1 text-[11px]">
						<summary class="cursor-pointer text-text-muted">
							{m.assistant_panel_tool_call()}
							<span class="font-mono">{String(field(message.content, 'tool') ?? 'tool')}</span>
						</summary>
						<pre class="mt-1 overflow-auto text-[10px]">{pretty(
								field(message.content, 'arguments')
							)}</pre>
					</details>
				{:else if message.role === 'tool_result'}
					<details class="mb-2 rounded-md border border-border bg-bg-panel px-2 py-1 text-[11px]">
						<summary class="cursor-pointer text-text-muted">
							{m.assistant_panel_result()}
							<Badge tone={field(message.content, 'outcome') === 'ok' ? 'success' : 'warning'}>
								{String(field(message.content, 'outcome') ?? 'ok')}
							</Badge>
						</summary>
						<pre class="mt-1 overflow-auto text-[10px]">{pretty(
								field(message.content, 'model_view')
							)}</pre>
					</details>
				{/if}
			{/each}
			{#if assistantStore.streamingText}
				<div class="mb-2 mr-2 opacity-80">
					<MarkdownView source={assistantStore.streamingText} />
				</div>
			{/if}
		</div>

		<div class="border-t border-border p-2">
			<textarea
				bind:this={composer}
				bind:value={input}
				onkeydown={onKeydown}
				rows="2"
				placeholder={m.assistant_panel_ask_about_your_data()}
				aria-label={m.assistant_panel_message_the_assistant()}
				autofocus
				class="w-full resize-none rounded-md border border-border bg-bg-toolbar px-2 py-1.5 text-[12px] text-text-main outline-none focus:border-primary"
			></textarea>
			<div class="mt-1.5 flex justify-end gap-1.5">
				{#if assistantStore.state === 'running'}
					<Button size="sm" onclick={() => assistantStore.cancel()}
						>{m.assistant_panel_stop()}</Button
					>
				{/if}
				<Button
					size="sm"
					variant="primary"
					disabled={!input.trim() || assistantStore.state === 'running'}
					onclick={send}
				>
					{m.assistant_panel_send()}
				</Button>
			</div>
		</div>
	{/if}
</div>
