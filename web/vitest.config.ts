import { sveltekit } from '@sveltejs/kit/vite';
import { svelteTesting } from '@testing-library/svelte/vite';
import { defineConfig } from 'vitest/config';

export default defineConfig({
	plugins: [sveltekit(), svelteTesting()],
	test: {
		environment: 'jsdom',
		include: ['src/**/*.{test,spec}.{js,ts}', 'src/**/*.svelte.{test,spec}.{js,ts}'],
		setupFiles: ['./vitest-setup.ts'],
		globals: false
	}
});
