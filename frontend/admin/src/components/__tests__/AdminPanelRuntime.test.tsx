import { render, screen } from '@testing-library/react';
import { describe, it, expect, beforeEach } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { ThemeProvider } from '../../context/ThemeContext';
import { AdminPanelStoryHarness } from '../AdminPanelStoryHarness';

function renderAdmin(mockState?: Parameters<typeof AdminPanelStoryHarness>[0]['mockState']) {
  return render(
    <MemoryRouter>
      <ThemeProvider>
        <AdminPanelStoryHarness mockState={mockState} />
      </ThemeProvider>
    </MemoryRouter>
  );
}

describe('AdminPanelRuntime state coverage', () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: (query: string) => ({
        matches: query.includes('light'),
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      }),
    });
  });

  it('renders the loading screen when auth is not ready', () => {
    renderAdmin({ authReady: false });
    expect(screen.getByText(/initializing pairline console/i)).toBeInTheDocument();
  });

  it('renders the login screen when unauthenticated', () => {
    renderAdmin({ authReady: true, isAuthenticated: false });
    expect(screen.getByText(/moderation & safety console/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /sign in to console/i })).toBeInTheDocument();
  });

  it('renders modal and role-gated admin states from story harness data', () => {
    renderAdmin({
      authReady: true,
      isAuthenticated: true,
      currentAdminUsername: 'albert',
      role: 'root',
      currentTab: 'infra',
      isRedisModalOpen: true,
      infraHealth: {
        status: 'ok',
        service: 'pairline_api',
        timestamp: Date.now(),
        topology: {
          phoenix_configured_nodes: 2,
          phoenix_connected_nodes: 2,
          phoenix_node_names: ['node1@127.0.0.1'],
          go_configured_services: 1,
          redis_configured_nodes: 2,
          redis_reachable_nodes: 2,
        },
        postgres: {
          status: 'ok',
          latency_ms: 1,
          connections: { open: 1, in_use: 1, idle: 0, max_open: 10 },
        },
        redis: {
          status: 'ok',
          latency_ms: 1,
          configured_nodes: ['127.0.0.1:7000'],
          cluster: {
            state: 'ok',
            slots_assigned: 16384,
            slots_ok: 16384,
            slots_pfail: 0,
            slots_fail: 0,
            known_nodes: 2,
            size: 1,
            current_epoch: 1,
            my_epoch: 1,
            total_cluster_links_buffer_limit_exceeded: 0,
          },
          nodes: [
            {
              node_id: 'redis-1',
              address: '127.0.0.1:7000',
              role: 'master',
              status: 'ok',
              link_state: 'connected',
              flags: ['master'],
              memory: {
                used_memory_bytes: 1,
                used_memory_human: '1B',
                used_memory_rss_bytes: 1,
                used_memory_rss_human: '1B',
                used_memory_peak_bytes: 1,
                used_memory_peak_human: '1B',
                used_memory_peak_perc: '100%',
                used_memory_dataset_bytes: 1,
                used_memory_dataset_perc: '100%',
                total_system_memory_bytes: 1,
                total_system_memory_human: '1B',
                maxmemory_bytes: 1,
                maxmemory_human: '1B',
                maxmemory_policy: 'allkeys-lru',
                allocator: 'jemalloc',
                fragmentation_ratio: 1,
                fragmentation_bytes: 0,
              },
              command_stats: [{ command: 'get', calls: 1, usec_total: 1, usec_per_call: 1 }],
            },
          ],
        },
        observability: {
          status: 'ok',
          traces_configured: true,
          metrics_configured: true,
          otlp_endpoint: 'http://localhost:4318',
          collector: { url: 'http://localhost:13133', status: 'ok', latency_ms: 1 },
        },
        services: [],
        summary: { healthy_services: 3, degraded_services: 0, total_services: 3 },
      },
    });

    expect(screen.getByRole('heading', { name: /infra health/i })).toBeInTheDocument();
    expect(screen.getByText(/redis_cluster_details/i)).toBeInTheDocument();
    expect(screen.getAllByText(/admin accounts/i).length).toBeGreaterThan(0);
  });
});
