/** Configured origin for the API, without the `/api/v1` prefix. */
export const API_BASE = import.meta.env.VITE_API_BASE ?? '';

/** Root of the versioned API, used by the session client and raw requests. */
export const API_ROOT = `${API_BASE}/api/v1`;
