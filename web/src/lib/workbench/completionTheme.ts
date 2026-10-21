// The autocomplete popup in the app's tokens (#383): light and dark follow the
// `.dark` class through the CSS variables in app.css. The `cm-baleia-completion`
// class outranks CodeMirror's base theme and oneDark.
import type { autocompletion } from '@codemirror/autocomplete';
import { EditorView } from '@codemirror/view';

export const completionConfig: Parameters<typeof autocompletion>[0] = {
	icons: true,
	maxRenderedOptions: 100,
	activateOnTypingDelay: 60,
	tooltipClass: () => 'cm-baleia-completion'
};

const popup = '.cm-tooltip.cm-tooltip-autocomplete.cm-baleia-completion';

// Kind badge: letter and color per completion type.
const icons: Record<string, { letter: string; color: string }> = {
	property: { letter: 'C', color: 'var(--color-type-string)' },
	class: { letter: 'T', color: 'var(--color-primary)' },
	interface: { letter: 'V', color: 'var(--color-primary)' },
	namespace: { letter: 'S', color: 'var(--color-type-temporal)' },
	function: { letter: 'f', color: 'var(--color-type-numeric)' },
	keyword: { letter: 'K', color: 'var(--color-text-muted)' },
	type: { letter: 'τ', color: 'var(--color-text-muted)' }
};

export const completionTheme = EditorView.theme({
	[popup]: {
		background: 'var(--color-bg-panel)',
		color: 'var(--color-text-main)',
		border: '1px solid var(--color-border)',
		borderRadius: '8px',
		boxShadow: '0 10px 30px -8px rgb(15 23 42 / 0.25)',
		padding: '4px',
		fontFamily: 'var(--font-ui)'
	},
	[`${popup} > ul`]: {
		fontFamily: 'var(--font-mono)',
		fontSize: '12px',
		minWidth: '320px',
		maxWidth: 'min(560px, 90vw)',
		maxHeight: '18.5em'
	},
	[`${popup} > ul > li`]: {
		display: 'flex',
		alignItems: 'center',
		gap: '8px',
		padding: '3px 8px',
		lineHeight: '1.5',
		borderRadius: '5px',
		color: 'var(--color-text-main)'
	},
	[`${popup} > ul > li[aria-selected]`]: {
		background: 'color-mix(in srgb, var(--color-primary) 14%, var(--color-bg-panel))',
		color: 'var(--color-text-main)'
	},
	[`${popup} .cm-completionLabel`]: {
		flex: '1 1 auto',
		minWidth: '0',
		overflow: 'hidden',
		textOverflow: 'ellipsis'
	},
	[`${popup} .cm-completionMatchedText`]: {
		textDecoration: 'none',
		fontWeight: '700'
	},
	[`${popup} .cm-completionDetail`]: {
		marginLeft: 'auto',
		paddingLeft: '12px',
		fontStyle: 'normal',
		fontSize: '11px',
		color: 'var(--color-text-muted)',
		flexShrink: '0',
		maxWidth: '45%',
		overflow: 'hidden',
		textOverflow: 'ellipsis'
	},
	// Muted text falls under AA on the selected tint.
	[`${popup} > ul > li[aria-selected] .cm-completionDetail`]: {
		color: 'var(--color-text-main)'
	},
	[`${popup} .cm-completionIcon`]: {
		width: '16px',
		height: '16px',
		padding: '0',
		flexShrink: '0',
		borderRadius: '4px',
		fontFamily: 'var(--font-ui)',
		fontSize: '10px',
		fontWeight: '700',
		lineHeight: '16px',
		textAlign: 'center',
		opacity: '1',
		border: '1px solid currentColor',
		color: 'var(--color-text-muted)'
	},
	...Object.fromEntries(
		Object.entries(icons).map(([type, { letter, color }]) => [
			`${popup} .cm-completionIcon-${type}`,
			{ color, '&:after': { content: `'${letter}'` } }
		])
	),
	[`${popup} .cm-completionInfo`]: {
		background: 'var(--color-bg-panel)',
		color: 'var(--color-text-main)',
		border: '1px solid var(--color-border)',
		borderRadius: '8px',
		boxShadow: '0 10px 30px -8px rgb(15 23 42 / 0.25)',
		padding: '8px 10px',
		margin: '0 6px',
		maxWidth: '320px',
		fontSize: '12px',
		whiteSpace: 'normal'
	},
	[`${popup} .cm-baleia-info-head`]: {
		display: 'flex',
		alignItems: 'baseline',
		gap: '8px',
		fontFamily: 'var(--font-mono)'
	},
	[`${popup} .cm-baleia-info-type`]: {
		color: 'var(--color-text-muted)',
		fontSize: '11px'
	},
	[`${popup} .cm-baleia-info p`]: {
		margin: '4px 0 0',
		color: 'var(--color-text-muted)'
	}
});
