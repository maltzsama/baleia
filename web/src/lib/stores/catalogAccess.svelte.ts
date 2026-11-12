// The catalogs a workspace can see change when a catalog is granted or
// revoked, here or in another tab. Views that load them (the datasource tree,
// the editor's targets) refetch when `version` moves; a grant or revoke calls
// changed(), and the app layout calls it when the window regains focus.

// Focus events come in bursts (a window switch fires focus and visibility
// together); one refetch per window is enough.
const FOCUS_THROTTLE_MS = 15_000;

function createCatalogAccessStore() {
	let version = $state(0);
	let lastFocusAt = 0;

	return {
		get version() {
			return version;
		},
		/** A grant or revoke just succeeded: reload what depends on it. */
		changed() {
			version += 1;
		},
		/** The window regained focus: pick up a grant made elsewhere. */
		focused(now: number = Date.now()) {
			if (now - lastFocusAt < FOCUS_THROTTLE_MS) return;
			lastFocusAt = now;
			version += 1;
		}
	};
}

export const catalogAccessStore = createCatalogAccessStore();
