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
  created_at: string;
  reviewed_by_username?: string;
  reviewed_at?: string;
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

export interface AdminAccount {
  id: string;
  username: string;
  role: AdminRole;
  created_at: string;
  created_by_username: string;
  is_active: boolean;
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
  address: string;
  status: string;
  latency_ms: number;
  error?: string;
}

export interface RedisHealth {
  status: string;
  latency_ms: number;
  error?: string;
  configured_nodes: string[];
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
