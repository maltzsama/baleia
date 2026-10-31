/// <reference types="vite/client" />

interface ImportMetaEnv {
	readonly VITE_API_BASE?: string;
}

interface ImportMeta {
	readonly env: ImportMetaEnv;
}

// See https://svelte.dev/docs/kit/types#app
declare global {
	namespace App {}
	// The released version, injected at build time from
	// .release-please-manifest.json (#513).
	const __APP_VERSION__: string;
}

export {};
