// CSV export for the Monitoring panels (#820). Values are written as given,
// quoted only when they carry a comma, quote or newline (RFC 4180).

export type CsvValue = string | number | null | undefined;

/**
 * A text cell that starts with one of these is run as a formula by
 * spreadsheets, so it gets a leading apostrophe; numbers are left alone.
 */
const FORMULA = /^\s*[=+@\t\r-]/;

/** Escapes one cell: neutralizes formulas, then applies RFC 4180 quoting. */
function escape(value: CsvValue): string {
	if (typeof value === 'number') return String(value);
	let text = value === null || value === undefined ? '' : String(value);
	if (FORMULA.test(text)) text = `'${text}`;
	return /[",\n\r]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
}

/** Header row plus one line per row. */
export function toCsv(headers: string[], rows: CsvValue[][]): string {
	return [headers, ...rows].map((row) => row.map(escape).join(',')).join('\r\n');
}

/** Saves CSV text through a temporary download link; the BOM keeps Excel in UTF-8. */
export function downloadCsv(filename: string, csv: string): void {
	const blob = new Blob(['\uFEFF', csv], { type: 'text/csv;charset=utf-8' });
	const url = URL.createObjectURL(blob);
	const link = document.createElement('a');
	link.href = url;
	link.download = filename;
	link.click();
	URL.revokeObjectURL(url);
}
