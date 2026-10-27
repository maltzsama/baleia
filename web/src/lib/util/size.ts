// Byte counts for people (#407): binary units, as Trino and the engines
// report memory and data sizes.

const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];

export function formatBytes(bytes: number): string {
	if (!Number.isFinite(bytes) || bytes < 0) return '—';
	let value = bytes;
	let unit = 0;
	while (value >= 1024 && unit < units.length - 1) {
		value /= 1024;
		unit++;
	}
	if (unit === 0) return `${value} B`;
	return `${value.toFixed(value < 10 ? 1 : 0)} ${units[unit]}`;
}
