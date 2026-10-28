#!/usr/bin/env node
// i18n guard (#67).
//
// 1. en.json and pt-BR.json must have exactly the same keys, and every value
//    must be a non-empty string.
// 2. No guarded file renders hard-coded user-facing text (#469):
//    - .svelte markup: text in every branch ({:else}, {:then}, snippets...),
//      text attributes and their expressions ({'Create'}, {`${n} items`},
//      {ok ? 'Yes' : 'No'});
//    - code, in .svelte scripts, markup expressions and .ts: strings
//      assigned to error/message/label-like names, returned by label-like
//      getters and functions, given as prop defaults or {#each} titles,
//      passed to toasts, apiErrorMessage fallbacks or m.*() parameters.
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
const ALLOWED = new Set([
	'Baleia',
	'SQL',
	'Trino',
	'Iceberg',
	// Join types, shown as SQL.
	'left',
	'inner',
	'right',
	'full',
	'cross'
]);

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
	/^(title|placeholder|alt|label|aria-label|aria-description|aria-roledescription|aria-valuetext|description|message|heading|hint|confirmLabel|cancelLabel|emptyText|emptyTitle|consequences)$/;
// Variables, properties, getters and functions that hold copy.
const TEXT_NAME =
	/^(label|title|description|placeholder|message|heading|hint|explanation|remediation|consequences|body|detail|group|summary|tooltip|confirmLabel|cancelLabel|[a-z]\w*(Error|Message|Label|Title|Hint|Notice|Text))$|^(error|notice)$/;
// Calls whose arguments are rendered: toasts, error fallbacks, messages.
const COPY_CALL = /^(toasts\.\w+|apiErrorMessage|m\.\w+)$/;

function hasLetters(value) {
	// Unicode letters only: "×", "→" and "·" are decorative, not copy.
	return /\p{L}/u.test(value);
}

