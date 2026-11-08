// Engine health for the Monitoring cluster cards (#820). The thresholds are
// the policy this card applies to the latest cluster snapshot; an engine
// setting that overrides them can come later without touching the callers.
import type { ClusterPoint } from './client';

export type EngineHealth = 'ok' | 'degraded' | 'critical';

export const HEALTH_THRESHOLDS = {
	/** Queued queries at or above this count degrade the engine. */
	queued: 10,
	/** Used memory at or above this share (percent) degrades the engine. */
	memoryPercent: 75,
	/** Used memory at or above this share (percent) is critical. */
	memoryPercentCritical: 90
};

type HealthInput = Pick<
	ClusterPoint,
	'blocked_queries' | 'queued_queries' | 'total_memory_bytes' | 'free_memory_bytes'
>;

/** Used memory as a whole percentage, or null when the engine did not report it. */
export function memoryUsedPercent(point: HealthInput): number | null {
	if (!point.total_memory_bytes || point.free_memory_bytes == null) return null;
	return Math.round((1 - point.free_memory_bytes / point.total_memory_bytes) * 100);
}

/** A blocked query or near-full memory is critical; a queue or high memory degrades. */
export function engineHealth(point: HealthInput | null): EngineHealth {
	if (!point) return 'degraded';
	const memory = memoryUsedPercent(point);
	if (
		point.blocked_queries > 0 ||
		(memory !== null && memory >= HEALTH_THRESHOLDS.memoryPercentCritical)
	) {
		return 'critical';
	}
	if (
		point.queued_queries >= HEALTH_THRESHOLDS.queued ||
		(memory !== null && memory >= HEALTH_THRESHOLDS.memoryPercent)
	) {
		return 'degraded';
	}
	return 'ok';
}
