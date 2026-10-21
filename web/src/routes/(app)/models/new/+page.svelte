<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import ModelEditor from '$lib/semantic/ModelEditor.svelte';
</script>

<div class="flex-1 overflow-y-auto p-7">
	<div class="mb-4">
		<h1 class="text-xl font-semibold tracking-[-0.3px]">{m.models_new()}</h1>
		<p class="mt-1 text-[13px] text-text-muted">{m.models_new_hint()}</p>
	</div>
	<ModelEditor />
</div>
