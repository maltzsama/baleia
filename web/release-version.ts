import { readFileSync } from 'node:fs';

// The released version, from the release-please manifest that every release
// bumps (#513). The login page shows it; nothing else hard-codes a version.
export function releaseVersion(): string {
	const manifest = JSON.parse(
		readFileSync(new URL('../.release-please-manifest.json', import.meta.url), 'utf8')
	) as Record<string, string>;
	return manifest['.'];
}
