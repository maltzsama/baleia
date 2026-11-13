// Access-mode string literals (ADR 0029).
//
// An access mode says how Baleia executes SQL (`direct` or `federated`); it is
// not an engine kind. The UI imports these constants instead of spelling the
// literals out.
import type { components } from '$lib/api/schema';

type ProviderAccessModes = components['schemas']['ConnectionProvider']['access_modes'];
export type AccessMode = NonNullable<ProviderAccessModes>[number];

export const DIRECT: AccessMode = 'direct';
export const FEDERATED: AccessMode = 'federated';
