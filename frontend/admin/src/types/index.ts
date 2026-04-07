export type AdminRole = 'moderator' | 'admin' | 'root';

export interface Message {
  type: 'offer' | 'answer' | 'ice' | 'message' | 'match' | 'disconnect' | 'disconnected' | 'error' | 'banned' | 'timeout' | 'stopped' | 'typing' | 'pong' | 'connected' | 'searching' | 'skipped' | 'webrtc_ready' | 'webrtc_start';
  data?: any;
  peer_id?: string;
  session_id?: string;
  session_token?: string;
}


export interface User {
  id: string;
  ip: string;
}

export interface ReportData {
  reason: string;
  description: string;
}

export interface BanCheck {
  is_banned: boolean;
  reason?: string;
  expires_at?: string;
}

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

export interface AdminAccount {
  id: string;
  username: string;
  role: AdminRole;
  created_at: string;
  created_by_username: string;
  is_active: boolean;
}
