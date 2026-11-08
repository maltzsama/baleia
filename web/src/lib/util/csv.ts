// CSV export for the Monitoring panels (#820). Values are written as given,
// quoted only when they carry a comma, quote or newline (RFC 4180).

export type CsvValue = string | number | null | undefined;

function escape(value: CsvValue): string {
	const text = value === null || value === undefined ? '' : String(value);
	return /[",\n\r]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
}

/** Header row plus one line per row. */
export function toCsv(headers: string[], rows: CsvValue[][]): string {
	return [headers, ...rows].map((row) => row.map(escape).join(',')).join('\r\n');
}

/** Saves CSV text through a temporary download link. */
export function downloadCsv(filename: string, csv: string): void {
	const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' });
	const url = URL.createObjectURL(blob);
	const link = document.createElement('a');
	link.href = url;
	link.download = filename;
	link.click();
	URL.revokeObjectURL(url);
}
