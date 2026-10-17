import { sveltekit } from '@sveltejs/kit/vite';
import { svelteTesting } from '@testing-library/svelte/vite';
import { defineConfig } from 'vitest/config';

export default defineConfig({
	plugins: [sveltekit(), svelteTesting()],
	// openapi-fetch builds absolute URLs; jsdom has no base URL to resolve a
	// relative `/api/v1` against.
	define: {
		'import.meta.env.VITE_API_BASE': JSON.stringify('http://localhost')
	},
	test: {
		environment: 'jsdom',
		include: ['src/**/*.{test,spec}.{js,ts}', 'src/**/*.svelte.{test,spec}.{js,ts}'],
		setupFiles: ['./src/test/setup.ts'],
		globals: false
	}
});
