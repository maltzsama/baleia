// Statement context for the workbench autocomplete (#383). sqlingo (see
// ./sqlingo.ts) tokenizes with the engine's dialect; what remains here is the
// editor glue that maps tokens to what fits at the cursor: a statement keyword,
// a table, a column or a clause keyword. It never parses SQL fully.

import { statementSpans, tokenize, usesHashComments, type Token } from './sqlingo';

export type Expect = 'statement' | 'table' | 'column' | 'keyword';

/** A table named in the statement; `path` is empty for a derived table. */
export type StatementTable = { path: string[]; alias: string };

/** The tables around a `JOIN <table> ON` at the cursor (#555). */
export type JoinOnContext = {
	/** The table the JOIN just introduced. */
	joined: StatementTable;
	/** The tables already in the statement (earlier joins included). */
	others: StatementTable[];
};

export type SqlContext = {
	expect: Expect;
	/** Identifiers before the dot(s) at the cursor: `n.na` -> ['n']. */
	qualifier: string[];
	/** The word being typed, and where it starts in the document. */
	prefix: string;
	from: number;
	tables: StatementTable[];
	/** Set when the cursor is right after the ON of a JOIN (#555). */
	joinOn?: JoinOnContext;
};

type Clause = 'none' | 'select' | 'from' | 'column' | 'keyword';

// Keywords that open a clause, and what the clause holds.
const CLAUSES: Record<string, Clause> = {
	SELECT: 'select',
	FROM: 'from',
	JOIN: 'from',
	UPDATE: 'from',
	INTO: 'from',
	TABLE: 'from',
	WHERE: 'column',
	ON: 'column',
	HAVING: 'column',
	BY: 'column',
	SET: 'column',
	AND: 'column',
	OR: 'column',
	WHEN: 'column',
	THEN: 'column',
	ELSE: 'column',
	LIMIT: 'keyword',
	OFFSET: 'keyword',
	FETCH: 'keyword',
	VALUES: 'keyword',
	UNION: 'none',
	INTERSECT: 'none',
	EXCEPT: 'none',
	WITH: 'none'
};

// Words that cannot be a table alias: they continue the statement instead.
const NOT_ALIAS = new Set([
	'WHERE',
	'JOIN',
	'ON',
	'USING',
	'LEFT',
	'RIGHT',
	'INNER',
	'OUTER',
	'FULL',
	'CROSS',
	'NATURAL',
	'LATERAL',
	'GROUP',
	'BY',
	'ORDER',
	'HAVING',
	'LIMIT',
	'OFFSET',
	'FETCH',
	'UNION',
	'INTERSECT',
	'EXCEPT',
	'WINDOW',
	'SET',
	'VALUES',
	'SELECT',
	'TABLESAMPLE',
	'FOR',
	'QUALIFY'
]);

const JOIN_CONDITION = new Set(['ON', 'AND', 'OR', 'WHEN', 'THEN', 'ELSE']);

const isWord = (token: Token | undefined) => token?.kind === 'word' || token?.kind === 'quoted';
const upper = (token: Token | undefined) =>
	token?.kind === 'word' ? token.value.toUpperCase() : '';

/** The tokens of the statement that holds `pos` (statements end at `;`). */
function statementAt(tokens: Token[], pos: number): Token[] {
	let begin = 0;
	for (let i = 0; i < tokens.length; i++) {
		const token = tokens[i];
		if (token.kind !== 'punct' || token.value !== ';') continue;
		if (token.end <= pos) begin = i + 1;
		else return tokens.slice(begin, i);
	}
	return tokens.slice(begin);
}

/** Reads `a.b.c` starting at `i`; returns the parts and the index after them. */
function readPath(tokens: Token[], i: number): { path: string[]; next: number } {
	const path: string[] = [];
	while (isWord(tokens[i])) {
		path.push(tokens[i].value);
		if (tokens[i + 1]?.kind === 'punct' && tokens[i + 1].value === '.') i += 2;
		else return { path, next: i + 1 };
	}
	return { path, next: i };
}

