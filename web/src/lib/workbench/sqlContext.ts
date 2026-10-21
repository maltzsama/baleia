// Statement context for the workbench autocomplete (#383). A small lexer reads
// the statement under the cursor and says what fits there: a statement keyword,
// a table, a column or a clause keyword. It never parses SQL fully; it only
// tracks clauses per parenthesis depth and the tables named in FROM/JOIN.

export type Expect = 'statement' | 'table' | 'column' | 'keyword';

/** A table named in the statement; `path` is empty for a derived table. */
export type StatementTable = { path: string[]; alias: string };

export type SqlContext = {
	expect: Expect;
	/** Identifiers before the dot(s) at the cursor: `n.na` -> ['n']. */
	qualifier: string[];
	/** The word being typed, and where it starts in the document. */
	prefix: string;
	from: number;
	tables: StatementTable[];
};

type Token = {
	kind: 'word' | 'quoted' | 'string' | 'comment' | 'number' | 'punct';
	/** Identifier value (unquoted for quoted identifiers) or the punctuation. */
	value: string;
	start: number;
	end: number;
	/** A string, comment or quoted identifier that runs to the end of input. */
	open?: boolean;
};

const QUOTE_CLOSE: Record<string, string> = { '"': '"', '`': '`', '[': ']' };

function tokenize(text: string): Token[] {
	const tokens: Token[] = [];
	let i = 0;
	while (i < text.length) {
		const ch = text[i];
		const start = i;
		if (/\s/.test(ch)) {
			i++;
		} else if (ch === '-' && text[i + 1] === '-') {
			const end = text.indexOf('\n', i);
			i = end < 0 ? text.length : end;
			tokens.push({ kind: 'comment', value: 'line', start, end: i, open: end < 0 });
		} else if (ch === '/' && text[i + 1] === '*') {
			const end = text.indexOf('*/', i + 2);
			i = end < 0 ? text.length : end + 2;
			tokens.push({ kind: 'comment', value: '', start, end: i, open: end < 0 });
		} else if (ch === "'") {
			i++;
			let closed = false;
			while (i < text.length) {
				if (text[i] === "'" && text[i + 1] === "'") i += 2;
				else if (text[i] === "'") {
					i++;
					closed = true;
					break;
				} else i++;
			}
			tokens.push({ kind: 'string', value: '', start, end: i, open: !closed });
		} else if (ch in QUOTE_CLOSE) {
			// A doubled closer is an escaped one: "a""b", `a``b`, [a]]b].
			const close = QUOTE_CLOSE[ch];
			let value = '';
			let closed = false;
			i++;
			while (i < text.length) {
				if (text[i] === close && text[i + 1] === close) {
					value += close;
					i += 2;
				} else if (text[i] === close) {
					i++;
					closed = true;
					break;
				} else value += text[i++];
			}
			tokens.push({ kind: 'quoted', value, start, end: i, open: !closed });
		} else if (/[A-Za-z_]/.test(ch)) {
			while (i < text.length && /[\w$]/.test(text[i])) i++;
			tokens.push({ kind: 'word', value: text.slice(start, i), start, end: i });
		} else if (/\d/.test(ch)) {
			while (i < text.length && /[\w.]/.test(text[i])) i++;
			tokens.push({ kind: 'number', value: text.slice(start, i), start, end: i });
		} else {
			i++;
			tokens.push({ kind: 'punct', value: ch, start, end: i });
		}
	}
	return tokens;
}

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
 * What fits at `pos` in `doc`. Returns null inside a string, a comment or a
 * quoted identifier, where nothing is suggested.
 */
export function sqlContextAt(doc: string, pos: number): SqlContext | null {
	const tokens = tokenize(doc);
	for (const token of tokens) {
		if (token.start >= pos) break;
		if (token.kind !== 'string' && token.kind !== 'comment' && token.kind !== 'quoted') continue;
		// A line comment runs up to its newline, so its end is still inside it.
		const lineComment = token.kind === 'comment' && token.value === 'line';
		if (pos < token.end || (pos === token.end && (token.open || lineComment))) return null;
	}

	const statement = statementAt(
		tokens.filter((token) => token.kind !== 'comment'),
		pos
	);
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
	return { expect: expectAt(head, qualifier), qualifier, prefix, from, tables };
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
 * The statement at `pos` as [start, end) offsets, split at `;` outside strings,
 * comments and quoted identifiers. The workbench uses it when the engine's
 * splitter is unavailable, so Run still executes one statement (#395). A
 * cursor after the last statement picks that statement; null on an empty doc.
 */
export function statementSpanAt(doc: string, pos: number): { start: number; end: number } | null {
	const spans: { start: number; end: number }[] = [];
	let start = 0;
	for (const token of tokenize(doc)) {
		if (token.kind === 'punct' && token.value === ';') {
			spans.push({ start, end: token.end });
			start = token.end;
		}
	}
	spans.push({ start, end: doc.length });
	const nonEmpty = spans.filter((span) =>
		doc.slice(span.start, span.end).replace(/;\s*$/, '').trim()
	);
	if (nonEmpty.length === 0) return null;
	return (
		nonEmpty.find((span) => span.start <= pos && pos <= span.end) ??
		[...nonEmpty].reverse().find((span) => span.end <= pos) ??
		nonEmpty[0]
	);
}
