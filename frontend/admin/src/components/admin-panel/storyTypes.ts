import type {
  AdminAccount,
  AdminRole,
  Ban,
  InfraHealthResponse,
  Report,
} from '../../types';

interface BanModalStoryState {
  open: boolean;
  sessionId: string;
  ip: string;
  sourceReportId?: string;
  target: 'session' | 'ip';
  reason: string;
  mode: 'permanent' | 'temporary';
  durationValue: string;
  durationUnit: 'hours' | 'days';
  clearManualInputsOnSubmit?: boolean;
}

export interface AdminPanelMockState {
  isAuthenticated?: boolean;
  authReady?: boolean;
  currentAdminUsername?: string;
  role?: AdminRole | null;
  reports?: Report[];
  bans?: Ban[];
  accounts?: AdminAccount[];
  infraHealth?: InfraHealthResponse | null;
  currentTab?: 'reports' | 'bans' | 'accounts' | 'infra';
  reportStatusFilter?: 'pending' | 'decided' | 'all';
  banFilter?: 'all' | 'active' | 'inactive';
  serverReportMetrics?: { pending: number; approved: number; rejected: number };
  serverBanMetrics?: { active: number; inactive: number; total: number };
  expandedReport?: string | null;
  viewingDescription?: string | null;
  isRedisModalOpen?: boolean;
  isUserMenuOpen?: boolean;
  isMobileMenuOpen?: boolean;
  showCreateAccountPassword?: boolean;
  banModal?: BanModalStoryState;
}

export interface AdminPanelStoryHarnessProps {
  loginRoute?: string;
  mockState?: AdminPanelMockState;
}
