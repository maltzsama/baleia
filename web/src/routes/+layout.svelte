<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';

	let { children } = $props();

	onMount(() => {
		// Apply the persisted theme on every route so it survives navigation.
		const dark = localStorage.getItem('theme') === 'dark';
		document.body.classList.toggle('dark', dark);
	});
</script>

{@render children()}
