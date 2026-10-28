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

	private abort: AbortController | null = null;
	private workspaceId = '';

	async loadStatus() {
		const { data } = await client.GET('/assistant/status');
		if (data) {
			this.enabled = data.enabled;
			this.dataSharing = data.data_sharing;
		}
	}

	async load(workspaceId: string) {
		this.workspaceId = workspaceId;
		if (!this.enabled || !workspaceId) return;
		this.loading = true;
		this.error = '';
		try {
			const { data, error } = await client.GET('/assistant/conversations', {
				params: { query: { workspace_id: workspaceId } }
			});
			if (error || !data) {
				this.error = apiErrorMessage(error, 'Could not load conversations.');
				return;
			}
			this.conversations = data;
			if (this.active && !data.some((c) => c.id === this.active?.id)) {
				this.active = null;
				this.messages = [];
			}
			if (!this.active && data.length > 0) await this.open(data[0].id);
		} finally {
			this.loading = false;
		}
	}

	async open(id: string) {
		const { data, error } = await client.GET('/assistant/conversations/{id}', {
			params: { path: { id } }
		});
		if (error || !data) {
			this.error = apiErrorMessage(error, 'Could not open the conversation.');
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
			this.error = apiErrorMessage(error, 'Could not start a conversation.');
			return;
		}
		this.conversations = [data, ...this.conversations];
		await this.open(data.id);
	}

	async send(text: string) {
		if (!this.active) await this.create();
		if (!this.active) return;
		this.state = 'running';
		this.streamingText = '';
		const { error } = await client.POST('/assistant/conversations/{id}/messages', {
			params: { path: { id: this.active.id } },
			body: { text }
		});
		if (error) {
			this.state = 'idle';
			this.error = apiErrorMessage(error, 'Could not send the message.');
		}
	}

	async cancel() {
		if (!this.active) return;
		await client.POST('/assistant/conversations/{id}/cancel', {
			params: { path: { id: this.active.id } }
		});
	}

	async remove(id: string) {
		await client.DELETE('/assistant/conversations/{id}', { params: { path: { id } } });
		this.conversations = this.conversations.filter((c) => c.id !== id);
		if (this.active?.id === id) {
			this.stop();
			this.active = null;
			this.messages = [];
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
			if (!controller.signal.aborted)
				this.error = m.assistant_storesvelte_the_assistant_stream_disconnected();
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
			if (this.messages.some((m) => m.seq === message.seq)) return;
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
