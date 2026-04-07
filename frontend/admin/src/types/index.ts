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
