import { m } from '$lib/paraglide/messages';
import type { components } from '$lib/api/schema';
import { apiErrorMessage, client } from '$lib/api/client';
import { describeError } from '$lib/errors/catalog';
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
	private streamConnected = false;
	private streamReady: Promise<boolean> | null = null;
	private resolveStreamReady: ((connected: boolean) => void) | null = null;

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
		const workspaceChanged = this.workspaceId !== workspaceId;
		this.workspaceId = workspaceId;
		const run = ++this.loadRun;
		if (workspaceChanged) {
			this.openRun += 1;
			this.stop();
			this.active = null;
			this.messages = [];
			this.streamingText = '';
			this.state = 'idle';
			this.error = '';
		}
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
		const workspaceId = this.workspaceId;
		const run = ++this.openRun;
		const { data, error } = await client.GET('/assistant/conversations/{id}', {
			params: { path: { id } }
		});
		if (run !== this.openRun || workspaceId !== this.workspaceId) return;
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
		this.state = 'running';
		this.streamingText = '';
		this.error = '';
		let conversationId: string | null = null;
		let openRun = this.openRun;
		try {
			if (!this.active) await this.create();
			conversationId = this.active?.id ?? null;
			openRun = this.openRun;
			if (!conversationId || !(await this.ensureConnected())) return false;
			if (this.active?.id !== conversationId || this.openRun !== openRun) return false;
			const { error } = await client.POST('/assistant/conversations/{id}/messages', {
				params: { path: { id: conversationId } },
				body: { text }
			});
			if (error) {
				if (this.active?.id === conversationId && this.openRun === openRun) {
					this.state = 'idle';
					this.error = apiErrorMessage(error, m.assistant_storesvelte_could_not_send_the_message());
				}
				return false;
			}
			return true;
		} catch (error) {
			if (this.active?.id === conversationId && this.openRun === openRun) {
				this.state = 'idle';
				this.streamingText = '';
				this.error = apiErrorMessage(error, m.assistant_storesvelte_could_not_send_the_message());
			}
			return false;
		}
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
		const removingActive = this.active?.id === id;
		const { error } = await client.DELETE('/assistant/conversations/{id}', {
			params: { path: { id } }
		});
		if (error) {
			this.error = apiErrorMessage(error, m.assistant_storesvelte_could_not_load_conversations());
			return;
		}
		if (removingActive && this.active?.id !== id) return;
		if (removingActive) this.stop();
		this.conversations = this.conversations.filter((c) => c.id !== id);
		if (removingActive) {
			this.active = null;
			this.messages = [];
			this.streamingText = '';
			this.state = 'idle';
		}
	}

	stop() {
		this.abort?.abort();
		this.abort = null;
		this.streamConnected = false;
		this.resolveStreamReady?.(false);
		this.resolveStreamReady = null;
		this.streamReady = null;
	}

	private connect(): Promise<boolean> | null {
		if (!this.active) return null;
		this.stop();
		const controller = new AbortController();
		this.abort = controller;
		const ready = new Promise<boolean>((resolve) => {
			this.resolveStreamReady = resolve;
		});
		this.streamReady = ready;
		const last = this.messages.reduce((max, msg) => Math.max(max, msg.seq), 0);
		const stream = streamEvents(
			`/assistant/conversations/${this.active.id}/events?after_seq=${last}`,
			{
				signal: controller.signal,
				onOpen: () => {
					if (this.abort !== controller) return;
					this.streamConnected = true;
					this.resolveStreamReady?.(true);
				},
				onEvent: (frame) => this.onFrame(frame)
			}
		);
		void stream.then(
			() => {
				if (this.abort !== controller) return;
				this.abort = null;
				this.streamConnected = false;
				this.resolveStreamReady?.(false);
				this.resolveStreamReady = null;
				this.streamReady = null;
				this.state = 'idle';
				this.streamingText = '';
				this.error = m.assistant_storesvelte_the_assistant_stream_disconnected();
			},
			(error) => {
				if (this.abort !== controller) return;
				this.abort = null;
				this.streamConnected = false;
				this.resolveStreamReady?.(false);
				this.resolveStreamReady = null;
				this.streamReady = null;
				if (!controller.signal.aborted) {
					this.state = 'idle';
					this.streamingText = '';
					this.error = describeError(error).title;
				}
			}
		);
		return ready;
	}

	private async ensureConnected(): Promise<boolean> {
		if (this.streamConnected) return true;
		const ready = this.streamReady ?? this.connect();
		if (!ready) return false;
		const connected = await ready;
		if (connected) this.error = '';
		return connected;
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
