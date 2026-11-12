// Why a table the source lists cannot be read (#977). The API sends a stable
// code and the source's own table kind; the text is built here so it follows
// the language of the session.
import { m } from '$lib/paraglide/messages';
import type { MetadataTableUnsupported } from './client';

/** The tooltip text for an unsupported table. */
export function unsupportedReason(reason: MetadataTableUnsupported): string {
	switch (reason.code) {
		case 'table_kind':
			return m.metadata_unsupported_table_kind({ kind: reason.detail ?? '' });
		case 'registration':
			return m.metadata_unsupported_table_registration({ registration: reason.detail ?? '' });
		default:
			return m.metadata_unsupported_table_refused();
	}
}
