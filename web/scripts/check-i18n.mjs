#!/usr/bin/env node
// i18n guard (#67).
//
// 1. en.json and pt-BR.json must have exactly the same keys, and every value
//    must be a non-empty string.
// 2. Every guarded .svelte file must not render hard-coded user-facing text.
//
// The guarded directory list grows as pages are extracted; until then only the
// key-parity check runs. Run from web/ (npm run check).
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse } from 'svelte/compiler';

const root = fileURLToPath(new URL('..', import.meta.url));
const messagesDir = join(root, 'messages');
const locales = ['en', 'pt-BR'];

// Directories extracted so far. Each PR that extracts a page adds its path.
// Example: 'src/routes/login', 'src/lib/components/GlobalNav.svelte'.
const GUARDED = [];

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

const TEXT_ATTRIBUTES = new Set(['title', 'placeholder', 'aria-label', 'alt', 'label']);

function hasLetters(value) {
	return /[A-Za-zÀ-ÿ]/.test(value);
}

function attributeText(attribute) {
	if (attribute.type !== 'Attribute' || !TEXT_ATTRIBUTES.has(attribute.name)) return undefined;
	const parts = attribute.value ?? [];
	if (parts.length !== 1 || parts[0]?.type !== 'Text') return undefined;
	return parts[0].data;
}

function walk(node, file, report) {
	if (!node || typeof node !== 'object') return;

	if (node.type === 'Text' && typeof node.data === 'string') {
		const text = node.data.trim();
		if (hasLetters(text) && !ALLOWED.has(text)) report(file, text);
	}

	for (const attribute of node.attributes ?? []) {
		const text = attributeText(attribute);
		if (text && hasLetters(text) && !ALLOWED.has(text)) report(file, text);
	}

	for (const key of ['children', 'nodes']) {
		for (const child of node[key] ?? []) walk(child, file, report);
	}
	if (node.fragment) walk(node.fragment, file, report);
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
	}).filter((file) => file.endsWith('.svelte'));

	for (const file of files) {
		const source = readFileSync(file, 'utf8');
		const ast = parse(source, { filename: file });
		const seen = new Set();
		walk(ast.html ?? ast.fragment ?? ast, file, (path, text) => {
			if (seen.has(text)) return;
			seen.add(text);
			fail(`${relative(root, path)}: hard-coded text "${text}" (use m.*)`);
		});
	}
}

checkParity();
checkHardCodedStrings();
