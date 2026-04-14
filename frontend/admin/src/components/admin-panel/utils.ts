import type { AdminRole, RedisNodeHealth, Report } from '../../types';

export function buildAdminHeaders(includeJSON = false) {
  const headers: Record<string, string> = {};
  const csrfToken = window.sessionStorage.getItem('admin_csrf') || '';
  if (includeJSON) headers['Content-Type'] = 'application/json';
  if (csrfToken) headers['X-CSRF-Token'] = csrfToken;
  return headers;
}

export function persistAdminSession(username: string | undefined, role: AdminRole, csrfToken?: string) {
  localStorage.setItem('admin_auth', 'true');
  localStorage.setItem('admin_role', role);
  if (username) localStorage.setItem('admin_username', username);
  if (csrfToken) sessionStorage.setItem('admin_csrf', csrfToken);
}

export function clearAdminSession() {
  localStorage.removeItem('admin_auth');
  localStorage.removeItem('admin_role');
  localStorage.removeItem('admin_username');
  sessionStorage.removeItem('admin_csrf');
}

export function formatDate(value?: string | null) {
  if (!value) return 'N/A';
  return new Date(value).toLocaleString('en-US', {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

export function reportStatusClass(status: Report['status']) {
  if (status === 'approved') return 'bg-emerald-500/10 text-emerald-400 border border-emerald-500/20';
  if (status === 'rejected') return 'bg-rose-500/10 text-rose-400 border border-rose-500/20';
  return 'bg-amber-500/10 text-amber-400 border border-amber-500/20';
}

export function autoModerationStateClass(state?: string) {
  if (state === 'completed') return 'border border-emerald-500/20 bg-emerald-500/10 text-emerald-400';
  if (state === 'processing') return 'border border-electric-cyan/20 bg-electric-cyan/10 text-electric-cyan';
  if (state === 'failed') return 'border border-rose-500/20 bg-rose-500/10 text-rose-400';
  return 'border border-amber-500/20 bg-amber-500/10 text-amber-400';
}

export function autoModerationDecisionClass(decision?: string) {
  if (decision === 'approved') return 'border border-emerald-500/20 bg-emerald-500/10 text-emerald-400';
  if (decision === 'rejected') return 'border border-rose-500/20 bg-rose-500/10 text-rose-400';
  if (decision === 'escalate') return 'border border-amber-500/20 bg-amber-500/10 text-amber-400';
  return 'border border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] text-[var(--admin-text-soft)]';
}

export function humanizeAutoModerationState(state?: string) {
  if (!state) return 'Pending';
  if (state === 'processing') return 'Processing';
  if (state === 'completed') return 'Completed';
  if (state === 'failed') return 'Failed';
  return state.replace(/[_-]+/g, ' ');
}

export function humanizeAutoModerationDecision(decision?: string) {
  if (!decision) return 'No Decision';
  if (decision === 'approved') return 'Auto Approved';
  if (decision === 'rejected') return 'Auto Rejected';
  if (decision === 'escalate') return 'Escalated';
  return decision.replace(/[_-]+/g, ' ');
}

export function healthStatusClass(status?: string) {
  if (status === 'ok') return 'border border-emerald-500/20 bg-emerald-500/10 text-emerald-400';
  if (status === 'degraded') return 'border border-amber-500/20 bg-amber-500/10 text-amber-400';
  return 'border border-rose-500/20 bg-rose-500/10 text-rose-400';
}

export function formatLatency(ms?: number) {
  if (!Number.isFinite(ms) || !ms || ms < 0) return '0ms';
  return `${ms}ms`;
}

export function formatBytes(bytes?: number) {
  if (!Number.isFinite(bytes) || bytes === undefined || bytes < 0) return '0 B';
  if (bytes === 0) return '0 B';

  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let value = bytes;
  let unitIndex = 0;

  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }

  return `${value >= 10 || unitIndex === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unitIndex]}`;
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  return value as Record<string, unknown>;
}

function asNumber(value: unknown): number | null {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  if (typeof value === 'string') {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return null;
}

export function goMemorySummary(details?: Record<string, unknown>) {
  const memory = asRecord(details?.memory);
  if (!memory) return null;

  return {
    primary: formatBytes(asNumber(memory.heap_alloc_bytes) ?? undefined),
    secondary: formatBytes(asNumber(memory.sys_bytes) ?? undefined),
    goroutines: asNumber(memory.goroutines) ?? 0,
  };
}

export function phoenixMemorySummary(details?: Record<string, unknown>) {
  const memory = asRecord(details?.memory);
  if (!memory) return null;

  return {
    total: formatBytes(asNumber(memory.total_bytes) ?? undefined),
    binary: formatBytes(asNumber(memory.binary_bytes) ?? undefined),
    processes: asNumber(memory.process_count) ?? 0,
  };
}

export function topRedisCommandStats(node: RedisNodeHealth) {
  return (node.command_stats || []).slice(0, 8);
}

export function buildExpiryDate(durationValue: string, durationUnit: 'hours' | 'days') {
  const amount = Number(durationValue);
  if (!Number.isFinite(amount) || amount <= 0) return null;
  const expiresAt = new Date();
  if (durationUnit === 'hours') expiresAt.setHours(expiresAt.getHours() + amount);
  else expiresAt.setDate(expiresAt.getDate() + amount);
  return expiresAt.toISOString();
}
