<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { workspaceStore } from '$lib/stores/workspace.svelte';
	import ActivityPanel from '$lib/audit/ActivityPanel.svelte';

	onMount(() => {
		void workspaceStore.load();
	});
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="max-w-3xl">
		<h1 class="text-xl font-semibold tracking-[-0.3px]">Settings</h1>
		<p class="mt-1 text-[13px] text-slate-600 dark:text-slate-300">
			Administration, identity and the audit trail live here.
		</p>

		<a
			class="mt-4 flex items-center justify-between rounded-lg border border-border bg-bg-panel px-4 py-3 text-[13px] transition-colors hover:border-primary"
			href={resolve('/settings/audit')}
		>
			<span>
				<span class="block font-semibold">Audit trail</span>
				<span class="block text-[11px] text-slate-600 dark:text-slate-300">
					Who changed what, when, and which requests were denied.
				</span>
			</span>
			<span class="text-primary">Open →</span>
		</a>

		{#if workspaceStore.current}
			<div class="mt-6">
				<ActivityPanel
					resourceType="workspace"
					resourceId={workspaceStore.current.id}
					title="Workspace activity"
				/>
			</div>
		{/if}
	</div>
</div>
