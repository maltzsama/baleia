import { render } from '@testing-library/svelte';
import type { Component } from 'svelte';
import { authStore, type AuthUser } from '$lib/stores/auth.svelte';

// Mounts a route component with the URL and auth store initialized (#106).
export function renderPage(
	component: Component<Record<string, never>>,
	options: { me?: AuthUser | null; url?: string } = {}
) {
	if (options.me !== undefined) authStore.set(options.me);
	if (options.url && typeof window !== 'undefined') {
		window.history.replaceState(null, '', options.url);
	}
	return render(component as Component<Record<string, never>>);
}
