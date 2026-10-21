// Syntax colors for read-only and embedded editors (FD11). CodeMirror's
// default style renders quoted identifiers, some atoms and invalid tokens
// below WCAG AA (as low as 3.6:1 on the active line); this keeps its palette
// and darkens only those.
import { HighlightStyle, defaultHighlightStyle, syntaxHighlighting } from '@codemirror/language';

const darker: Record<string, string> = { '#e40': '#b33200', '#085': '#00704a', '#f00': '#c00' };

export const accessibleHighlight = syntaxHighlighting(
	HighlightStyle.define(
		defaultHighlightStyle.specs.map((spec) =>
			spec.color && darker[spec.color] ? { ...spec, color: darker[spec.color] } : spec
		)
	)
);
