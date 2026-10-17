// Type-aware cell formatting (#26). Everything is driven by the Arrow schema
// and the `baleia.native_type` field metadata, never by the engine's name.
import { Type, type Field } from 'apache-arrow';

export type ColumnKind =
	| 'number'
	| 'decimal'
	| 'timestamp'
	| 'date'
	| 'boolean'
	| 'binary'
	| 'nested'
	| 'string'
	| 'other';

export type ColumnMeta = {
	name: string;
	/** The Arrow type, e.g. `Int64` or `Timestamp<MILLISECOND>`. */
	type: string;
	/** The source type when the adapter recorded it. */
	nativeType: string;
	kind: ColumnKind;
	nested: boolean;
	unit?: number;
	scale?: number;
};

export type CellDisplay = {
	text: string;
	align: 'left' | 'right';
	null: boolean;
	nested: boolean;
};

type TimestampType = { unit?: number };
type DecimalType = { scale?: number; precision?: number };
type DictionaryType = { valueType?: { typeId: number } };

function underlying(type: Field['type']): Field['type'] {
	if (type.typeId === Type.Dictionary) {
		const valueType = (type as unknown as DictionaryType).valueType;
		if (valueType) return valueType as Field['type'];
	}
	return type;
}

export function columnMeta(field: Field): ColumnMeta {
	const type = underlying(field.type);
	const nativeType = field.metadata?.get('baleia.native_type') ?? '';
	const kind = kindOf(type.typeId);
	return {
		name: field.name,
		type: field.type.toString(),
		nativeType,
		kind,
		nested: kind === 'nested',
		unit: (type as unknown as TimestampType).unit,
		scale: kind === 'decimal' ? ((type as unknown as DecimalType).scale ?? 0) : undefined
	};
}

function kindOf(typeId: Type): ColumnKind {
	switch (typeId) {
		case Type.Int:
		case Type.Float:
			return 'number';
		case Type.Decimal:
			return 'decimal';
		case Type.Timestamp:
			return 'timestamp';
		case Type.Date:
			return 'date';
		case Type.Bool:
			return 'boolean';
		case Type.Binary:
		case Type.LargeBinary:
		case Type.FixedSizeBinary:
			return 'binary';
		case Type.List:
		case Type.FixedSizeList:
		case Type.Struct:
		case Type.Map:
		case Type.Union:
		case Type.DenseUnion:
		case Type.SparseUnion:
			return 'nested';
		case Type.Utf8:
		case Type.LargeUtf8:
			return 'string';
		default:
			return 'other';
	}
}

const NULL_CELL: CellDisplay = { text: 'NULL', align: 'right', null: true, nested: false };

export function formatCell(value: unknown, column: ColumnMeta): CellDisplay {
	if (value === null || value === undefined) return NULL_CELL;
	switch (column.kind) {
		case 'number':
			return { text: String(value), align: 'right', null: false, nested: false };
		case 'decimal':
			return {
				text: formatDecimal(value, column.scale ?? 0),
				align: 'right',
				null: false,
				nested: false
			};
		case 'timestamp':
			return {
				text: formatTimestamp(value, column.unit),
				align: 'left',
				null: false,
				nested: false
			};
		case 'date':
			return { text: formatDate(value, column.unit), align: 'left', null: false, nested: false };
		case 'boolean':
			return { text: value ? 'true' : 'false', align: 'left', null: false, nested: false };
		case 'binary':
			return { text: '<binary>', align: 'left', null: false, nested: false };
		case 'nested':
			return { text: previewJson(value), align: 'left', null: false, nested: true };
		default:
			return { text: String(value), align: 'left', null: false, nested: false };
	}
}

/** Exact decimal text: Arrow decimals arrive as unscaled integers. */
export function formatDecimal(value: unknown, scale: number): string {
	const text = String(value);
	if (scale <= 0 || !/^-?\d+$/.test(text)) return text;
	const negative = text.startsWith('-');
	const digits = (negative ? text.slice(1) : text).padStart(scale + 1, '0');
	const whole = digits.slice(0, -scale);
	const fraction = digits.slice(-scale);
	return `${negative ? '-' : ''}${whole}.${fraction}`;
}

/** ISO 8601 with the timezone, from the Arrow time unit. */
export function formatTimestamp(value: unknown, unit = 1): string {
	const date = new Date(timestampMillis(value, unit));
	return Number.isNaN(date.getTime()) ? String(value) : date.toISOString();
}

function timestampMillis(value: unknown, unit: number): number {
	const raw = typeof value === 'bigint' ? Number(value) : Number(value);
	switch (unit) {
		case 0:
			return raw * 1000;
		case 2:
			return raw / 1000;
		case 3:
			return raw / 1_000_000;
		default:
			return raw;
	}
}

export function formatDate(value: unknown, unit = 1): string {
	const raw = Number(value);
	const millis = unit === 0 ? raw * 86_400_000 : raw;
	const date = new Date(millis);
	return Number.isNaN(date.getTime()) ? String(value) : date.toISOString().slice(0, 10);
}

function jsonReplacer(_key: string, value: unknown): unknown {
	return typeof value === 'bigint' ? value.toString() : value;
}

/** A compact JSON preview for a nested value. */
export function previewJson(value: unknown, maxLength = 120): string {
	let text: string;
	try {
		text = JSON.stringify(value, jsonReplacer) ?? String(value);
	} catch {
		text = String(value);
	}
	return text.length > maxLength ? `${text.slice(0, maxLength)}…` : text;
}

/** The full, pretty JSON of a nested value (for the expand dialog). */
export function fullJson(value: unknown): string {
	try {
		return JSON.stringify(value, jsonReplacer, 2) ?? String(value);
	} catch {
		return String(value);
	}
}

/** Plain text for the clipboard: numbers and decimals exact, nested as JSON. */
export function cellText(value: unknown, column: ColumnMeta): string {
	if (value === null || value === undefined) return '';
	return formatCell(value, column).text;
}
