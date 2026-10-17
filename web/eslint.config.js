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
		files: ['**/*.d.ts'],
		rules: {
			'@typescript-eslint/no-unused-vars': 'off'
		}
	},
	{
		files: ['src/**/*.{ts,svelte}'],
		rules: {
			// App state travels through typed stores (#29); window custom events
			// are not allowed. Plain DOM events (resize, keydown, …) stay allowed.
			'no-restricted-syntax': [
				'error',
				{
					selector: "NewExpression[callee.name='CustomEvent']",
					message: 'Use the typed stores instead of CustomEvent (#29).'
				},
				{
					selector:
						"CallExpression[callee.object.name='window'][callee.property.name='dispatchEvent']",
					message: 'Use the typed stores instead of window.dispatchEvent (#29).'
				},
				{
					selector:
						"CallExpression[callee.object.name='window'][callee.property.name='addEventListener'][arguments.0.value=/^[a-z]+-/]",
					message: 'Use the typed stores for app events; this is not a DOM event (#29).'
				}
			]
		}
	},
	{
		rules: {
			'@typescript-eslint/no-explicit-any': 'error',
			'no-empty': ['error', { allowEmptyCatch: false }]
		}
	}
);
