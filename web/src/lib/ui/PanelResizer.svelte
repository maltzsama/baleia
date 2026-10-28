<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { saveWidth, widthFromDrag, widthFromKey, type PanelBounds } from './panelWidth';

	// The vertical separator beside a side panel (#468), like the workbench's
	// results splitter: drag it, use ←/→/Home/End, or double-click to reset.
	// `value` is the panel's width in px, remembered under `storageKey`.
	let {
		value = $bindable(),
		bounds,
		side,
		storageKey,
		label
	}: {
		value: number;
		bounds: PanelBounds;
		/** Which side of the separator the panel is on. */
		side: 'left' | 'right';
		storageKey: string;
		label: string;
	} = $props();

	let dragging = $state(false);
	let startX = 0;
	let startWidth = 0;

	function set(next: number) {
		value = next;
		saveWidth(storageKey, next, bounds);
	}

	function onpointerdown(event: PointerEvent) {
		if (event.button !== 0) return;
		event.preventDefault();
		(event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId);
		dragging = true;
		startX = event.clientX;
		startWidth = value;
	}

	function onpointermove(event: PointerEvent) {
		if (!dragging) return;
		value = widthFromDrag(startWidth, event.clientX - startX, side, bounds);
	}

	function onpointerup(event: PointerEvent) {
		if (!dragging) return;
		(event.currentTarget as HTMLElement).releasePointerCapture?.(event.pointerId);
		dragging = false;
		saveWidth(storageKey, value, bounds);
	}

	function onkeydown(event: KeyboardEvent) {
		const next = widthFromKey(value, event.key, side, bounds);
		if (next === null) return;
		event.preventDefault();
		set(next);
	}
</script>

<!-- A focusable separator with a value is the WAI-ARIA window splitter, an
     interactive widget; Svelte's lint only knows the static separator. -->
<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
<div
	role="separator"
	aria-orientation="vertical"
	aria-label={label}
	aria-valuenow={value}
	aria-valuemin={bounds.min}
	aria-valuemax={bounds.max}
	tabindex="0"
	title={m.ui_panelresizer_hint()}
	class="group relative z-10 -mx-[5px] w-[7px] shrink-0 cursor-col-resize touch-none self-stretch outline-none"
	{onpointerdown}
	{onpointermove}
	{onpointerup}
	onpointercancel={onpointerup}
	ondblclick={() => set(bounds.initial)}
	{onkeydown}
>
	<div
		class="absolute inset-y-0 left-[3px] w-px transition-colors group-hover:bg-primary group-focus-visible:w-[2px] group-focus-visible:bg-primary {dragging
			? 'bg-primary'
			: 'bg-transparent'}"
	></div>
</div>
