import type {
  AdminAccount,
  AutoModerationSettings,
  AdminRole,
  Ban,
  BannedWord,
  BotDefinition,
  BotSettings,
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
  bannedWords?: BannedWord[];
  botSettings?: BotSettings | null;
  botDefinitions?: BotDefinition[];
  accounts?: AdminAccount[];
  infraHealth?: InfraHealthResponse | null;
  autoModerationSettings?: AutoModerationSettings | null;
  currentTab?: 'reports' | 'bans' | 'bannedWords' | 'bots' | 'accounts' | 'infra';
  reportStatusFilter?: 'pending' | 'decided' | 'all';
  reportReviewSourceFilter?: 'all' | 'awaitingHuman' | 'autoReviewed' | 'humanReviewed';
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
