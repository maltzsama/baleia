// Tracks whether the app has navigated at least once since the document
// loaded. `history.length` also counts forward entries, so it cannot tell
// whether "Go back" has a previous entry to reach (#1082). The root layout
// records each in-app navigation; a full reload resets the module.
let internalNavigations = 0;

export function recordNavigation(): void {
	internalNavigations += 1;
}

export function canGoBack(): boolean {
	return internalNavigations > 0;
}

/** Test helper: forget the in-app navigation count. */
export function resetNavigationHistory(): void {
	internalNavigations = 0;
}
