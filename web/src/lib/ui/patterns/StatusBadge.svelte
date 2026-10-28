<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import Badge from '../Badge.svelte';

	type Tone = 'neutral' | 'info' | 'success' | 'warning' | 'danger';
	type Kind =
		| 'binding'
		| 'execution'
		| 'statement'
		| 'sync'
		| 'metadataSync'
		| 'reconcile'
		| 'retriever'
		| 'maintenance'
		| 'agent'
		| 'engine';
	type Look = { tone: Tone; label: string; spinning?: boolean };

	let { status, kind, title }: { status: string; kind: Kind; title?: string } = $props();

	const mappings: Record<Kind, Record<string, Look>> = {
		binding: {
			active: {
				tone: 'success',
				get label() {
					return m.statusbadge_active();
				}
			},
			provisioning: {
				tone: 'info',
				get label() {
					return m.statusbadge_provisioning();
				},
				spinning: true
			},
			updating: {
				tone: 'info',
				get label() {
					return m.statusbadge_updating();
				},
				spinning: true
			},
			deprovisioning: {
				tone: 'warning',
				get label() {
					return m.statusbadge_deprovisioning();
				},
				spinning: true
			},
			error: {
				tone: 'danger',
				get label() {
					return m.statusbadge_error();
				}
			},
			pending: {
				tone: 'neutral',
				get label() {
					return m.statusbadge_pending();
				}
			},
			disabled: {
				tone: 'neutral',
				get label() {
					return m.statusbadge_disabled();
				}
			}
		},
		execution: {
			pending: {
				tone: 'neutral',
				get label() {
					return m.statusbadge_pending();
				},
				spinning: true
			},
			running: {
				tone: 'info',
				get label() {
					return m.statusbadge_running();
				},
				spinning: true
			},
			finished: {
				tone: 'success',
				get label() {
					return m.statusbadge_finished();
				}
			},
			failed: {
				tone: 'danger',
				get label() {
					return m.statusbadge_failed();
				}
			},
			cancelled: {
				tone: 'warning',
				get label() {
					return m.statusbadge_cancelled();
				}
			}
		},
		statement: {
			pending: {
				tone: 'neutral',
				get label() {
					return m.statusbadge_pending();
				},
				spinning: true
			},
			running: {
				tone: 'info',
				get label() {
					return m.statusbadge_running();
				},
				spinning: true
			},
			finished: {
				tone: 'success',
				get label() {
					return m.statusbadge_finished();
				}
			},
			failed: {
				tone: 'danger',
				get label() {
					return m.statusbadge_failed();
				}
			},
			cancelled: {
				tone: 'warning',
				get label() {
					return m.statusbadge_cancelled();
				}
			},
			skipped: {
				tone: 'neutral',
				get label() {
					return m.statusbadge_skipped();
				}
			}
		},
		sync: {
			ok: {
				tone: 'success',
				get label() {
					return m.statusbadge_synced();
				}
			},
			running: {
				tone: 'info',
				get label() {
					return m.statusbadge_syncing();
				},
				spinning: true
			},
			failed: {
				tone: 'danger',
				get label() {
					return m.statusbadge_failed();
				}
			},
			stale: {
				tone: 'warning',
				get label() {
					return m.statusbadge_stale();
				}
			}
		},
		// A catalog's metadata sync (#454): "never" has not run, it is not a success.
		metadataSync: {
			never: {
				tone: 'neutral',
				get label() {
					return m.statusbadge_never_synced();
				}
			},
			running: {
				tone: 'info',
				get label() {
					return m.statusbadge_syncing();
				},
				spinning: true
			},
			finished: {
				tone: 'success',
				get label() {
					return m.statusbadge_synced();
				}
			},
			failed: {
				tone: 'danger',
				get label() {
					return m.statusbadge_sync_failed();
				}
			}
		},
		reconcile: {
			ok: {
				tone: 'success',
				get label() {
					return m.statusbadge_in_sync();
				}
			},
			drift: {
				tone: 'warning',
				get label() {
					return m.statusbadge_drift();
				}
			},
			failed: {
				tone: 'danger',
				get label() {
					return m.statusbadge_failed();
				}
			},
			running: {
				tone: 'info',
				get label() {
					return m.statusbadge_reconciling();
				},
				spinning: true
			}
		},
		retriever: {
			active: {
				tone: 'success',
				get label() {
					return m.statusbadge_active();
				}
			},
			paused: {
				tone: 'neutral',
				get label() {
					return m.statusbadge_paused();
				}
			},
			error: {
				tone: 'danger',
				get label() {
					return m.statusbadge_error();
				}
			}
		},
		maintenance: {
			running: {
				tone: 'info',
				get label() {
					return m.statusbadge_running();
				},
				spinning: true
			},
			finished: {
				tone: 'success',
				get label() {
					return m.statusbadge_finished();
				}
			},
			failed: {
				tone: 'danger',
				get label() {
					return m.statusbadge_failed();
				}
			},
			scheduled: {
				tone: 'neutral',
				get label() {
					return m.statusbadge_scheduled();
				}
			}
		},
		agent: {
			active: {
				tone: 'success',
				get label() {
					return m.statusbadge_active();
				}
			},
			expired: {
				tone: 'warning',
				get label() {
					return m.statusbadge_expired();
				}
			},
			revoked: {
				tone: 'danger',
				get label() {
					return m.statusbadge_revoked();
				}
			},
			pending: {
				tone: 'neutral',
				get label() {
					return m.statusbadge_pending();
				}
			}
		},
		engine: {
			ok: {
				tone: 'success',
				get label() {
					return m.statusbadge_healthy();
				}
			},
			unreachable: {
				tone: 'danger',
				get label() {
					return m.statusbadge_unreachable();
				}
			},
			unknown: {
				tone: 'neutral',
				get label() {
					return m.statusbadge_unknown();
				}
			},
			disabled: {
				tone: 'neutral',
				get label() {
					return m.statusbadge_disabled();
				}
			}
		}
	};

	const look: Look = $derived(mappings[kind]?.[status] ?? { tone: 'neutral', label: status });
</script>

<span title={title ?? look.label}>
	<Badge tone={look.tone}>
		{#if look.spinning}
			<span
				class="h-2.5 w-2.5 animate-spin rounded-full border border-current border-t-transparent"
				aria-hidden="true"
			></span>
		{/if}
		{look.label}
	</Badge>
</span>
