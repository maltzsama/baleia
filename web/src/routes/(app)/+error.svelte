<script lang="ts">
	import ErrorSurface from '$lib/ui/patterns/ErrorSurface.svelte';
</script>

<!-- In-shell boundary (#1062): a missing or failed authenticated route keeps the
     navigation and header, so the user can recover without leaving the app. -->
<ErrorSurface />
