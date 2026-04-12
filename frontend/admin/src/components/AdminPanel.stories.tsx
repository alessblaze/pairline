import type { Meta, StoryObj } from '@storybook/react-vite';
import { AdminPanelStoryHarness } from './AdminPanelStoryHarness';
import type { AdminAccount, Ban, BannedWord, InfraHealthResponse, Report } from '../types';

const meta = {
  title: 'Pages/AdminPanel',
  component: AdminPanelStoryHarness,
  tags: ['autodocs'],
  parameters: {
    layout: 'fullscreen',
  },
} satisfies Meta<typeof AdminPanelStoryHarness>;

export default meta;
type Story = StoryObj<typeof meta>;

// Pure disconnected state (Login overlay view)
export const Unauthenticated: Story = {
  name: 'Login View',
};

export const Loading: Story = {
  name: 'Loading State',
  args: {
    mockState: {
      authReady: false,
    },
  },
};

// Mock data definitions for authenticated variations
const mockReports: Report[] = [
  {
    id: 'report-1',
    reporter_session_id: 'client-A',
    reported_session_id: 'client-B',
    reporter_ip: '192.168.1.1',
    reported_ip: '10.0.0.1',
    reason: 'abusive behavior',
    description: 'User immediately started swearing.',
    chat_log: [
      { id: '1', text: 'hi', sender: 'peer', timestamp: 123456789 },
      { id: '2', text: 'You are terrible.', sender: 'me', timestamp: 123456800 },
    ],
    status: 'pending' as const,
    created_at: new Date().toISOString(),
  },
];

const mockBans: Ban[] = [
  {
    id: 'ban-1',
    session_id: 'client-B',
    ip_address: '10.0.0.1',
    reason: 'Repeated offensive behavior identified via reports.',
    banned_by_username: 'albert',
    created_at: new Date().toISOString(),
    expires_at: null,
    is_active: true,
    unbanned_at: null,
    unbanned_by_username: null,
  },
];

const mockBannedWords: BannedWord[] = [
  {
    id: 'wb-1',
    word: 'offensiveword',
    normalized_word: 'offensiveword',
    created_by_username: 'albert',
    created_at: new Date().toISOString(),
  },
  {
    id: 'wb-2',
    word: 'b*a*d*w*o*r*d',
    normalized_word: 'badword',
    created_by_username: 'mod_jane',
    created_at: new Date().toISOString(),
  }
];

const mockAccounts: AdminAccount[] = [
  {
    id: 'acc-1',
    username: 'albert',
    role: 'root' as const,
    created_at: new Date().toISOString(),
    created_by_username: 'system',
    is_active: true,
  },
  {
    id: 'acc-2',
    username: 'mod_jane',
    role: 'moderator' as const,
    created_at: new Date().toISOString(),
    created_by_username: 'albert',
    is_active: true,
  },
];

