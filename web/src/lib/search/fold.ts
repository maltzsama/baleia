// Case and accent folding, as the server's search does it (#943), for the
// places that compare text typed by a person with names they could not spell
// exactly: "regiao" finds "Região".

/** Lower case, without diacritics. */
export function foldText(value: string): string {
	return value.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase();
}

/** Whether `text` starts with `prefix`, ignoring case and accents. */
export function startsWithFolded(text: string, prefix: string): boolean {
	return foldText(text).startsWith(foldText(prefix));
}
