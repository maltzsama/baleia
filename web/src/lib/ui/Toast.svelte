<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	type Tone = 'info' | 'success' | 'error';

	let {
		message,
		tone = 'info',
		ondismiss
	}: {
		message: string;
		tone?: Tone;
		ondismiss?: () => void;
	} = $props();

	const tones: Record<Tone, string> = {
		info: 'border-border bg-bg-panel text-text-main',
		success:
			'border-green-200 bg-green-50 text-green-800 dark:border-[#14532d] dark:bg-[#0f231a] dark:text-green-300',
		error:
			'border-red-200 bg-red-50 text-red-700 dark:border-[#4d1111] dark:bg-[#1f0f0f] dark:text-red-300'
	};
</script>

<div
	role={tone === 'error' ? 'alert' : 'status'}
	class="flex items-start gap-3 rounded-lg border px-4 py-3 text-[13px] shadow-sm {tones[tone]}"
>
	<span class="flex-1">{message}</span>
	<button
		type="button"
		class="rounded px-1 text-current opacity-60 hover:opacity-100"
		onclick={ondismiss}
		aria-label={m.ui_toast_dismiss_notification()}
	>
		×
	</button>
</div>
