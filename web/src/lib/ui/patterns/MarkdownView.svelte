<script lang="ts">
	import { Marked } from 'marked';
	import DOMPurify from 'dompurify';

	let { source }: { source: string } = $props();

	const marked = new Marked({
		gfm: true,
		breaks: true,
		renderer: {
			// Remote images are never rendered (FD1/security): show the alt text
			// as a link instead.
			image({ href, text }) {
				const label = text || href;
				return `<a href="${href}" target="_blank" rel="noopener noreferrer">${label}</a>`;
			},
			link({ href, title, text }) {
				const titleAttr = title ? ` title="${title}"` : '';
				return `<a href="${href}"${titleAttr} target="_blank" rel="noopener noreferrer">${text}</a>`;
			}
		}
	});

	const html = $derived.by(() => {
		if (typeof window === 'undefined') return '';
		const rendered = marked.parse(source, { async: false }) as string;
		return DOMPurify.sanitize(rendered, { FORBID_TAGS: ['img'] });
	});
</script>

<div class="markdown-body text-[13px] leading-[1.6] text-text-main">
	<!-- eslint-disable-next-line svelte/no-at-html-tags -- sanitized with DOMPurify -->
	{@html html}
</div>

<style>
	.markdown-body :global(h1),
	.markdown-body :global(h2),
	.markdown-body :global(h3) {
		font-weight: 600;
		margin: 0.8em 0 0.4em;
	}
	.markdown-body :global(p) {
		margin: 0.4em 0;
	}
	.markdown-body :global(ul),
	.markdown-body :global(ol) {
		margin: 0.4em 0;
		padding-left: 1.4em;
	}
	.markdown-body :global(code) {
		font-family: var(--font-mono);
		font-size: 0.92em;
		background: var(--color-bg-toolbar);
		border-radius: 3px;
		padding: 0.1em 0.3em;
	}
	.markdown-body :global(pre) {
		background: var(--color-bg-toolbar);
		border: 1px solid var(--color-border);
		border-radius: 6px;
		padding: 0.6em;
		overflow: auto;
	}
	.markdown-body :global(a) {
		color: var(--color-primary);
		text-decoration: underline;
	}
</style>
