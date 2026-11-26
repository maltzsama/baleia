<script lang="ts">
	// A stand-in for CodeBlock in page tests (vi.mock): CodeMirror needs a
	// layout jsdom does not have. It keeps the code text and the region's name.
	let { code, label }: { code: string; label?: string } = $props();
</script>

<pre role="region" aria-label={label}>{code}</pre>
