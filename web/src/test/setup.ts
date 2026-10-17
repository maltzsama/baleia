import { afterAll, afterEach, beforeAll } from 'vitest';
import { server } from './server';

// jsdom has no ResizeObserver; Svelte's bind:clientWidth needs one. The stub is
// inert, so bound sizes stay at 0 — enough for the virtualized grid's tests.
if (typeof globalThis.ResizeObserver === 'undefined') {
	globalThis.ResizeObserver = class {
		observe() {}
		unobserve() {}
		disconnect() {}
	} as unknown as typeof ResizeObserver;
}

// jsdom ships <dialog> semantics but not the modal methods. The Modal primitive
// relies on showModal()/close(); mirror the open attribute and fire `close`.
if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
	HTMLDialogElement.prototype.showModal = function (this: HTMLDialogElement) {
		this.setAttribute('open', '');
	};
	HTMLDialogElement.prototype.close = function (this: HTMLDialogElement) {
		this.removeAttribute('open');
		this.dispatchEvent(new Event('close'));
	};
}

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());
