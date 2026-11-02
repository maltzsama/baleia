// The metadata search grammar (B6, #561): `key:value` filters plus free text,
// with quoted values and `-key:value` exclusions. It mirrors the backend's
// parameters so the request only carries what the user typed.
export type SearchFilters = {
	text: string;
	kind?: 'table' | 'column' | 'all';
	tags: string[];
	excludeTags: string[];
	certified?: boolean;
	namespace?: string;
};

const KEYS = new Set(['type', 'tag', 'certified', 'namespace']);

/** parseSearchFilters splits the input into free text and filters. */
export function parseSearchFilters(input: string): SearchFilters {
	const out: SearchFilters = { text: '', tags: [], excludeTags: [] };
	const free: string[] = [];
	for (const token of tokenize(input)) {
		const negated = token.startsWith('-');
		const body = negated ? token.slice(1) : token;
		const colon = body.indexOf(':');
		if (colon <= 0) {
			free.push(token);
			continue;
		}
		const key = body.slice(0, colon).toLowerCase();
		const value = body.slice(colon + 1);
		if (!KEYS.has(key) || value === '') {
			free.push(token);
			continue;
		}
		switch (key) {
			case 'type':
				if (!negated && (value === 'table' || value === 'column' || value === 'all')) {
					out.kind = value;
				} else {
					free.push(token);
				}
				break;
			case 'tag':
				(negated ? out.excludeTags : out.tags).push(value);
				break;
			case 'certified':
				if (!negated) out.certified = value === 'yes' || value === 'true';
				break;
			case 'namespace':
				if (!negated) out.namespace = value;
				break;
		}
	}
	out.text = free.join(' ').trim();
	return out;
}

// tokenize splits on whitespace, keeping a quoted value in one token and
// dropping the quotes.
function tokenize(input: string): string[] {
	const out: string[] = [];
	let i = 0;
	while (i < input.length) {
		while (i < input.length && /\s/.test(input[i])) i++;
		if (i >= input.length) break;
		let token = '';
		while (i < input.length) {
			const ch = input[i];
			if (ch === '"' || ch === "'") {
				const quote = ch;
				i++;
				while (i < input.length && input[i] !== quote) token += input[i++];
				if (i < input.length) i++;
				continue;
			}
			if (/\s/.test(ch)) break;
			token += ch;
			i++;
		}
		out.push(token);
	}
	return out;
}
