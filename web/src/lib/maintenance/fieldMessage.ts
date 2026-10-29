// Field errors of the maintenance form (#500), in the form's units: the API
// sends limits in seconds, bytes and ratios.
import { m } from '$lib/paraglide/messages';

const MB = 1024 * 1024;

// How many API units one form unit is, by the form key's suffix.
function divisor(formKey: string): number {
	const key = formKey.split('.').pop() ?? '';
	if (key.endsWith('Minutes')) return 60;
	if (key.endsWith('Hours')) return 3600;
	if (key.endsWith('MB')) return MB;
	if (key.endsWith('Percent')) return 0.01;
	return 1;
}

function inUnits(value: string, formKey: string): string {
	const n = Number(value);
	if (!Number.isFinite(n)) return value;
	return String(Math.round((n / divisor(formKey)) * 100) / 100);
}

export function fieldMessage(
	code: string,
	params: Record<string, string> | undefined,
	formKey: string
): string {
	let base: string;
	switch (code) {
		case 'required':
			base = m.field_required();
			break;
		case 'too_long':
			base = m.field_too_long();
			break;
		case 'invalid_format':
			base = m.field_invalid_format();
			break;
		case 'out_of_range':
			base = m.field_out_of_range();
			break;
		case 'not_unique':
			base = m.field_not_unique();
			break;
		default:
			base = m.field_not_allowed();
	}
	if (code === 'out_of_range' && params?.min !== undefined) {
		return `${base} ${m.maintenance_field_min({ value: inUnits(params.min, formKey) })}`;
	}
	if (code === 'not_allowed' && params?.format_version !== undefined) {
		return `${base} ${m.maintenance_field_needs_v2()}`;
	}
	return base;
}
