// Access-mode string literals (ADR 0021 D.2).
//
// `trino` collides with the Trino engine kind, which the FD6 lint rule keeps
// inside kindIcon.ts. Access modes are a separate closed enum, so — like kind
// literals in kindIcon.ts — they live in this single module the rule excludes,
// and the rest of the UI imports the constants instead of spelling them out.
import type { components } from '$lib/api/schema';

type ProviderAccessModes = components['schemas']['ConnectionProvider']['access_modes'];
export type AccessMode = NonNullable<ProviderAccessModes>[number];

export const DIRECT: AccessMode = 'direct';
export const TRINO: AccessMode = 'trino';
