import { en, ptBR, type MessageKey } from './messages';

export type Locale = 'en' | 'pt-BR';

const catalogs: Record<Locale, Record<MessageKey, string>> = { en, 'pt-BR': ptBR };
const STORAGE_KEY = 'baleia_locale';

let locale = $state<Locale>('en');

/** currentLocale reads the active locale. */
export function currentLocale(): Locale {
	return locale;
}

/** t resolves a key and interpolates named `{param}` placeholders. */
export function t(key: MessageKey, params?: Record<string, string | number>): string {
	let text = catalogs[locale][key] ?? en[key] ?? key;
	for (const [name, value] of Object.entries(params ?? {})) {
		text = text.replaceAll(`{${name}}`, String(value));
	}
	return text;
}

/** setLocale switches the UI language and updates <html lang>. */
export function setLocale(next: Locale): void {
	locale = next;
	if (typeof document !== 'undefined') document.documentElement.lang = next;
	try {
		localStorage.setItem(STORAGE_KEY, next);
	} catch {
		// Private mode: the preference stays in memory.
	}
}

/** resolveLocale applies preference → stored choice → browser → en. */
export function resolveLocale(preferred?: string | null): Locale {
	if (preferred === 'en' || preferred === 'pt-BR') return preferred;
	try {
		const stored = localStorage.getItem(STORAGE_KEY);
		if (stored === 'en' || stored === 'pt-BR') return stored;
	} catch {
		// ignore
	}
	if (typeof navigator !== 'undefined') {
		for (const candidate of navigator.languages ?? [navigator.language]) {
			if (candidate.toLowerCase().startsWith('pt')) return 'pt-BR';
			if (candidate.toLowerCase().startsWith('en')) return 'en';
		}
	}
	return 'en';
}

/** initLocale resolves and applies the locale once the user is known. */
export function initLocale(preferred?: string | null): void {
	setLocale(resolveLocale(preferred));
}
