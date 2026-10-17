// Store-driven toast queue (#28). Components call `toasts.success(...)` and a
// single <Toaster /> mounted in the app shell renders the queue.

export type ToastTone = 'info' | 'success' | 'error';
export type ToastItem = { id: number; message: string; tone: ToastTone };

function createToastStore() {
	let items = $state<ToastItem[]>([]);
	let nextId = 1;

	function dismiss(id: number) {
		items = items.filter((item) => item.id !== id);
	}

	function show(message: string, tone: ToastTone = 'info', timeout = 5000): number {
		const id = nextId++;
		items = [...items, { id, message, tone }];
		if (timeout > 0) setTimeout(() => dismiss(id), timeout);
		return id;
	}

	return {
		get items() {
			return items;
		},
		show,
		success: (message: string) => show(message, 'success'),
		error: (message: string) => show(message, 'error'),
		dismiss
	};
}

export const toasts = createToastStore();
