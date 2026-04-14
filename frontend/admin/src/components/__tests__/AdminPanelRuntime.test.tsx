import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { describe, it, expect, beforeEach, vi } from 'vitest';
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
    sessionStorage.setItem('admin_csrf', 'csrf-token');
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
    vi.stubGlobal('fetch', vi.fn());
    vi.spyOn(window, 'alert').mockImplementation(() => {});
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

  it('submits report approval actions to the admin API', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ reports: [], metrics: { pending: 0, approved: 1, rejected: 0 } }),
      headers: new Headers(),
    } as Response);

    renderAdmin({
      authReady: true,
      isAuthenticated: true,
      currentAdminUsername: 'albert',
      role: 'admin',
      currentTab: 'reports',
      reports: [{
        id: 'report-1',
        reporter_session_id: 'reporter',
        reported_session_id: 'reported',
        reporter_ip: '1.1.1.1',
        reported_ip: '2.2.2.2',
        reason: 'abuse',
        description: 'bad actor',
        chat_log: [],
        status: 'pending',
        created_at: new Date().toISOString(),
      }],
      serverReportMetrics: { pending: 1, approved: 0, rejected: 0 },
    });

    fireEvent.click(screen.getByRole('button', { name: /^approve$/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalled();
    });

    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toContain('/api/v1/admin/reports/report-1');
    expect(init?.method).toBe('PUT');
    expect(JSON.parse(String(init?.body))).toEqual({ status: 'approved' });
  });

  it('shows auto moderation details and override controls for auto-reviewed reports', () => {
    renderAdmin({
      authReady: true,
      isAuthenticated: true,
      currentAdminUsername: 'albert',
      role: 'admin',
      currentTab: 'reports',
      autoModerationSettings: {
        enabled: true,
        enabled_default: false,
        configured: true,
        model: 'nvidia/llama-3.1-nemotron-safety-guard-8b-v3',
        batch_size: 10,
        interval_seconds: 30,
        timeout_seconds: 20,
        max_attempts: 3,
      },
      reports: [{
        id: 'report-2',
        reporter_session_id: 'reporter',
        reported_session_id: 'reported',
        reporter_ip: '1.1.1.1',
        reported_ip: '2.2.2.2',
        reason: 'threats',
        description: 'violent message',
        chat_log: [],
        status: 'approved',
        auto_moderation_state: 'completed',
        auto_moderation_decision: 'approved',
        auto_moderation_categories: ['violence'],
        auto_moderation_summary: 'Approved automatically because the reported content was marked unsafe.',
        reviewed_by_username: 'auto-moderation',
        reviewed_at: new Date().toISOString(),
        created_at: new Date().toISOString(),
      }],
      reportStatusFilter: 'all',
      reportReviewSourceFilter: 'autoReviewed',
      serverReportMetrics: { pending: 0, approved: 1, rejected: 0 },
    });

    expect(screen.getByRole('button', { name: /auto reviewed/i })).toBeInTheDocument();
    expect(screen.getByText(/approved automatically because the reported content was marked unsafe/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /reject override/i })).toBeInTheDocument();
  });

  it('submits auto moderation toggle requests to the admin API', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
        enabled: false,
        enabled_default: false,
        configured: true,
        model: 'nvidia/llama-3.1-nemotron-safety-guard-8b-v3',
        batch_size: 10,
        interval_seconds: 30,
        timeout_seconds: 20,
        max_attempts: 3,
      }),
      headers: new Headers(),
    } as Response);

    renderAdmin({
      authReady: true,
      isAuthenticated: true,
      currentAdminUsername: 'albert',
      role: 'admin',
      currentTab: 'reports',
      autoModerationSettings: {
        enabled: true,
        enabled_default: false,
        configured: true,
        model: 'nvidia/llama-3.1-nemotron-safety-guard-8b-v3',
        batch_size: 10,
        interval_seconds: 30,
        timeout_seconds: 20,
        max_attempts: 3,
      },
      reports: [],
      reportStatusFilter: 'all',
      serverReportMetrics: { pending: 0, approved: 0, rejected: 0 },
    });

    fireEvent.click(screen.getByRole('button', { name: /disable auto moderation/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalled();
    });

    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toContain('/api/v1/admin/auto-moderation/settings');
    expect(init?.method).toBe('PUT');
    expect(JSON.parse(String(init?.body))).toEqual({ enabled: false });
  });

  it('submits account creation requests', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: async () => ({}),
        headers: new Headers(),
      } as Response)
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: async () => ({ accounts: [], total: 0 }),
        headers: new Headers(),
      } as Response);

    renderAdmin({
      authReady: true,
      isAuthenticated: true,
      currentAdminUsername: 'albert',
      role: 'root',
      currentTab: 'accounts',
      accounts: [],
    });

    fireEvent.change(screen.getByPlaceholderText('Username'), { target: { value: 'new_mod' } });
    fireEvent.change(screen.getByPlaceholderText('Password'), { target: { value: 'super-secret' } });
    fireEvent.click(screen.getByRole('button', { name: /add member/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalled();
    });

    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toContain('/api/v1/admin/accounts');
    expect(init?.method).toBe('POST');
    expect(JSON.parse(String(init?.body))).toMatchObject({
      username: 'new_mod',
      password: 'super-secret',
      role: 'moderator',
    });
  });

  it('submits temporary ban modal data with expiry', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: async () => ({}),
        headers: new Headers(),
      } as Response)
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: async () => ({ bans: [], metrics: { active: 1, inactive: 0, total: 1 } }),
        headers: new Headers(),
      } as Response)
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: async () => ({ reports: [], metrics: { pending: 0, approved: 1, rejected: 0 } }),
        headers: new Headers(),
      } as Response);

    renderAdmin({
      authReady: true,
      isAuthenticated: true,
      currentAdminUsername: 'albert',
      role: 'admin',
      currentTab: 'bans',
      banModal: {
        open: true,
        sessionId: 'reported-session',
        ip: '10.0.0.5',
        sourceReportId: 'report-1',
        target: 'ip',
        reason: 'Escalated abuse',
        mode: 'temporary',
        durationValue: '3',
        durationUnit: 'days',
      },
      bans: [],
      reports: [],
      serverBanMetrics: { active: 0, inactive: 0, total: 0 },
    });

    fireEvent.click(screen.getByRole('button', { name: /confirm_enforcement/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalled();
    });

    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toContain('/api/v1/admin/ban');
    expect(init?.method).toBe('POST');

    const body = JSON.parse(String(init?.body));
    expect(body).toMatchObject({
      ip: '10.0.0.5',
      reason: 'Escalated abuse',
      report_id: 'report-1',
    });
    expect(body.expiry_date).toBeTruthy();
  });
});
