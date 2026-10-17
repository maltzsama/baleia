<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import ConnectionWizard from '$lib/connections/ConnectionWizard.svelte';
</script>

<div class="flex-1 overflow-y-auto p-7">
	<ConnectionWizard ondone={() => goto(resolve('/connections'))} />
</div>
