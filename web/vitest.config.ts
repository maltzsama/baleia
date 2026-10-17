import { sveltekit } from '@sveltejs/kit/vite';
import { svelteTesting } from '@testing-library/svelte/vite';
import { defineConfig } from 'vitest/config';

export default defineConfig({
	plugins: [sveltekit(), svelteTesting()],
	// openapi-fetch builds absolute URLs; jsdom has no base URL to resolve a
	// relative `/api/v1` against.
	define: {
		// Matches jsdom's default origin so MSW's relative handlers resolve.
		'import.meta.env.VITE_API_BASE': JSON.stringify('http://localhost:3000')
	},
	test: {
		environment: 'jsdom',
		server: {
			deps: {
				// MSW must be bundled so its interceptors replace the real fetch.
				inline: ['msw', '@mswjs/interceptors']
			}
		},
		include: ['src/**/*.{test,spec}.{js,ts}', 'src/**/*.svelte.{test,spec}.{js,ts}'],
		setupFiles: ['./src/test/setup.ts'],
		globals: false
	}
});
