// Locale selection (#67). The order mirrors the Paraglide strategy configured
// in vite.config.ts:
//
//   1. the signed-in user's preference (set from /auth/me and PATCH /me)
//   2. the choice stored for anonymous pages (localStorage)
//   3. the browser's languages (navigator.languages)
//   4. en (the source locale)
//
// `globalVariable` is the first strategy so the in-memory value always wins.
import {
	getLocale as resolveLocale,
	overwriteGetLocale,
	setLocale as runtimeSetLocale,
	type Locale
} from '$lib/paraglide/runtime';

export const SUPPORTED_LOCALES = ['en', 'pt-BR'] as const;

let current = $state<Locale>(resolveLocale());

// Generated messages call getLocale(). Reading $state there makes every m.*
// call reactive, so a language switch re-renders without a reload.
overwriteGetLocale(() => current);

function applyDocumentLang(locale: Locale): void {
	if (typeof document !== 'undefined') document.documentElement.lang = locale;
}

export function setAppLocale(locale: Locale): void {
	current = locale;
	// reload:false is the documented escape hatch for a client-only SPA that
	// owns its reactive updates. The localStorage strategy keeps the choice.
	runtimeSetLocale(locale, { reload: false });
	applyDocumentLang(locale);
}

/** Applies the user's saved preference; null/undefined keeps the current one. */
export function applyUserLocale(locale: Locale | null | undefined): void {
	if (locale) setAppLocale(locale);
	else applyDocumentLang(current);
}

/** Keeps <html lang> in sync on first paint. */
export function initLocale(): void {
	applyDocumentLang(current);
}

export const localeStore = {
	get current(): Locale {
		return current;
	},
	set(locale: Locale): void {
		setAppLocale(locale);
	}
};
