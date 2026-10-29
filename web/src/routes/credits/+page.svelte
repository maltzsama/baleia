<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { resolve } from '$app/paths';
	import Logo from '$lib/components/Logo.svelte';

	// Read on every render so a language switch updates the list.
	const permittedUses = $derived([
		m.credits_license_commercial(),
		m.credits_license_media(),
		m.credits_license_unlimited(),
		m.credits_license_worldwide(),
		m.credits_license_derivatives()
	]);
</script>

<svelte:head>
	<title>{m.common_credits()} · {m.common_baleia()}</title>
</svelte:head>

<div class="h-screen overflow-y-auto bg-bg-app">
	<main class="mx-auto w-full max-w-[720px] px-6 py-10">
		<header class="mb-8 flex items-center gap-3">
			<a href={resolve('/')} class="flex items-center gap-3 text-text-main no-underline">
				<Logo class="h-12 w-12" />
				<span class="text-lg font-semibold">{m.common_baleia()}</span>
			</a>
		</header>

		<h1 class="text-2xl font-semibold tracking-[-0.4px]">{m.common_credits()}</h1>
		<p class="mt-2 text-[13px] leading-relaxed text-text-muted">{m.credits_intro()}</p>

		<section class="mt-6 rounded-lg border border-border bg-bg-panel p-5">
			<h2 class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">
				{m.credits_brand_heading()}
			</h2>
			<p class="mt-2 text-[13px] leading-relaxed">{m.credits_brand_body()}</p>
			<ul class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[13px]">
				<li>
					<a
						class="text-primary hover:underline"
						href="https://www.flaticon.com"
						target="_blank"
						rel="noopener noreferrer">{m.credits_flaticon()}</a
					>
				</li>
				<li>
					<a
						class="text-primary hover:underline"
						href="https://www.flaticon.com/legal"
						target="_blank"
						rel="noopener noreferrer">{m.credits_flaticon_free_license()}</a
					>
				</li>
				<li>
					<a
						class="text-primary hover:underline"
						href="https://realfavicongenerator.net"
						target="_blank"
						rel="noopener noreferrer">{m.credits_realfavicongenerator()}</a
					>
				</li>
			</ul>
			<blockquote
				class="mt-4 border-l-2 border-border pl-4 text-[13px] leading-relaxed text-text-muted"
			>
				<p>{m.credits_license_summary()}</p>
				<ul class="mt-1 list-disc pl-5">
					{#each permittedUses as use (use)}
						<li>{use}</li>
					{/each}
				</ul>
				<p class="mt-2">
					{m.credits_license_disclaimer()}
					<a
						class="text-primary hover:underline"
						href="https://www.flaticon.com/legal"
						target="_blank"
						rel="noopener noreferrer">{m.credits_terms_of_use()}</a
					>.
				</p>
			</blockquote>
			<p class="mt-4 text-[13px] text-text-muted">{m.credits_favicon_body()}</p>
		</section>

		<section class="mt-4 rounded-lg border border-border bg-bg-panel p-5">
			<h2 class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">
				{m.credits_fonts_heading()}
			</h2>
			<p class="mt-2 text-[13px] leading-relaxed">{m.credits_fonts_body()}</p>
			<ul class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[13px]">
				<li>
					<a
						class="text-primary hover:underline"
						href="https://fontsource.org"
						target="_blank"
						rel="noopener noreferrer">{m.credits_fontsource()}</a
					>
				</li>
				<li>
					<a
						class="text-primary hover:underline"
						href="https://openfontlicense.org"
						target="_blank"
						rel="noopener noreferrer">{m.credits_sil_ofl()}</a
					>
				</li>
			</ul>
		</section>

		<section class="mt-4 rounded-lg border border-border bg-bg-panel p-5">
			<h2 class="text-[11px] font-semibold uppercase tracking-wide text-text-muted">
				{m.credits_software_heading()}
			</h2>
			<p class="mt-2 text-[13px] leading-relaxed">
				{m.credits_software_body()}
				<a
					class="text-primary hover:underline"
					href="https://github.com/maltzsama/baleia/blob/main/CREDITS.md"
					target="_blank"
					rel="noopener noreferrer">{m.credits_credits_md()}</a
				>.
			</p>
			<ul class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[13px]">
				<li>
					<a
						class="text-primary hover:underline"
						href="https://github.com/maltzsama/baleia/blob/main/LICENSE"
						target="_blank"
						rel="noopener noreferrer">{m.credits_apache()}</a
					>
				</li>
			</ul>
		</section>

		<footer class="mt-8">
			<a href={resolve('/')} class="text-[13px] text-primary hover:underline">
				{m.credits_back()}
			</a>
		</footer>
	</main>
</div>
