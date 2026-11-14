<script lang="ts">
	// Where and how a freshly issued API key or service account token is used,
	// shown next to the secret so nobody has to guess the address (#1020). The
	// key goes in the X-Api-Key header of the API under this origin; it reaches
	// no /auth/* route and cannot manage keys, so the example reads workspaces.
	import { m } from '$lib/paraglide/messages';
	import { API_ROOT } from '$lib/api/base';
	import CopyButton from './CopyButton.svelte';

	// The header the server reads an API key from.
	const HEADER = 'X-Api-Key';

	let { secret }: { secret: string } = $props();

	const address = $derived(new URL(API_ROOT, window.location.origin).href.replace(/\/$/, ''));
	const example = $derived(`curl -H '${HEADER}: ${secret}' ${address}/workspaces`);
</script>

<section class="flex flex-col gap-2 border-t border-border pt-3" aria-labelledby="api-key-usage">
	<h3 id="api-key-usage" class="text-[12px] font-semibold">{m.apikeyusage_title()}</h3>
	<p class="text-text-muted">{m.apikeyusage_header({ header: HEADER })}</p>

	<div class="flex flex-col gap-1">
		<span class="text-[11px] font-semibold text-text-muted">{m.apikeyusage_address()}</span>
		<div
			class="flex items-center gap-2 rounded-md border border-border bg-bg-toolbar px-3 py-2 font-mono text-xs"
		>
			<code class="flex-1 break-all">{address}</code>
			<CopyButton value={address} label={m.apikeyusage_address()} />
		</div>
	</div>

	<div class="flex flex-col gap-1">
		<span class="text-[11px] font-semibold text-text-muted">{m.apikeyusage_example()}</span>
		<div
			class="flex items-start gap-2 rounded-md border border-border bg-bg-toolbar px-3 py-2 font-mono text-xs"
		>
			<code class="flex-1 break-all whitespace-pre-wrap">{example}</code>
			<CopyButton value={example} label={m.apikeyusage_example()} />
		</div>
	</div>

	<p class="text-[11px] text-text-muted">{m.apikeyusage_limits()}</p>
</section>