const mockInfra: InfraHealthResponse = {
  status: 'ok',
  service: 'pairline_api',
  timestamp: Date.now(),
  topology: {
    phoenix_configured_nodes: 4,
    phoenix_connected_nodes: 4,
    phoenix_node_names: ['node1@127.0.0.1'],
    go_configured_services: 4,
    redis_configured_nodes: 6,
    redis_reachable_nodes: 6,
  },
  postgres: {
    status: 'ok',
    latency_ms: 1.2,
    connections: { open: 10, in_use: 2, idle: 8, max_open: 50 },
  },
  redis: {
    status: 'ok',
    latency_ms: 0.8,
    configured_nodes: ['127.0.0.1:7000'],
    cluster: {
      state: 'ok',
      slots_assigned: 16384,
      slots_ok: 16384,
      slots_pfail: 0,
      slots_fail: 0,
      known_nodes: 6,
      size: 3,
      current_epoch: 6,
      my_epoch: 1,
      total_cluster_links_buffer_limit_exceeded: 0,
    },
    nodes: [
      {
        node_id: 'db-node-master-1',
        address: '10.0.0.1:7000',
        role: 'master',
        status: 'ok',
        link_state: 'connected',
        flags: ['master'],
        memory: {
          used_memory_bytes: 536870912,
          used_memory_human: '512M',
          used_memory_rss_bytes: 600000000,
          used_memory_rss_human: '572M',
          used_memory_peak_bytes: 800000000,
          used_memory_peak_human: '762M',
          used_memory_peak_perc: '64%',
          used_memory_dataset_bytes: 500000000,
          used_memory_dataset_perc: '93%',
          total_system_memory_bytes: 4294967296,
          total_system_memory_human: '4G',
          maxmemory_bytes: 2147483648,
          maxmemory_human: '2G',
          maxmemory_policy: 'allkeys-lru',
          allocator: 'jemalloc',
          fragmentation_ratio: 1.1,
          fragmentation_bytes: 53687000
        },
        command_stats: [
          { command: 'get', calls: 95042, usec_total: 120000, usec_per_call: 1.2 },
          { command: 'set', calls: 45000, usec_total: 80000, usec_per_call: 1.7 },
          { command: 'ping', calls: 1200, usec_total: 5000, usec_per_call: 4.1 },
        ]
      },
      {
        node_id: 'db-node-replica-1',
        address: '10.0.0.2:7000',
        role: 'replica',
        status: 'ok',
        link_state: 'connected',
        flags: ['replica'],
        master_id: 'db-node-master-1',
        master_link_status: 'up',
        replication_lag_seconds: 0,
        memory: {
          used_memory_bytes: 536870912,
          used_memory_human: '512M',
          used_memory_rss_bytes: 600000000,
          used_memory_rss_human: '572M',
          used_memory_peak_bytes: 800000000,
          used_memory_peak_human: '762M',
          used_memory_peak_perc: '64%',
          used_memory_dataset_bytes: 500000000,
          used_memory_dataset_perc: '93%',
          total_system_memory_bytes: 4294967296,
          total_system_memory_human: '4G',
          maxmemory_bytes: 2147483648,
          maxmemory_human: '2G',
          maxmemory_policy: 'allkeys-lru',
          allocator: 'jemalloc',
          fragmentation_ratio: 1.1,
          fragmentation_bytes: 53687000
        },
      }
    ],
  },
  observability: {
    status: 'ok',
    traces_configured: true,
    metrics_configured: true,
    otlp_endpoint: 'http://localhost:4318',
    collector: {
      url: 'http://localhost:13133',
      status: 'ok',
      latency_ms: 2.1,
    },
  },
  services: [
    {
      name: 'Phoenix Matchmaker',
      kind: 'phoenix',
      url: 'http://localhost:4000',
      status: 'ok',
      http_status: 200,
      latency_ms: 12.4,
      reported_at: Date.now(),
      details: {
        memory: { total_bytes: 123456789, binary_bytes: 54321, process_count: 85 }
      }
    },
    {
      name: 'Go Signaling Hub',
      kind: 'go',
      url: 'http://localhost:8080',
      status: 'ok',
      http_status: 200,
      latency_ms: 3.1,
      reported_at: Date.now(),
      details: {
        memory: { heap_alloc_bytes: 86420500, sys_bytes: 133400000, goroutines: 642 }
      }
    }
  ],
  summary: {
    healthy_services: 12,
    degraded_services: 0,
    total_services: 12,
  },
};

export const AuthenticatedReportsView: Story = {
  name: 'Reports Tab (Admin)',
  args: {
    mockState: {
      isAuthenticated: true,
      authReady: true,
      currentAdminUsername: 'albert',
      role: 'admin',
      currentTab: 'reports',
      reports: mockReports,
      serverReportMetrics: { pending: 1, approved: 0, rejected: 0 }
    }
  }
};

export const ReportsEmptyState: Story = {
  name: 'Reports Tab Empty',
  args: {
    mockState: {
      isAuthenticated: true,
      authReady: true,
      currentAdminUsername: 'albert',
      role: 'admin',
      currentTab: 'reports',
      reports: [],
      reportStatusFilter: 'decided',
      serverReportMetrics: { pending: 0, approved: 0, rejected: 0 },
    }
  }
};

