import { paraglideVitePlugin } from '@inlang/paraglide-js';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [
		tailwindcss(),
		// Compiled, tree-shakable messages (#67). A missing m.* key is a
		// TypeScript error because emitTsDeclarations types the generated module.
		paraglideVitePlugin({
			project: './project.inlang',
			outdir: './src/lib/paraglide',
			strategy: ['globalVariable', 'localStorage', 'preferredLanguage', 'baseLocale'],
			emitTsDeclarations: true
		}),
		sveltekit()
	]
});
