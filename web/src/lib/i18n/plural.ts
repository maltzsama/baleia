// Plural choice by the active locale's rules (#469): pt-BR treats 0 and 1 as
// "one", en only 1. Each count gets a `_one` and an `_other` message.
import { getLocale } from '$lib/paraglide/runtime';

type CountMessage = (inputs: { count: number | string }) => string;

export function plural(count: number, one: CountMessage, other: CountMessage): string {
	const shown = count.toLocaleString(getLocale());
	return new Intl.PluralRules(getLocale()).select(count) === 'one'
		? one({ count: shown })
		: other({ count: shown });
}