export const ReportsTranscriptModal: Story = {
  name: 'Reports Transcript Open',
  args: {
    mockState: {
      isAuthenticated: true,
      authReady: true,
      currentAdminUsername: 'albert',
      role: 'admin',
      currentTab: 'reports',
      reports: mockReports,
      expandedReport: 'report-1',
      serverReportMetrics: { pending: 1, approved: 0, rejected: 0 },
    }
  }
};

export const ReportsDescriptionModal: Story = {
  name: 'Reports Description Open',
  args: {
    mockState: {
      isAuthenticated: true,
      authReady: true,
      currentAdminUsername: 'albert',
      role: 'admin',
      currentTab: 'reports',
      reports: mockReports,
      viewingDescription: 'report-1',
      serverReportMetrics: { pending: 1, approved: 0, rejected: 0 },
    }
  }
};

export const AuthenticatedBansView: Story = {
  name: 'Bans Tab (Moderator)',
  args: {
    mockState: {
      isAuthenticated: true,
      authReady: true,
      currentAdminUsername: 'mod_jane',
      role: 'moderator',
      currentTab: 'bans',
      bans: mockBans,
      serverBanMetrics: { active: 1, inactive: 0, total: 1 }
    }
  }
};

export const BansTemporaryModal: Story = {
  name: 'Bans Temporary Modal',
  args: {
    mockState: {
      isAuthenticated: true,
      authReady: true,
      currentAdminUsername: 'albert',
      role: 'admin',
      currentTab: 'bans',
      bans: mockBans,
      banModal: {
        open: true,
        sessionId: 'client-B',
        ip: '10.0.0.1',
        sourceReportId: 'report-1',
        target: 'ip',
        reason: 'Escalated abuse across multiple reports.',
        mode: 'temporary',
        durationValue: '7',
        durationUnit: 'days',
      },
      serverBanMetrics: { active: 1, inactive: 0, total: 1 }
    }
  }
};

export const AuthenticatedWordBansView: Story = {
  name: 'Word Bans Tab',
  args: {
    mockState: {
      isAuthenticated: true,
      authReady: true,
      currentAdminUsername: 'albert',
      role: 'admin',
      currentTab: 'bannedWords',
      bannedWords: mockBannedWords,
    }
  }
};

export const AuthenticatedAccountsView: Story = {
  name: 'Accounts Tab (Root)',
  args: {
    mockState: {
      isAuthenticated: true,
      authReady: true,
      currentAdminUsername: 'albert',
      role: 'root',
      currentTab: 'accounts',
      accounts: mockAccounts,
      showCreateAccountPassword: true,
    }
  }
};

export const AccountsUserMenuOpen: Story = {
  name: 'Accounts With User Menu Open',
  args: {
    mockState: {
      isAuthenticated: true,
      authReady: true,
      currentAdminUsername: 'albert',
      role: 'root',
      currentTab: 'accounts',
      accounts: mockAccounts,
      isUserMenuOpen: true,
    }
  }
};

export const AuthenticatedInfraView: Story = {
  name: 'Infrastructure Tab (Root)',
  args: {
    mockState: {
      isAuthenticated: true,
      authReady: true,
      currentAdminUsername: 'albert',
      role: 'root',
      currentTab: 'infra',
      infraHealth: mockInfra,
    }
  }
};

export const InfraRedisModalOpen: Story = {
  name: 'Infra Redis Modal Open',
  args: {
    mockState: {
      isAuthenticated: true,
      authReady: true,
      currentAdminUsername: 'albert',
      role: 'root',
      currentTab: 'infra',
      infraHealth: mockInfra,
      isRedisModalOpen: true,
    }
  }
};

export const MobileMenuOpen: Story = {
  name: 'Mobile Menu Open',
  args: {
    mockState: {
      isAuthenticated: true,
      authReady: true,
      currentAdminUsername: 'albert',
      role: 'root',
      currentTab: 'reports',
      reports: mockReports,
      serverReportMetrics: { pending: 1, approved: 0, rejected: 0 },
      isMobileMenuOpen: true,
    }
  }
};