/** The tables named in FROM/JOIN/UPDATE/INTO at any depth, with their alias. */
function statementTables(tokens: Token[]): StatementTable[] {
	const tables: StatementTable[] = [];
	// Per parenthesis depth: whether a comma there starts another table.
	const inFrom: boolean[] = [false];
	let i = 0;
	while (i < tokens.length) {
		const token = tokens[i];
		const word = upper(token);
		if (token.kind === 'punct' && token.value === '(') {
			inFrom.push(false);
			i++;
			continue;
		}
		if (token.kind === 'punct' && token.value === ')') {
			if (inFrom.length > 1) inFrom.pop();
			i++;
			// A derived table: `FROM (...) alias`.
			if (inFrom[inFrom.length - 1]) {
				const { alias, next } = readAlias(tokens, i);
				if (alias) tables.push({ path: [], alias });
				i = next;
			}
			continue;
		}
		const opens =
			['FROM', 'JOIN', 'UPDATE', 'INTO'].includes(word) ||
			(token.kind === 'punct' && token.value === ',' && inFrom[inFrom.length - 1]);
		// A join condition stays in the FROM clause: `JOIN b ON a.x = b.x, c`.
		if (word && CLAUSES[word] !== undefined && !JOIN_CONDITION.has(word))
			inFrom[inFrom.length - 1] = CLAUSES[word] === 'from';
		if (word === 'TABLE') inFrom[inFrom.length - 1] = false;
		if (!opens) {
			i++;
			continue;
		}
		i++;
		const { path, next } = readPath(tokens, i);
		if (path.length === 0) continue;
		const { alias, next: after } = readAlias(tokens, next);
		tables.push({ path, alias: alias || path[path.length - 1] });
		i = after;
	}
	return tables;
}

function readAlias(tokens: Token[], i: number): { alias: string; next: number } {
	if (upper(tokens[i]) === 'AS') i++;
	const token = tokens[i];
	if (!isWord(token) || NOT_ALIAS.has(upper(token))) return { alias: '', next: i };
	return { alias: token.value, next: i + 1 };
}

/**
 * What fits at `pos` in `doc`, for the engine's dialect. Returns null inside a
 * string, a comment or a quoted identifier, and on unterminated input, where
 * nothing is suggested.
 */
export async function sqlContextAt(
	name: string | null | undefined,
	doc: string,
	pos: number
): Promise<SqlContext | null> {
	let tokens: Token[];
	try {
		tokens = await tokenize(name, doc);
	} catch {
		// An unterminated string/comment/quoted identifier anywhere breaks
		// sqlingo's whole-text tokenizer. Only the statement under the cursor
		// matters: retry with the prefix up to the cursor. If that is also
		// unterminated, the open construct is at or before the cursor and there
		// is nothing to suggest.
		try {
			tokens = await tokenize(name, doc.slice(0, pos));
		} catch {
			return null;
		}
	}

	for (const token of tokens) {
		if (token.start >= pos) break;
		if (token.kind !== 'string' && token.kind !== 'quoted') continue;
		if (pos < token.end) return null;
	}
	// sqlingo attaches comments to tokens without their spans, so the editor
	// finds comment boundaries itself (see commentSpanAt).
	if (commentSpanAt(name, tokens, doc, pos)) return null;

	const statement = statementAt(tokens, pos);
	const before = statement.filter((token) => token.end <= pos);
	let last = before.length - 1;

	// The word being typed ends at the cursor.
	let prefix = '';
	let from = pos;
	if (before[last]?.kind === 'word' && before[last].end === pos) {
		prefix = before[last].value;
		from = before[last].start;
		last--;
	} else if (before[last]?.kind === 'number' && before[last].end === pos) {
		return null;
	}

	// Qualifier: `a.b.` before the word.
	const qualifier: string[] = [];
	while (before[last]?.kind === 'punct' && before[last].value === '.' && isWord(before[last - 1])) {
		qualifier.unshift(before[last - 1].value);
		last -= 2;
	}

	const head = before.slice(0, last + 1);
	const tables = statementTables(statement);
	const expect = expectAt(head, qualifier);
	// Right after `JOIN <table> ON` with nothing typed: offer the join
	// conditions the workspace used (#555). Once a word is typed, the column
	// completion takes over.
	const joinOn =
		expect === 'column' && prefix === '' && qualifier.length === 0
			? joinOnAt(head, tables)
			: undefined;
	return { expect, qualifier, prefix, from, tables, joinOn };
}

