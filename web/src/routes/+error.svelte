<script lang="ts">
	import ErrorSurface from '$lib/ui/patterns/ErrorSurface.svelte';
</script>

<!-- Root boundary (#1062): unknown routes and failures before the app shell is
     available render a branded surface that does not depend on auth or
     workspace state. This page owns the only main landmark of the route. -->
<main class="flex h-screen w-full flex-col overflow-hidden">
	<ErrorSurface />
</main>
