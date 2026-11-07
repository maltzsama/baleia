// Function signature help (#790): with the cursor inside a function call, show
// the engine's signature for that function. The catalog is the same runtime one
// the completion reads; it is loaded on demand and cached.
import { StateEffect, StateField, type Extension } from '@codemirror/state';
import { EditorView, showTooltip } from '@codemirror/view';
import { loadBindingFunctions } from '$lib/engines/client';
import type { CompletionScope } from './sqlCompletion';

/** The Innermost function call around the cursor, and its 0-based argument. */
export type CallContext = { name: string; argIndex: number };

const isWord = (ch: string) => /[\w$]/.test(ch);

/**
 * callAt returns the innermost function call containing `pos` and the argument
 * the cursor is in, or null. It counts only top-level commas; a comma inside a
 * string literal is not distinguished, which is acceptable for a hint.
 */
export function callAt(doc: string, pos: number): CallContext | null {
	let depth = 0;
	for (let i = pos - 1; i >= 0; i--) {
		const ch = doc[i];
		if (ch === ')') {
			depth++;
		} else if (ch === '(') {
			if (depth > 0) {
				depth--;
				continue;
			}
			let j = i - 1;
			while (j >= 0 && /\s/.test(doc[j])) j--;
			const end = j + 1;
			while (j >= 0 && isWord(doc[j])) j--;
			const name = doc.slice(j + 1, end);
			if (!name) return null;
			let argIndex = 0;
			let inner = 0;
			for (let k = i + 1; k < pos; k++) {
				const c = doc[k];
				if (c === '(') inner++;
				else if (c === ')') inner--;
				else if (c === ',' && inner === 0) argIndex++;
			}
			return { name, argIndex };
		}
	}
	return null;
}

type Help = { name: string; signature: string; argIndex: number; pos: number } | null;

const setHelp = StateEffect.define<Help>();

const helpField = StateField.define<Help>({
	create: () => null,
	update(value, tr) {
		for (const effect of tr.effects) if (effect.is(setHelp)) return effect.value;
		if (!value || (!tr.docChanged && !tr.selection)) return value;
		// Keep the help while the cursor stays in the same call, updating the
		// argument and position; clear it when the call is left.
		const pos = tr.state.selection.main.head;
		const ctx = callAt(tr.state.doc.toString(), pos);
		if (!ctx || ctx.name.toLowerCase() !== value.name.toLowerCase()) return null;
		return { ...value, argIndex: ctx.argIndex, pos };
	}
});

/** The signature-help extension; `scope` reads the active tab's target. */
export function signatureHelp(scope: () => CompletionScope | null): Extension {
	let seq = 0;

	const listener = EditorView.updateListener.of((update) => {
		if (!update.docChanged && !update.selectionSet) return;
		const view = update.view;
		const pos = update.state.selection.main.head;
		const ctx = callAt(update.state.doc.toString(), pos);
		if (!ctx) {
			if (view.state.field(helpField)) view.dispatch({ effects: setHelp.of(null) });
			return;
		}
		const current = scope();
		if (!current?.workspaceId) return;
		const ticket = ++seq;
		void loadBindingFunctions(current.workspaceId, current.catalogId, current.bindingId).then(
			(refs) => {
				if (ticket !== seq) return; // a newer cursor position superseded this
				const fn = refs?.find((row) => row.name.toLowerCase() === ctx.name.toLowerCase());
				if (!fn?.signature || view.state.selection.main.head !== pos) {
					if (!fn?.signature && view.state.field(helpField)) {
						view.dispatch({ effects: setHelp.of(null) });
					}
					return;
				}
				view.dispatch({
					effects: setHelp.of({
						name: ctx.name,
						signature: fn.signature,
						argIndex: ctx.argIndex,
						pos
					})
				});
			}
		);
	});

	const tooltip = showTooltip.computeN([helpField], (state) => {
		const help = state.field(helpField);
		if (!help) return [];
		return [
			{
				pos: help.pos,
				above: true,
				arrow: false,
				create: () => {
					const dom = document.createElement('div');
					dom.className = 'cm-signature-help';
					const sig = document.createElement('span');
					sig.className = 'cm-signature-help-text';
					sig.textContent = help.signature;
					const arg = document.createElement('span');
					arg.className = 'cm-signature-help-arg';
					arg.textContent = `arg ${help.argIndex + 1}`;
					dom.append(sig, arg);
					return { dom };
				}
			}
		];
	});

	return [helpField, listener, tooltip];
}
