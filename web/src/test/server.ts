import { setupServer } from 'msw/node';
import { createOpenApiHttp } from 'openapi-msw';
import type { paths } from '$lib/api/schema';

// Typed handler factory: a mock whose response does not match the OpenAPI
// schema fails `npm run check` (openapi-msw forces the response shape).
// The client calls `/api/v1/<path>`; prepending the base here keeps handlers
// written with the schema's paths (e.g. `/workspaces`).
export const http = createOpenApiHttp<paths>({ baseUrl: '/api/v1' });

export const server = setupServer();
