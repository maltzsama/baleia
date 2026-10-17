import js from '@eslint/js';
import svelte from 'eslint-plugin-svelte';
import globals from 'globals';
import ts from 'typescript-eslint';

export default ts.config(
	{
		ignores: [
			'build/',
			'.svelte-kit/',
			'node_modules/',
			'src/lib/api/schema.d.ts',
			'playwright-report/',
			'test-results/',
			'src/**/*.svelte.ts.timestamp-*'
		]
	},
	js.configs.recommended,
	...ts.configs.recommended,
	...svelte.configs['flat/recommended'],
	{
		languageOptions: {
			globals: {
				...globals.browser,
				...globals.node
			}
		}
	},
	{
		files: ['**/*.svelte', '**/*.svelte.ts', '**/*.svelte.js'],
		languageOptions: {
			parserOptions: {
				parser: ts.parser
			}
		}
	},
	{
		files: ['**/*.{ts,svelte}'],
		rules: {
			// TypeScript already resolves globals; the core rule only produces false
			// positives on DOM lib types such as EventListener.
			'no-undef': 'off'
		}
	},
	{
		rules: {
			'@typescript-eslint/no-explicit-any': 'error',
			'no-empty': ['error', { allowEmptyCatch: false }]
		}
	}
);
