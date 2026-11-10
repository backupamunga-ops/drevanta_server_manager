// Typed client for the frozen Sprint 1 API surface.

export interface CpuMetrics {
	cores: number;
	usage_percent: number;
	load_1m: number;
	load_5m: number;
	load_15m: number;
}

export interface MemoryMetrics {
	total_bytes: number;
	available_bytes: number;
	used_bytes: number;
	used_percent: number;
	buffers_bytes: number;
	cached_bytes: number;
	swap_total_bytes: number;
	swap_used_bytes: number;
}

export interface DiskMetrics {
	path: string;
	total_bytes: number;
	used_bytes: number;
	available_bytes: number;
	used_percent: number;
}

export interface MetricsSnapshot {
	timestamp: string;
	cpu: CpuMetrics;
	memory: MemoryMetrics;
	disk: DiskMetrics[];
	uptime_seconds: number;
}

async function getJSON<T>(url: string): Promise<T> {
	const res = await fetch(url, { headers: { accept: 'application/json' } });
	if (!res.ok) {
		throw new Error(`${url} -> HTTP ${res.status}`);
	}
	return (await res.json()) as T;
}

export function fetchMetrics(): Promise<MetricsSnapshot> {
	return getJSON<MetricsSnapshot>('/api/v1/server/metrics');
}

export async function isUp(url: string): Promise<boolean> {
	try {
		const res = await fetch(url);
		return res.ok;
	} catch {
		return false;
	}
}
