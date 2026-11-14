import { m } from '$lib/paraglide/messages';
import type { components } from '$lib/api/schema';
import { apiErrorMessage, client } from '$lib/api/client';
import { streamEvents, type SseFrame } from './sse';

type Conversation = components['schemas']['AssistantConversation'];
type Message = components['schemas']['AssistantMessage'];

class AssistantStore {
	enabled = $state(false);
	dataSharing = $state<'metadata_only' | 'metadata_and_samples'>('metadata_only');
	conversations = $state<Conversation[]>([]);
	active = $state<Conversation | null>(null);
	messages = $state<Message[]>([]);
	streamingText = $state('');
	state = $state<'idle' | 'running' | 'interrupted'>('idle');
	loading = $state(false);
	error = $state('');
	loadError = $state('');

	private abort: AbortController | null = null;
	private workspaceId = '';
	private loadRun = 0;
	private openRun = 0;

	async loadStatus() {
		try {
			const { data } = await client.GET('/assistant/status');
			if (data) {
				this.enabled = data.enabled;
				this.dataSharing = data.data_sharing;
			}
		} catch {
			this.enabled = false;
		}
	}

	async load(workspaceId: string) {
		this.workspaceId = workspaceId;
		const run = ++this.loadRun;
		if (!this.enabled || !workspaceId) return;
		this.loading = true;
		this.loadError = '';
		try {
			const { data, error } = await client.GET('/assistant/conversations', {
				params: { query: { workspace_id: workspaceId } }
			});
			if (run !== this.loadRun) return;
			if (error || !data) {
				this.loadError = apiErrorMessage(
					error,
					m.assistant_storesvelte_could_not_load_conversations()
				);
				return;
			}
			this.conversations = data;
			if (this.active && !data.some((c) => c.id === this.active?.id)) {
				this.active = null;
				this.messages = [];
			}
			if (!this.active && data.length > 0 && run === this.loadRun) await this.open(data[0].id);
		} catch (error) {
			if (run === this.loadRun) {
				this.loadError = apiErrorMessage(
					error,
					m.assistant_storesvelte_could_not_load_conversations()
				);
			}
		} finally {
			if (run === this.loadRun) this.loading = false;
		}
	}

	async open(id: string) {
		const run = ++this.openRun;
		const { data, error } = await client.GET('/assistant/conversations/{id}', {
			params: { path: { id } }
		});
		if (run !== this.openRun) return;
		if (error || !data) {
			this.error = apiErrorMessage(
				error,
				m.assistant_storesvelte_could_not_open_the_conversation()
			);
			return;
		}
		this.stop();
		this.active = {
			id: data.id,
			workspace_id: data.workspace_id,
			title: data.title,
			state: data.state as Conversation['state'],
			last_seq: data.last_seq,
			created_at: data.created_at,
			updated_at: data.updated_at
		};
		this.messages = data.messages;
		this.state = data.state as Conversation['state'];
		this.streamingText = '';
		this.connect();
	}

	async create() {
		if (!this.workspaceId) return;
		const { data, error } = await client.POST('/assistant/conversations', {
			body: { workspace_id: this.workspaceId }
		});
		if (error || !data) {
			this.error = apiErrorMessage(error, m.assistant_storesvelte_could_not_start_a_conversation());
			return;
		}
		this.conversations = [data, ...this.conversations];
		await this.open(data.id);
	}

	async send(text: string): Promise<boolean> {
		if (!this.active) await this.create();
		if (!this.active) return false;
		this.state = 'running';
		this.streamingText = '';
		this.error = '';
		const { error } = await client.POST('/assistant/conversations/{id}/messages', {
			params: { path: { id: this.active.id } },
			body: { text }
		});
		if (error) {
			this.state = 'idle';
			this.error = apiErrorMessage(error, m.assistant_storesvelte_could_not_send_the_message());
			return false;
		}
		return true;
	}

	async cancel() {
		if (!this.active) return;
		this.state = 'idle';
		this.streamingText = '';
		const { error } = await client.POST('/assistant/conversations/{id}/cancel', {
			params: { path: { id: this.active.id } }
		});
		if (error)
			this.error = apiErrorMessage(
				error,
				m.assistant_storesvelte_the_assistant_stream_disconnected()
			);
	}

	async remove(id: string) {
		if (this.active?.id === id) this.stop();
		const { error } = await client.DELETE('/assistant/conversations/{id}', {
			params: { path: { id } }
		});
		if (error) {
			this.error = apiErrorMessage(error, m.assistant_storesvelte_could_not_load_conversations());
			return;
		}
		this.conversations = this.conversations.filter((c) => c.id !== id);
		if (this.active?.id === id) {
			this.active = null;
			this.messages = [];
			this.streamingText = '';
			this.state = 'idle';
		}
	}

	stop() {
		this.abort?.abort();
		this.abort = null;
	}

	private connect() {
		if (!this.active) return;
		this.stop();
		const controller = new AbortController();
		this.abort = controller;
		const last = this.messages.reduce((max, msg) => Math.max(max, msg.seq), 0);
		streamEvents(`/assistant/conversations/${this.active.id}/events?after_seq=${last}`, {
			signal: controller.signal,
			onEvent: (frame) => this.onFrame(frame)
		}).catch(() => {
			if (!controller.signal.aborted) {
				this.state = 'idle';
				this.streamingText = '';
				this.error = m.assistant_storesvelte_the_assistant_stream_disconnected();
			}
		});
	}

	private onFrame(frame: SseFrame) {
		if (frame.event === 'delta') {
			try {
				const payload = JSON.parse(frame.data) as { text?: string };
				this.streamingText += payload.text ?? '';
			} catch {
				// A malformed delta is not worth failing the turn.
			}
			return;
		}
		if (frame.event !== 'message') return;
		try {
			const message = JSON.parse(frame.data) as Message;
			if (this.messages.some((msg) => msg.seq === message.seq)) return;
			this.messages = [...this.messages, message];
			if (message.role === 'assistant' || message.role === 'notice') {
				this.streamingText = '';
				this.state = message.role === 'notice' ? 'interrupted' : 'idle';
			}
		} catch {
			// Ignore a frame the server did not finish writing.
		}
	}
}

export const assistantStore = new AssistantStore();