// Code, not copy: identifiers, keys, routes, MIME types, CSS-ish tokens.
function looksLikeCode(value) {
	const text = value.trim();
	return (
		/^[a-z0-9_.:/#?=&@%+-]*$/.test(text) ||
		/^[A-Z0-9_]+$/.test(text) ||
		// Filters and assignments without spaces: (uid={{username}}), key=value.
		(/^\S+$/.test(text) && text.includes('=')) ||
		// Class lists: lowercase tokens only, at least one utility-shaped.
		(/^[a-z0-9_.:/#%()[\]!-]+(\s+[a-z0-9_.:/#%()[\]!-]+)*$/.test(text) && /[-:[]/.test(text)) ||
		/^(https?:|mailto:|\/)/.test(text) ||
		// camelCase identifiers and anchored patterns: otherTable, ^[a-z]+$.
		/^[a-z]+[A-Z]\w*$/.test(text) ||
		/^\^.*\$$/.test(text) ||
		// Key bindings: Mod-Enter, Shift-Alt-F.
		/^((Shift|Mod|Alt|Ctrl|Meta)-)+\w+$/.test(text)
	);
}

function isCopy(value) {
	const text = value.trim();
	return hasLetters(text) && !ALLOWED.has(text) && !looksLikeCode(text);
}

// Text people read even as one lowercase word ("to", "rows", "yes"): literal
// markup, rendered expressions and toasts. Only class lists, routes and
// filters are code there.
function isRenderedCopy(value) {
	const text = value.trim();
	return hasLetters(text) && !ALLOWED.has(text) && (!looksLikeCode(text) || /^[a-z]+$/.test(text));
}

// Copy in code, through the TypeScript AST: .ts files, <script> blocks and
// every markup expression. `rendered` marks an expression whose value is shown
// ({expr}, a text attribute); `each` marks the list of an {#each} block.
function walkCode(code, report, { rendered = false, each = false } = {}) {
	const file = ts.createSourceFile('x.ts', code, ts.ScriptTarget.Latest, true);
	const nameOf = (node) =>
		!node
			? ''
			: ts.isIdentifier(node) || ts.isStringLiteral(node) || ts.isPrivateIdentifier(node)
				? node.text
				: ts.isPropertyAccessExpression(node)
					? node.name.text
					: '';
	const callee = (node) =>
		ts.isPropertyAccessExpression(node)
			? `${nameOf(node.expression)}.${node.name.text}`
			: nameOf(node);

	// Strings an expression yields: literals through conditionals, fallbacks,
	// concatenation, templates ("{}" per hole), arrays and arrow bodies.
	const strings = (node, out = []) => {
		if (!node) return out;
		if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) out.push(node.text);
		else if (ts.isTemplateExpression(node))
			out.push([node.head.text, ...node.templateSpans.map((span) => span.literal.text)].join('{}'));
		else if (ts.isConditionalExpression(node)) {
			strings(node.whenTrue, out);
			strings(node.whenFalse, out);
		} else if (ts.isBinaryExpression(node)) {
			const op = node.operatorToken.kind;
			if (
				op === ts.SyntaxKind.PlusToken ||
				op === ts.SyntaxKind.BarBarToken ||
				op === ts.SyntaxKind.QuestionQuestionToken
			) {
				strings(node.left, out);
				strings(node.right, out);
			}
		} else if (ts.isArrayLiteralExpression(node)) node.elements.forEach((e) => strings(e, out));
		else if (
			ts.isParenthesizedExpression(node) ||
			ts.isAsExpression(node) ||
			ts.isNonNullExpression(node) ||
			(ts.isSatisfiesExpression && ts.isSatisfiesExpression(node))
		)
			strings(node.expression, out);
		else if (ts.isArrowFunction(node) || ts.isFunctionExpression(node)) returns(node, out);
		else if (
			ts.isCallExpression(node) &&
			/^\$(state|derived)(\.by)?$/.test(callee(node.expression))
		)
			strings(node.arguments[0], out);
		return out;
	};
	// What a function returns.
	const returns = (fn, out = []) => {
		if (!fn.body) return out;
		if (!ts.isBlock(fn.body)) return strings(fn.body, out);
		const visit = (node) => {
			if (ts.isReturnStatement(node)) strings(node.expression, out);
			else if (!ts.isFunctionLike(node)) ts.forEachChild(node, visit);
		};
		ts.forEachChild(fn.body, visit);
		return out;
	};
	const check = (texts, rule = isCopy) => {
		for (const text of texts) if (rule(text)) report(text.trim());
	};

	const visit = (node) => {
		if (ts.isPropertyAssignment(node) && TEXT_NAME.test(nameOf(node.name)))
			check(strings(node.initializer));
		else if (
			(ts.isGetAccessorDeclaration(node) ||
				ts.isFunctionDeclaration(node) ||
				ts.isMethodDeclaration(node)) &&
			TEXT_NAME.test(nameOf(node.name))
		)
			// A label function returns what people read: "row", "rows".
			check(returns(node), isRenderedCopy);
		else if (
			(ts.isVariableDeclaration(node) || ts.isBindingElement(node)) &&
			TEXT_NAME.test(nameOf(node.name))
		)
			// Covers $state('...'), $derived(...) and a prop default (title = '...').
			check(strings(node.initializer));
		else if (
			ts.isBinaryExpression(node) &&
			node.operatorToken.kind === ts.SyntaxKind.EqualsToken &&
			TEXT_NAME.test(nameOf(node.left))
		)
			check(strings(node.right));
		else if (ts.isCallExpression(node) && COPY_CALL.test(callee(node.expression))) {
			const name = callee(node.expression);
			// apiErrorMessage(error, fallback): only the fallback is copy.
			const args = name === 'apiErrorMessage' ? node.arguments.slice(1) : node.arguments;
			for (const arg of args) {
				if (ts.isObjectLiteralExpression(arg))
					for (const prop of arg.properties)
						if (ts.isPropertyAssignment(prop)) check(strings(prop.initializer), isRenderedCopy);
						else continue;
				else check(strings(arg), isRenderedCopy);
			}
		}
		ts.forEachChild(node, visit);
	};

	const root = file.statements[0];
	let expression = root && ts.isExpressionStatement(root) ? root.expression : undefined;
	while (expression && ts.isParenthesizedExpression(expression)) expression = expression.expression;
	if (rendered && expression) check(strings(expression), isRenderedCopy);
	// {#each [['overview', 'Overview'], ...] as ...}: the titles are copy, the ids code.
	if (each && expression && ts.isArrayLiteralExpression(expression)) check(strings(expression));
	visit(file);
}

function attributeTexts(attribute) {
	const value = attribute.value;
	if (value === true || value == null) return [];
	const parts = Array.isArray(value) ? value : [value];
	return parts.filter((part) => part.type === 'Text').map((part) => part.data);
}

function attributeExpressions(attribute) {
	const value = attribute.value;
	if (value === true || value == null) return [];
	const parts = Array.isArray(value) ? value : [value];
	return parts.filter((part) => part.type === 'ExpressionTag').map((part) => part.expression);
}

function walkMarkup(node, source, report, skip = false) {
	if (!node || typeof node !== 'object') return;
	if (Array.isArray(node)) {
		for (const child of node) walkMarkup(child, source, report, skip);
		return;
	}
	const name = node.name ?? '';
	const nextSkip =
		skip ||
		((node.type === 'RegularElement' || node.type === 'Component') &&
			(SKIP_TAGS.has(name) || SKIP_COMPONENTS.test(name)));
	const code = (expression, options) =>
		walkCode(`(${source.slice(expression.start, expression.end)})`, report, options);

	if (!nextSkip) {
		if (node.type === 'Text') {
			const text = node.data.trim();
			if (isRenderedCopy(text)) report(text);
		} else if (node.type === 'ExpressionTag') code(node.expression, { rendered: true });
		else if (node.type === 'EachBlock') code(node.expression, { each: true });
		else if (node.expression && typeof node.expression.start === 'number') code(node.expression);
	}

	for (const attribute of node.attributes ?? []) {
		if (nextSkip) break;
		const isText = attribute.type === 'Attribute' && TEXT_ATTRIBUTE.test(attribute.name);
		if (isText)
			for (const text of attributeTexts(attribute))
				// A placeholder often shows an example value: "orders", "cn".
				if ((attribute.name === 'placeholder' ? isCopy : isRenderedCopy)(text)) report(text.trim());
		if (attribute.type === 'Attribute')
			for (const expression of attributeExpressions(attribute))
				code(expression, { rendered: isText && attribute.name !== 'placeholder' });
		else if (attribute.expression && typeof attribute.expression.start === 'number')
			code(attribute.expression);
	}

	for (const [key, value] of Object.entries(node)) {
		// Expressions are checked above.
		if (key === 'attributes' || key === 'expression' || key === 'metadata') continue;
		if (value && typeof value === 'object') walkMarkup(value, source, report, nextSkip);
	}
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
		walkMarkup(ast.fragment, source, report);
		for (const script of [ast.instance, ast.module]) {
			if (!script) continue;
			walkCode(source.slice(script.content.start, script.content.end), report);
		}
	} else if (path.endsWith('.ts') && !SCRIPT_SKIP.some((pattern) => pattern.test(path))) {
		walkCode(source, report);
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
