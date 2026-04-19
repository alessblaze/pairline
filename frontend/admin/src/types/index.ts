// Pairline - Open Source Video Chat and Matchmaking
// Copyright (C) 2026 Albert Blasczykowski
// Aless Microsystems
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

export type AdminRole = 'moderator' | 'admin' | 'root';

export interface LoginResponse {
  username?: string;
  role: AdminRole;
  csrf_token?: string;
}

export interface ChatMessage {
  id: string;
  text: string;
  sender: 'me' | 'peer' | 'system';
  timestamp: number;
}

export interface Report {
  id: string;
  reporter_session_id: string;
  reported_session_id: string;
  reporter_ip: string;
  reported_ip: string;
  reason: string;
  description: string;
  chat_log: ChatMessage[];
  status: 'pending' | 'approved' | 'rejected';
  auto_moderation_state?: string;
  auto_moderation_decision?: string;
  auto_moderation_categories?: string[];
  auto_moderation_summary?: string;
  auto_moderation_error?: string;
  auto_moderation_model?: string;
  auto_moderation_attempts?: number;
  auto_moderation_claimed_at?: string | null;
  auto_moderation_completed_at?: string | null;
  created_at: string;
  reviewed_by_username?: string;
  reviewed_at?: string;
}

export interface AutoModerationSettings {
  enabled: boolean;
  enabled_default: boolean;
  configured: boolean;
  model: string;
  batch_size: number;
  interval_seconds: number;
  timeout_seconds: number;
  max_attempts: number;
}

export interface CreateBanRequest {
  session_id?: string;
  ip?: string;
  report_id?: string;
  reason: string;
  expiry_date?: string;
}

export interface Ban {
  id: string;
  session_id: string;
  ip_address: string;
  reason: string;
  banned_by_username: string;
  created_at: string;
  expires_at: string | null;
  is_active: boolean;
  unbanned_at: string | null;
  unbanned_by_username: string | null;
}

export interface BannedWord {
  id: string;
  word: string;
  normalized_word: string;
  created_by_username: string;
  created_at: string;
}

export interface BannedWordsSettings {
  enabled: boolean;
}

export interface AdminAccount {
  id: string;
  username: string;
  role: AdminRole;
  created_at: string;
  created_by_username: string;
  is_active: boolean;
}

export interface BotSettings {
  enabled: boolean;
  engagement_enabled: boolean;
  ai_enabled: boolean;
  rollout_percent: number;
  max_concurrent_runs: number;
  emergency_stop: boolean;
}

export interface ScriptTrigger {
  regex: string;
  reply: string;
}

export interface ScriptJSON {
  opening_messages?: string[];
  reply_messages?: string[];
  fallback_message?: string;
  closing_message?: string;
  triggers?: ScriptTrigger[];
}

export interface BotDefinition {
  id: string;
  name: string;
  slug: string;
  bot_type: 'engagement' | 'ai';
  is_active: boolean;
  description: string;
  match_modes_json: string[];
  bot_count: number;
  traffic_weight: number;
  targeting_json: Record<string, unknown>;
  script_json: ScriptJSON | Record<string, unknown>;
  ai_config_json: Record<string, unknown>;
  message_limit: number;
  session_ttl_seconds: number;
  idle_timeout_seconds: number;
  disconnect_reason: string;
  created_by_username: string;
  updated_by_username: string;
  created_at: string;
  updated_at: string;
}

export interface InfraSummary {
  healthy_services: number;
  degraded_services: number;
  total_services: number;
}

export interface InfraTopology {
  phoenix_configured_nodes: number;
  phoenix_connected_nodes: number;
  phoenix_node_names: string[];
  go_configured_services: number;
  redis_configured_nodes: number;
  redis_reachable_nodes: number;
}

export interface PostgresConnectionStats {
  open: number;
  in_use: number;
  idle: number;
  max_open: number;
}

export interface PostgresHealth {
  status: string;
  latency_ms: number;
  error?: string;
  connections: PostgresConnectionStats;
}

export interface RedisNodeHealth {
  node_id: string;
  address: string;
  role: string;
  status: string;
  link_state: string;
  flags: string[];
  master_id?: string;
  slots?: string[];
  master_link_status?: string;
  replication_lag_seconds?: number;
  memory: RedisMemoryInfo;
  command_stats?: RedisCommandStat[];
  error?: string;
}

export interface RedisClusterInfo {
  state: string;
  slots_assigned: number;
  slots_ok: number;
  slots_pfail: number;
  slots_fail: number;
  known_nodes: number;
  size: number;
  current_epoch: number;
  my_epoch: number;
  total_cluster_links_buffer_limit_exceeded: number;
}

export interface RedisMemoryInfo {
  used_memory_bytes: number;
  used_memory_human: string;
  used_memory_rss_bytes: number;
  used_memory_rss_human: string;
  used_memory_peak_bytes: number;
  used_memory_peak_human: string;
  used_memory_peak_perc: string;
  used_memory_dataset_bytes: number;
  used_memory_dataset_perc: string;
  total_system_memory_bytes: number;
  total_system_memory_human: string;
  maxmemory_bytes: number;
  maxmemory_human: string;
  maxmemory_policy: string;
  allocator: string;
  fragmentation_ratio: number;
  fragmentation_bytes: number;
}

export interface RedisCommandStat {
  command: string;
  calls: number;
  usec_total: number;
  usec_per_call: number;
}

export interface RedisHealth {
  status: string;
  latency_ms: number;
  error?: string;
  configured_nodes: string[];
  cluster: RedisClusterInfo;
  nodes: RedisNodeHealth[];
}

export interface CollectorHealth {
  url: string;
  status: string;
  latency_ms: number;
  error?: string;
}

export interface ObservabilityHealth {
  status: string;
  traces_configured: boolean;
  metrics_configured: boolean;
  otlp_endpoint: string;
  collector: CollectorHealth;
}

export interface RemoteServiceHealth {
  name: string;
  kind: string;
  url: string;
  status: string;
  http_status: number;
  latency_ms: number;
  error?: string;
  service?: string;
  reported_at?: number;
  details?: Record<string, unknown>;
}

export interface InfraHealthResponse {
  status: string;
  service: string;
  timestamp: number;
  topology: InfraTopology;
  postgres: PostgresHealth;
  redis: RedisHealth;
  observability: ObservabilityHealth;
  services: RemoteServiceHealth[];
  summary: InfraSummary;
}