/** Whether `pos` is inside a `--` line comment, a block comment, or (for MySQL
 *  and MariaDB) a `#` line comment, outside any string or quoted identifier.
 *  sqlingo attaches comments to tokens without their spans, so this scans the
 *  text, skipping the spans sqlingo located. Block comments do not nest — the
 *  same rule sqlingo's own tokenizer applies. */
function commentSpanAt(
	name: string | null | undefined,
	tokens: Token[],
	doc: string,
	pos: number
): boolean {
	const protectedSpans = tokens
		.filter((token) => token.kind === 'string' || token.kind === 'quoted')
		.sort((a, b) => a.start - b.start);
	const hashComment = usesHashComments(name);
	let i = 0;
	let spanIdx = 0;
	while (i < pos) {
		// Skip over the next string/quoted span, advancing the cursor through the
		// sorted spans instead of re-scanning them for every character.
		const span = protectedSpans[spanIdx];
		if (span && i >= span.start) {
			i = Math.max(i, span.end);
			spanIdx++;
			continue;
		}
		if (doc[i] === '-' && doc[i + 1] === '-') {
			const end = doc.indexOf('\n', i);
			if (pos <= (end < 0 ? doc.length : end)) return true;
			i = end < 0 ? doc.length : end;
		} else if (doc[i] === '/' && doc[i + 1] === '*') {
			const end = doc.indexOf('*/', i + 2);
			const stop = end < 0 ? doc.length : end + 2;
			if (pos <= stop) return true;
			i = stop;
		} else if (hashComment && doc[i] === '#') {
			const end = doc.indexOf('\n', i);
			if (pos <= (end < 0 ? doc.length : end)) return true;
			i = end < 0 ? doc.length : end;
		} else {
			i++;
		}
	}
	return false;
}

/** The JOIN being completed when `head` ends with its ON, else undefined. */
function joinOnAt(head: Token[], tables: StatementTable[]): JoinOnContext | undefined {
	if (tables.length < 2 || upper(head[head.length - 1]) !== 'ON') return undefined;
	return { joined: tables[tables.length - 1], others: tables.slice(0, -1) };
}

function expectAt(head: Token[], qualifier: string[]): Expect {
	// The clause in effect at the cursor's depth.
	const stack: Clause[] = ['none'];
	for (const token of head) {
		if (token.kind === 'punct' && token.value === '(') {
			// Function arguments and expressions keep the column context; a
			// subquery switches it at its SELECT.
			const outer = stack[stack.length - 1];
			stack.push(outer === 'select' || outer === 'column' ? 'column' : 'none');
		} else if (token.kind === 'punct' && token.value === ')') {
			if (stack.length > 1) stack.pop();
		} else {
			const clause = CLAUSES[upper(token)];
			if (clause !== undefined) stack[stack.length - 1] = clause;
		}
	}
	const clause = stack[stack.length - 1];
	const prev = head[head.length - 1];
	const prevWord = upper(prev);

	if (clause === 'from') {
		if (qualifier.length > 0) return 'table';
		const opensTable =
			!prev ||
			['FROM', 'JOIN', 'UPDATE', 'INTO', 'TABLE'].includes(prevWord) ||
			(prev.kind === 'punct' && prev.value === ',');
		if (opensTable) return 'table';
		if (prev.kind === 'punct' && prev.value === '(') return 'statement';
		return 'keyword';
	}
	if (clause === 'select' || clause === 'column') return 'column';
	if (clause === 'keyword') return 'keyword';
	if (qualifier.length > 0) return 'column';
	// Nothing yet, or a fresh `(`: a new statement or subquery starts here.
	if (!prev || (prev.kind === 'punct' && prev.value === '(')) return 'statement';
	return 'keyword';
}

/**
 * The statement at `pos` as [start, end) offsets, split at `;` with the engine's
 * dialect rules (#395, #756). A cursor after the last statement picks that
 * statement; null when no statement is non-empty.
 */
export async function statementSpanAt(
	name: string | null | undefined,
	doc: string,
	pos: number
): Promise<{ start: number; end: number } | null> {
	const spans = await statementSpans(name, doc);
	if (spans.length === 0) return null;
	return (
		spans.find((span) => span.start <= pos && pos < span.end) ??
		[...spans].reverse().find((span) => span.end <= pos) ??
		spans[0]
	);
}
