// The metadata search grammar (B6, #561): `key:value` filters plus free text,
// with quoted values and `-key:value` exclusions. It mirrors the backend's
// parameters so the request only carries what the user typed.
export type SearchFilters = {
	text: string;
	kind?: 'table' | 'column' | 'all';
	tags: string[];
	excludeTags: string[];
	certified?: boolean;
	/** One value per namespace level; a quoted value keeps its dots. */
	namespace?: string[];
};

const KEYS = new Set(['type', 'tag', 'certified', 'namespace']);

type Token = { value: string; quoted: boolean };

/** parseSearchFilters splits the input into free text and filters. */
export function parseSearchFilters(input: string): SearchFilters {
	const out: SearchFilters = { text: '', tags: [], excludeTags: [] };
	const free: string[] = [];
	for (const token of tokenize(input)) {
		const negated = token.value.startsWith('-');
		const body = negated ? token.value.slice(1) : token.value;
		const colon = body.indexOf(':');
		if (colon <= 0) {
			free.push(token.value);
			continue;
		}
		const key = body.slice(0, colon).toLowerCase();
		const value = body.slice(colon + 1);
		if (!KEYS.has(key) || value === '') {
			free.push(token.value);
			continue;
		}
		switch (key) {
			case 'type':
				if (!negated && (value === 'table' || value === 'column' || value === 'all')) {
					out.kind = value;
				} else {
					free.push(token.value);
				}
				break;
			case 'tag':
				(negated ? out.excludeTags : out.tags).push(value);
				break;
			case 'certified':
				if (negated) {
					free.push(token.value);
				} else if (value === 'yes' || value === 'true') {
					out.certified = true;
				} else if (value === 'no' || value === 'false') {
					out.certified = false;
				} else {
					// A typo must not silently filter to uncertified tables.
					free.push(token.value);
				}
				break;
			case 'namespace':
				if (negated) {
					free.push(token.value);
				} else {
					out.namespace = token.quoted ? [value] : value.split('.').filter(Boolean);
				}
				break;
		}
	}
	out.text = free.join(' ').trim();
	return out;
}

// tokenize splits on whitespace, keeping a quoted value in one token and
// dropping the quotes.
function tokenize(input: string): Token[] {
	const out: Token[] = [];
	let i = 0;
	while (i < input.length) {
		while (i < input.length && /\s/.test(input[i])) i++;
		if (i >= input.length) break;
		let value = '';
		let quoted = false;
		while (i < input.length) {
			const ch = input[i];
			if (ch === '"' || ch === "'") {
				quoted = true;
				const quote = ch;
				i++;
				while (i < input.length && input[i] !== quote) value += input[i++];
				if (i < input.length) i++;
				continue;
			}
			if (/\s/.test(ch)) break;
			value += ch;
			i++;
		}
		out.push({ value, quoted });
	}
	return out;
}
