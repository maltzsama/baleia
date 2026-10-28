#!/usr/bin/env node
// i18n guard (#67).
//
// 1. en.json and pt-BR.json must have exactly the same keys, and every value
//    must be a non-empty string.
// 2. No guarded file renders hard-coded user-facing text (#469):
//    - .svelte markup: text in every branch ({:else}, {:then}, snippets...),
//      text attributes and their expressions ({'Create'}, {`${n} items`},
//      {ok ? 'Yes' : 'No'});
//    - script code, in .svelte and .ts: strings assigned to error/message/
//      label-like variables, passed to toasts, or set as label-like object
//      properties (label tables such as settings/sections.ts).
//
// Run from web/ (npm run check).
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { parse } from 'svelte/compiler';
import ts from 'typescript';

// Imported by a test, the module URL may not be a file URL; web/ is the cwd then.
const root = import.meta.url.startsWith('file:')
	? fileURLToPath(new URL('..', import.meta.url))
	: process.cwd();
const messagesDir = join(root, 'messages');
const locales = ['en', 'pt-BR'];

// Every product surface is extracted (#67). Dev-only galleries stay out: they
// exist to show literal examples and are never shipped.
const GUARDED = ['src'];
const SKIP_FILES = [/patterns\.examples\.svelte$/, /\/routes\/_dev\//];

// Code samples are not translated: SQL, identifiers and product names.
const SKIP_TAGS = new Set(['code', 'pre']);
const SKIP_COMPONENTS =
	/^(CodeBlock|MarkdownView|MarkdownEditor|DiffView|JsonView|SqlEditor|CodeMirror)$/;

// Intentional literals: product names and SQL/code vocabulary.
const ALLOWED = new Set(['Baleia', 'SQL', 'Trino', 'Iceberg']);

function fail(message) {
	console.error(`check-i18n: ${message}`);
	process.exitCode = 1;
}

function readMessages(locale) {
	const path = join(messagesDir, `${locale}.json`);
	try {
		return JSON.parse(readFileSync(path, 'utf8'));
	} catch (error) {
		fail(`${relative(root, path)}: ${error.message}`);
		return {};
	}
}

function checkParity() {
	const messages = Object.fromEntries(locales.map((locale) => [locale, readMessages(locale)]));
	const [base, ...others] = locales.map((locale) => ({
		locale,
		keys: Object.keys(messages[locale]).filter((key) => key !== '$schema')
	}));

	for (const { locale, keys } of others) {
		const baseKeys = new Set(base.keys);
		const keysSet = new Set(keys);
		for (const key of base.keys) {
			if (!keysSet.has(key)) fail(`${locale}.json is missing "${key}"`);
		}
		for (const key of keys) {
			if (!baseKeys.has(key)) fail(`en.json is missing "${key}"`);
		}
	}

	for (const locale of locales) {
		for (const [key, value] of Object.entries(messages[locale])) {
			if (key === '$schema') continue;
			if (!/^[a-z][a-z0-9_]*$/.test(key)) fail(`${locale}.json: "${key}" is not snake_case`);
			if (typeof value !== 'string' || value.trim() === '')
				fail(`${locale}.json: "${key}" is empty`);
		}
	}
}

function walkFiles(path, out = []) {
	for (const entry of readdirSync(path)) {
		const full = join(path, entry);
		if (statSync(full).isDirectory()) walkFiles(full, out);
		else out.push(full);
	}
	return out;
}

// Attributes and props that carry copy.
const TEXT_ATTRIBUTE =
	/^(title|placeholder|alt|label|aria-label|aria-description|aria-roledescription|aria-valuetext|description|message|heading|hint|confirmLabel|cancelLabel|emptyText|emptyTitle)$/;
// Variables and object properties that hold copy.
const TEXT_NAME =
	/^(label|title|description|placeholder|message|heading|hint|explanation|remediation|confirmLabel|cancelLabel|[a-z]\w*(Error|Message|Label|Title|Hint|Notice))$|^(error|notice)$/;

function hasLetters(value) {
	// Unicode letters only: "×", "→" and "·" are decorative, not copy.
	return /\p{L}/u.test(value);
}

// Code, not copy: identifiers, keys, routes, MIME types, CSS-ish tokens.
function looksLikeCode(value) {
	const text = value.trim();
	return (
		/^[a-z0-9_.:/#?=&@%+-]*$/.test(text) ||
		// Class lists: lowercase tokens only, at least one utility-shaped.
		(/^[a-z0-9_.:/#%()[\]!-]+(\s+[a-z0-9_.:/#%()[\]!-]+)*$/.test(text) && /[-:[]/.test(text)) ||
		/^[A-Z0-9_]+$/.test(text) ||
		// Filters and assignments without spaces: (uid={{username}}), key=value.
		(/^\S+$/.test(text) && text.includes('=')) ||
		/^(https?:|mailto:|\/)/.test(text)
	);
}

function isCopy(value) {
	const text = value.trim();
	return hasLetters(text) && !ALLOWED.has(text) && !looksLikeCode(text);
}

// Literal markup text is read by people even as one lowercase word ("to",
// "rows"); only class lists, routes and filters are code there.
function isMarkupCopy(value) {
	const text = value.trim();
	return hasLetters(text) && !ALLOWED.has(text) && (!looksLikeCode(text) || /^[a-z]+$/.test(text));
}

// The strings an expression can render: literals reached through
// conditionals, fallbacks, concatenation and template literals, but not
// through calls, member access or comparisons.
function displayed(node, out = []) {
	if (!node) return out;
	switch (node.type) {
		case 'Literal':
			if (typeof node.value === 'string') out.push(node.value);
			break;
		case 'StringLiteral':
			out.push(node.value);
			break;
		case 'TemplateLiteral': {
			// "{}" stands for each expression: `${n} items` reads "{} items", copy.
			const text = node.quasis.map((quasi) => quasi.value.cooked ?? '').join('{}');
			if (text.trim()) out.push(text);
			break;
		}
		case 'ConditionalExpression':
			displayed(node.consequent, out);
			displayed(node.alternate, out);
			break;
		case 'LogicalExpression':
			displayed(node.left, out);
			displayed(node.right, out);
			break;
		case 'BinaryExpression':
			if (node.operator === '+') {
				displayed(node.left, out);
				displayed(node.right, out);
			}
			break;
		case 'TSAsExpression':
		case 'TSSatisfiesExpression':
		case 'TSNonNullExpression':
			displayed(node.expression, out);
			break;
	}
	return out;
}

function attributeStrings(attribute) {
	const value = attribute.value;
	if (value === true || value == null) return [];
	const parts = Array.isArray(value) ? value : [value];
	return parts.flatMap((part) =>
		part.type === 'Text'
			? // A placeholder often shows an example value: "orders", "cn".
				[part.data].filter(attribute.name === 'placeholder' ? isCopy : isMarkupCopy)
			: part.type === 'ExpressionTag'
				? displayed(part.expression).filter(isCopy)
				: []
	);
}

function walkMarkup(node, report, skip = false) {
	if (!node || typeof node !== 'object') return;
	if (Array.isArray(node)) {
		for (const child of node) walkMarkup(child, report, skip);
		return;
	}
	const name = node.name ?? '';
	const nextSkip =
		skip ||
		((node.type === 'RegularElement' || node.type === 'Component') &&
			(SKIP_TAGS.has(name) || SKIP_COMPONENTS.test(name)));

	if (!nextSkip) {
		if (node.type === 'Text') {
			const text = node.data.trim();
			if (isMarkupCopy(text)) report(text);
		} else if (node.type === 'ExpressionTag') {
			for (const text of displayed(node.expression)) if (isCopy(text)) report(text);
		}
		for (const attribute of node.attributes ?? []) {
			if (attribute.type !== 'Attribute' || !TEXT_ATTRIBUTE.test(attribute.name)) continue;
			for (const text of attributeStrings(attribute)) report(text.trim());
		}
	}

	for (const [key, value] of Object.entries(node)) {
		// Expressions are checked above, only where they render.
		if (key === 'attributes' || key === 'expression' || key === 'metadata') continue;
		if (value && typeof value === 'object') walkMarkup(value, report, nextSkip);
	}
}

// Script code, through the TypeScript AST (works for .ts and <script>).
function walkScript(source, fileName, report) {
	const file = ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true);
	const strings = (node) => {
		if (!node) return [];
		if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return [node.text];
		if (ts.isTemplateExpression(node))
			return [[node.head.text, ...node.templateSpans.map((span) => span.literal.text)].join('{}')];
		if (ts.isConditionalExpression(node))
			return [...strings(node.whenTrue), ...strings(node.whenFalse)];
		if (ts.isBinaryExpression(node)) {
			const op = node.operatorToken.kind;
			if (
				op === ts.SyntaxKind.PlusToken ||
				op === ts.SyntaxKind.BarBarToken ||
				op === ts.SyntaxKind.QuestionQuestionToken
			)
				return [...strings(node.left), ...strings(node.right)];
		}
		if (
			ts.isParenthesizedExpression(node) ||
			ts.isAsExpression(node) ||
			ts.isSatisfiesExpression?.(node)
		)
			return strings(node.expression);
		if (ts.isArrowFunction(node) && !ts.isBlock(node.body)) return strings(node.body);
		return [];
	};
	const check = (node) => {
		for (const text of strings(node)) if (isCopy(text)) report(text);
	};
	const nameOf = (node) =>
		node && (ts.isIdentifier(node) || ts.isStringLiteral(node) || ts.isPrivateIdentifier(node))
			? node.text
			: ts.isPropertyAccessExpression(node)
				? node.name.text
				: '';
	const visit = (node) => {
		if (ts.isPropertyAssignment(node) && TEXT_NAME.test(nameOf(node.name))) check(node.initializer);
		else if (ts.isVariableDeclaration(node) && TEXT_NAME.test(nameOf(node.name))) {
			// $state('...') / $derived(...) wrap the initial value.
			const init = node.initializer;
			check(init && ts.isCallExpression(init) ? init.arguments[0] : init);
		} else if (
			ts.isBinaryExpression(node) &&
			node.operatorToken.kind === ts.SyntaxKind.EqualsToken &&
			TEXT_NAME.test(nameOf(node.left))
		)
			check(node.right);
		else if (
			ts.isCallExpression(node) &&
			ts.isPropertyAccessExpression(node.expression) &&
			nameOf(node.expression.expression) === 'toasts'
		)
			node.arguments.forEach(check);
		ts.forEachChild(node, visit);
	};
	visit(file);
}

const SCRIPT_SKIP = [
	/\.(spec|test)\.ts$/,
	/^src\/lib\/paraglide\//,
	/^src\/lib\/api\/schema\.d\.ts$/,
	/^src\/test\//,
	/\.d\.ts$/
];

/** Hard-coded copy in one file (path relative to web/), in source order. */
export function findHardCodedText(path, source) {
	const found = [];
	const report = (text) => {
		if (!found.includes(text)) found.push(text);
	};
	if (path.endsWith('.svelte')) {
		const ast = parse(source, { filename: path, modern: true });
		walkMarkup(ast.fragment, report);
		for (const script of [ast.instance, ast.module]) {
			if (!script) continue;
			walkScript(source.slice(script.content.start, script.content.end), `${path}.ts`, report);
		}
	} else if (path.endsWith('.ts') && !SCRIPT_SKIP.some((pattern) => pattern.test(path))) {
		walkScript(source, path, report);
	}
	return found;
}

function checkHardCodedStrings() {
	const files = GUARDED.flatMap((entry) => {
		const path = join(root, entry);
		try {
			return statSync(path).isDirectory() ? walkFiles(path) : [path];
		} catch {
			fail(`guarded path does not exist: ${entry}`);
			return [];
		}
	}).filter((file) => !SKIP_FILES.some((pattern) => pattern.test(relative(root, file))));

	for (const file of files) {
		const path = relative(root, file);
		for (const text of findHardCodedText(path, readFileSync(file, 'utf8')))
			fail(`${path}: hard-coded text "${text}" (use m.*)`);
	}
}

// Tests import findHardCodedText; only a direct run checks the tree.
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
	checkParity();
	checkHardCodedStrings();
}
