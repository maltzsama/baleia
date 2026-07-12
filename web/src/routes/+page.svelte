<script lang="ts">
	// Server-side redirect handles setup check and auth.
	// This page is only reached when authenticated and configured.
	import { onMount } from 'svelte';
	onMount(() => window.location.href = '/sql');
</script>
