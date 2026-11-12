import { paraglideVitePlugin } from '@inlang/paraglide-js';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';
import { releaseVersion } from './release-version';

export default defineConfig({
	define: { __APP_VERSION__: JSON.stringify(releaseVersion()) },
	// Keep Kit's cookie version in the server bundle instead of resolving MSW's hoisted major.
	ssr: { noExternal: ['cookie'] },
	plugins: [
		tailwindcss(),
		// Compiled, tree-shakable messages (#67). A missing m.* key is a
		// TypeScript error because emitTsDeclarations types the generated module.
		paraglideVitePlugin({
			project: './project.inlang',
			outdir: './src/lib/paraglide',
			strategy: ['globalVariable', 'localStorage', 'preferredLanguage', 'baseLocale'],
			emitTsDeclarations: true,
			// Match CLI generation so tests can consume the same physical message files.
			outputStructure: 'message-modules'
		}),
		sveltekit()
	]
});
