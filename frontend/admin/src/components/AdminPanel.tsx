import React, { useDeferredValue, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import DOMPurify from 'dompurify';
import type { AdminAccount, AdminRole, CreateBanRequest, LoginResponse, Report } from '../types';

interface Ban {
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

interface BanModalState {
  open: boolean;
  sessionId: string;
  ip: string;
  target: 'session' | 'ip';
  reason: string;
  mode: 'permanent' | 'temporary';
  durationValue: string;
  durationUnit: 'hours' | 'days';
  clearManualInputsOnSubmit?: boolean;
}

const tabButtonClass = (active: boolean) =>
  `rounded-full px-4 py-2 text-sm font-semibold transition ${
    active
      ? 'bg-white text-slate-950 shadow-lg shadow-cyan-950/20'
      : 'text-slate-300 hover:bg-white/10 hover:text-white'
  }`;

const filterButtonClass = (active: boolean) =>
  `rounded-full border px-3 py-1.5 text-xs font-semibold uppercase tracking-[0.18em] transition ${
    active
      ? 'border-cyan-300/60 bg-cyan-300/20 text-cyan-100'
      : 'border-white/10 bg-white/5 text-slate-400 hover:border-white/20 hover:text-slate-200'
  }`;

const metricCardClass =
  'rounded-[28px] border border-white/10 bg-white/6 p-5 backdrop-blur-xl shadow-[0_24px_80px_rgba(15,23,42,0.28)]';

const surfaceCardClass =
  'rounded-[30px] border border-white/10 bg-slate-950/55 backdrop-blur-xl shadow-[0_30px_120px_rgba(8,15,30,0.55)]';

const inputClass =
  'w-full rounded-2xl border border-white/10 bg-white/6 px-4 py-3 text-sm text-white placeholder:text-slate-500 outline-none transition focus:border-cyan-300/60 focus:bg-white/8';

const actionButtonClass =
  'rounded-2xl px-4 py-2.5 text-sm font-semibold transition disabled:cursor-not-allowed disabled:opacity-50';

function buildAdminHeaders(includeJSON = false) {
  const headers: Record<string, string> = {};
  const csrfToken = window.sessionStorage.getItem('admin_csrf') || '';

  if (includeJSON) {
    headers['Content-Type'] = 'application/json';
  }

  if (csrfToken) {
    headers['X-CSRF-Token'] = csrfToken;
  }

  return headers;
}

function persistAdminSession(username: string | undefined, role: AdminRole, csrfToken?: string) {
  localStorage.setItem('admin_auth', 'true');
  localStorage.setItem('admin_role', role);
  if (username) {
    localStorage.setItem('admin_username', username);
  }

  if (csrfToken) {
    sessionStorage.setItem('admin_csrf', csrfToken);
  }
}

function clearAdminSession() {
  localStorage.removeItem('admin_auth');
  localStorage.removeItem('admin_role');
  localStorage.removeItem('admin_username');
  sessionStorage.removeItem('admin_csrf');
}

function formatShort(value?: string | null, length = 12) {
  if (!value) return 'N/A';
  return value.length > length ? `${value.slice(0, length)}...` : value;
}

function formatDate(value?: string | null) {
  if (!value) return 'N/A';
  return new Date(value).toLocaleString();
}

function reportStatusClass(status: Report['status']) {
  if (status === 'approved') return 'bg-emerald-400/15 text-emerald-200 ring-1 ring-emerald-300/20';
  if (status === 'rejected') return 'bg-rose-400/15 text-rose-200 ring-1 ring-rose-300/20';
  return 'bg-amber-300/15 text-amber-100 ring-1 ring-amber-200/20';
}

function banStatusClass(active: boolean) {
  return active
    ? 'bg-rose-400/15 text-rose-200 ring-1 ring-rose-300/20'
    : 'bg-slate-400/15 text-slate-300 ring-1 ring-slate-300/20';
}

function buildExpiryDate(durationValue: string, durationUnit: 'hours' | 'days') {
  const amount = Number(durationValue);
  if (!Number.isFinite(amount) || amount <= 0) return null;

  const expiresAt = new Date();
  if (durationUnit === 'hours') {
    expiresAt.setHours(expiresAt.getHours() + amount);
  } else {
    expiresAt.setDate(expiresAt.getDate() + amount);
  }

  return expiresAt.toISOString();
}

interface AdminPanelProps {
  loginRoute?: string;
}

export function AdminPanel({ loginRoute = '/' }: AdminPanelProps) {
  const navigate = useNavigate();
  const [isAuthenticated, setIsAuthenticated] = useState(localStorage.getItem('admin_auth') === 'true');
  const [authReady, setAuthReady] = useState(false);
  const [role, setRole] = useState<AdminRole | null>((localStorage.getItem('admin_role') as AdminRole | null) || null);
  const [currentAdminUsername, setCurrentAdminUsername] = useState(localStorage.getItem('admin_username') || '');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [showLoginPassword, setShowLoginPassword] = useState(false);
  const [reports, setReports] = useState<Report[]>([]);
  const [accounts, setAccounts] = useState<AdminAccount[]>([]);
  const [selectedReports, setSelectedReports] = useState<Set<string>>(new Set());
  const [expandedReport, setExpandedReport] = useState<string | null>(null);
  const [bans, setBans] = useState<Ban[]>([]);
  const [currentTab, setCurrentTab] = useState<'reports' | 'bans' | 'accounts'>('reports');
  const [reportStatusFilter, setReportStatusFilter] = useState<'pending' | 'decided' | 'all'>('pending');
  const [reportLimit, setReportLimit] = useState<string>('10');
  const [serverReportMetrics, setServerReportMetrics] = useState({ pending: 0, approved: 0, rejected: 0 });
  const [banFilter, setBanFilter] = useState<'all' | 'active' | 'inactive'>('active');
  const [banLimit, setBanLimit] = useState<string>('10');
  const [banSearch, setBanSearch] = useState('');
  const [serverBanMetrics, setServerBanMetrics] = useState({ active: 0, inactive: 0, total: 0 });
  const [manualBanSessionId, setManualBanSessionId] = useState('');
  const [manualBanIP, setManualBanIP] = useState('');
  const [manualBanReason, setManualBanReason] = useState('');
  const [accountUsername, setAccountUsername] = useState('');
  const [accountPassword, setAccountPassword] = useState('');
  const [showCreateAccountPassword, setShowCreateAccountPassword] = useState(false);
  const [accountRole, setAccountRole] = useState<AdminRole>('moderator');
  const [submittingAccount, setSubmittingAccount] = useState(false);
  const [submittingBan, setSubmittingBan] = useState(false);
  const [banModal, setBanModal] = useState<BanModalState>({
    open: false,
    sessionId: '',
    ip: '',
    target: 'session',
    reason: '',
    mode: 'permanent',
    durationValue: '24',
    durationUnit: 'hours',
  });

  const canCreateBans = role === 'moderator' || role === 'admin' || role === 'root';
  const canManageBans = role === 'admin' || role === 'root';
  const canManageAccounts = role === 'admin' || role === 'root';
  const deferredBanSearch = useDeferredValue(banSearch);

  useEffect(() => {
    if (authReady && isAuthenticated) {
      fetchBans();
    }
  }, [authReady, isAuthenticated, banFilter, banLimit, deferredBanSearch]);

  useEffect(() => {
    if (authReady && isAuthenticated) {
      fetchReports();
    }
  }, [authReady, isAuthenticated, reportStatusFilter, reportLimit]);

  useEffect(() => {
    if (authReady && isAuthenticated && canManageAccounts) {
      void fetchAccounts();
    }
  }, [authReady, isAuthenticated, canManageAccounts]);

  useEffect(() => {
    const bootstrapAuth = async () => {
      const rememberedAuth = localStorage.getItem('admin_auth') === 'true';
      const csrfToken = sessionStorage.getItem('admin_csrf');

      if (!rememberedAuth || !csrfToken) {
        setAuthReady(true);
        return;
      }

      const refreshed = await refreshSession();
      if (!refreshed) {
        clearAdminSession();
        setIsAuthenticated(false);
        setRole(null);
      }
      setAuthReady(true);
    };

    void bootstrapAuth();
  }, []);

  const storeCSRFFromResponse = async (response: Response) => {
    const csrfToken = response.headers.get('X-CSRF-Token');
    if (csrfToken) {
      sessionStorage.setItem('admin_csrf', csrfToken);
    }
  };

  const refreshSession = async () => {
    const csrfToken = window.sessionStorage.getItem('admin_csrf') || '';

    try {
      const response = await fetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/refresh`, {
        method: 'POST',
        headers: csrfToken ? { 'X-CSRF-Token': csrfToken } : {},
        credentials: 'include',
      });

      if (!response.ok) {
        return false;
      }

      await storeCSRFFromResponse(response);
      const data: LoginResponse = await response.json();
      persistAdminSession(data.username, data.role, data.csrf_token);
      setIsAuthenticated(true);
      setRole(data.role);
      setCurrentAdminUsername(data.username || localStorage.getItem('admin_username') || '');
      return true;
    } catch (error) {
      console.error('Failed to refresh admin session:', error);
      return false;
    }
  };

  const adminFetch = async (path: string, init: RequestInit = {}, retryOnUnauthorized = true) => {
    const response = await fetch(path, {
      ...init,
      headers: {
        ...buildAdminHeaders(Boolean(init.body)),
        ...(init.headers || {}),
      },
      credentials: 'include',
    });

    if (response.status !== 401 || !retryOnUnauthorized) {
      return response;
    }

    const refreshed = await refreshSession();
    if (!refreshed) {
      clearAdminSession();
      setIsAuthenticated(false);
      setRole(null);
      return response;
    }

    return fetch(path, {
      ...init,
      headers: {
        ...buildAdminHeaders(Boolean(init.body)),
        ...(init.headers || {}),
      },
      credentials: 'include',
    });
  };

  const login = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const response = await fetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/login`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, password }),
        credentials: 'include',
      });

      if (response.ok) {
        await storeCSRFFromResponse(response);
        const data: LoginResponse = await response.json();
        persistAdminSession(data.username || username, data.role, data.csrf_token);
        setIsAuthenticated(true);
        setRole(data.role);
        setCurrentAdminUsername(data.username || username);
        fetchReports();
      } else {
        alert('Invalid credentials');
      }
    } catch (error) {
      console.error('Login failed:', error);
      alert('Login failed');
    }
  };

  const logout = async () => {
    try {
      await fetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/logout`, {
        method: 'POST',
        headers: buildAdminHeaders(),
        credentials: 'include',
      });
    } catch (error) {
      console.error('Failed to clear admin session cookies:', error);
    }

    setIsAuthenticated(false);
    setRole(null);
    setCurrentAdminUsername('');
    clearAdminSession();
    setReports([]);
    setBans([]);
    setAccounts([]);
    navigate(loginRoute);
  };

  const fetchReports = async () => {
    try {
      const response = await adminFetch(
        `${import.meta.env.VITE_API_URL}/api/v1/admin/reports?status=${reportStatusFilter}&limit=${reportLimit}`
      );

      if (response.status === 401) return logout();

      if (response.ok) {
        const data = await response.json();
        if (data.metrics) {
          setServerReportMetrics({
            pending: data.metrics.pending || 0,
            approved: data.metrics.approved || 0,
            rejected: data.metrics.rejected || 0,
          });
        }
        const normalized = (data.reports || []).map((r: any) => ({
          ...r,
          chat_log:
            typeof r.chat_log === 'string'
              ? (() => {
                  try {
                    return JSON.parse(r.chat_log);
                  } catch {
                    return [];
                  }
                })()
              : Array.isArray(r.chat_log)
                ? r.chat_log
                : [],
        }));
        setReports(normalized);
      }
    } catch (error) {
      console.error('Failed to fetch reports:', error);
    }
  };

  const updateReportStatus = async (reportId: string, newStatus: 'approved' | 'rejected') => {
    try {
      const response = await adminFetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/reports/${reportId}`, {
        method: 'PUT',
        body: JSON.stringify({ status: newStatus }),
      });

      if (response.status === 401) return logout();

      if (response.ok) {
        await storeCSRFFromResponse(response);
        fetchReports();
      }
    } catch (error) {
      console.error('Failed to update report:', error);
    }
  };

  const createBan = async (request: CreateBanRequest) => {
    if (!canCreateBans) {
      alert('Your role cannot create bans');
      return false;
    }

    try {
      const response = await adminFetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/ban`, {
        method: 'POST',
        body: JSON.stringify(request),
      });

      if (response.status === 401) {
        logout();
        return false;
      }

      if (response.ok) {
        await storeCSRFFromResponse(response);
        const data = await response.json();
        alert(data.status === 'already_banned' ? 'User is already banned' : 'User banned successfully');
        fetchBans();
        fetchReports();
        return true;
      }
    } catch (error) {
      console.error('Failed to create ban:', error);
      alert('Failed to create ban');
    }

    return false;
  };

  const openBanModal = ({
    sessionId = '',
    ip = '',
    target,
    reason = '',
    clearManualInputsOnSubmit = false,
  }: {
    sessionId?: string;
    ip?: string;
    target?: 'session' | 'ip';
    reason?: string;
    clearManualInputsOnSubmit?: boolean;
  }) => {
    const nextTarget = target || (sessionId ? 'session' : 'ip');
    setBanModal({
      open: true,
      sessionId,
      ip,
      target: nextTarget,
      reason,
      mode: 'permanent',
      durationValue: '24',
      durationUnit: 'hours',
      clearManualInputsOnSubmit,
    });
  };

  const closeBanModal = () => {
    if (submittingBan) return;
    setBanModal((current) => ({ ...current, open: false }));
  };

  const submitBanModal = async () => {
    const targetValue = banModal.target === 'session' ? banModal.sessionId : banModal.ip;
    if (!targetValue) {
      alert(`Missing ${banModal.target === 'session' ? 'session ID' : 'IP address'} for ban`);
      return;
    }

    if (!banModal.reason.trim()) {
      alert('Please enter a reason');
      return;
    }

    const request: CreateBanRequest = {
      reason: banModal.reason.trim(),
    };

    if (banModal.target === 'session') {
      request.session_id = banModal.sessionId;
      if (banModal.ip) {
        request.ip = banModal.ip;
      }
    } else {
      request.ip = banModal.ip;
    }

    if (banModal.mode === 'temporary') {
      const expiryDate = buildExpiryDate(banModal.durationValue, banModal.durationUnit);
      if (!expiryDate) {
        alert('Please enter a valid duration');
        return;
      }
      request.expiry_date = expiryDate;
    }

    setSubmittingBan(true);
    const success = await createBan(request);
    setSubmittingBan(false);

    if (!success) {
      return;
    }

    if (banModal.clearManualInputsOnSubmit) {
      setManualBanSessionId('');
      setManualBanIP('');
      setManualBanReason('');
    }

    setBanModal((current) => ({ ...current, open: false }));
  };

  const fetchBans = async () => {
    try {
      const params = new URLSearchParams({
        status: banFilter,
        limit: banLimit,
      });

      const normalizedBanSearch = deferredBanSearch.trim();
      if (normalizedBanSearch) {
        params.set('ip', normalizedBanSearch);
      }

      const response = await adminFetch(
        `${import.meta.env.VITE_API_URL}/api/v1/admin/bans?${params.toString()}`
      );

      if (response.status === 401) return logout();

      if (response.ok) {
        const data = await response.json();
        if (data.metrics) {
          setServerBanMetrics({
            active: data.metrics.active || 0,
            inactive: data.metrics.inactive || 0,
            total: data.metrics.total || 0,
          });
        }
        setBans(data.bans || []);
      }
    } catch (error) {
      console.error('Failed to fetch bans:', error);
    }
  };

  const unban = async (banId: string) => {
    if (!canManageBans) {
      alert('Your role cannot remove bans');
      return;
    }

    try {
      const response = await adminFetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/ban/${banId}`, {
        method: 'DELETE',
      });

      if (response.status === 401) return logout();

      if (response.ok) {
        await storeCSRFFromResponse(response);
        alert('User unbanned successfully');
        fetchBans();
        fetchReports();
      }
    } catch (error) {
      console.error('Failed to unban:', error);
      alert('Failed to unban');
    }
  };

  const fetchAccounts = async () => {
    if (!canManageAccounts) return;

    try {
      const response = await adminFetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/accounts`);
      if (response.status === 401) return logout();
      if (!response.ok) return;

      const data = await response.json();
      setAccounts(data.accounts || []);
    } catch (error) {
      console.error('Failed to fetch admin accounts:', error);
    }
  };

  const createAccount = async () => {
    if (!canManageAccounts) return;
    if (!accountUsername.trim() || !accountPassword.trim()) {
      alert('Please enter username and password');
      return;
    }

    setSubmittingAccount(true);
    try {
      const response = await adminFetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/accounts`, {
        method: 'POST',
        body: JSON.stringify({
          username: accountUsername.trim(),
          password: accountPassword,
          role: accountRole,
        }),
      });

      if (response.status === 401) return logout();
      if (response.ok) {
        await storeCSRFFromResponse(response);
        setAccountUsername('');
        setAccountPassword('');
        setAccountRole('moderator');
        await fetchAccounts();
        alert('Admin account created');
        return;
      }

      const data = await response.json().catch(() => ({}));
      alert(data.error || 'Failed to create admin account');
    } catch (error) {
      console.error('Failed to create admin account:', error);
      alert('Failed to create admin account');
    } finally {
      setSubmittingAccount(false);
    }
  };

  const deleteAccount = async (targetUsername: string) => {
    if (!canManageAccounts) return;
    if (!window.confirm(`Delete admin account "${targetUsername}"?`)) return;

    try {
      const response = await adminFetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/accounts/${encodeURIComponent(targetUsername)}`, {
        method: 'DELETE',
      });

      if (response.status === 401) return logout();
      if (response.ok) {
        await storeCSRFFromResponse(response);
        await fetchAccounts();
        alert('Admin account deleted');
        return;
      }

      const data = await response.json().catch(() => ({}));
      alert(data.error || 'Failed to delete admin account');
    } catch (error) {
      console.error('Failed to delete admin account:', error);
      alert('Failed to delete admin account');
    }
  };

  if (!authReady) {
    return (
      <div className="min-h-screen bg-[#050816] text-white">
        <div className="fixed inset-0 bg-[radial-gradient(circle_at_top_left,_rgba(34,211,238,0.15),_transparent_28%),radial-gradient(circle_at_80%_20%,_rgba(244,114,182,0.14),_transparent_22%),linear-gradient(180deg,_#0a1020_0%,_#050816_55%,_#02040b_100%)]" />
        <div className="relative flex min-h-screen items-center justify-center px-6">
          <div className="rounded-[28px] border border-white/10 bg-white/6 px-6 py-4 text-sm text-slate-300 backdrop-blur-xl">
            Restoring moderation session...
          </div>
        </div>
      </div>
    );
  }

  if (!isAuthenticated) {
    return (
      <div className="min-h-screen overflow-hidden bg-[#050816] text-white">
        <div className="absolute inset-0 bg-[radial-gradient(circle_at_top_left,_rgba(34,211,238,0.18),_transparent_26%),radial-gradient(circle_at_80%_20%,_rgba(244,114,182,0.16),_transparent_20%),linear-gradient(180deg,_#0a1020_0%,_#050816_50%,_#02040b_100%)]" />
        <div className="relative mx-auto flex min-h-screen max-w-6xl items-center justify-center px-6 py-12">
          <div className="grid w-full gap-8 lg:grid-cols-[1.15fr_0.85fr]">
            <section className="rounded-[36px] border border-white/10 bg-white/6 p-8 backdrop-blur-2xl shadow-[0_30px_140px_rgba(3,8,20,0.65)]">
              <div className="mb-8 inline-flex rounded-full border border-cyan-300/20 bg-cyan-300/10 px-3 py-1 text-xs font-semibold uppercase tracking-[0.22em] text-cyan-100">
                Moderation Console
              </div>
              <h1 className="max-w-xl text-4xl font-semibold leading-tight text-white sm:text-5xl">
                Review reports and act fast with a cleaner admin workspace.
              </h1>
              <p className="mt-5 max-w-xl text-sm leading-7 text-slate-300 sm:text-base">
                Designed for high-signal moderation: pending queues, active bans, transcript review, and fast response controls in one modern surface.
              </p>
              <div className="mt-10 grid gap-4 sm:grid-cols-3">
                <div className={metricCardClass}>
                  <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Signal</p>
                  <p className="mt-3 text-3xl font-semibold text-white">Live</p>
                  <p className="mt-2 text-sm text-slate-400">Reports and bans stay in one moderation stream.</p>
                </div>
                <div className={metricCardClass}>
                  <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Focus</p>
                  <p className="mt-3 text-3xl font-semibold text-white">Sharp</p>
                  <p className="mt-2 text-sm text-slate-400">Readable spacing, fast actions, less scanning fatigue.</p>
                </div>
                <div className={metricCardClass}>
                  <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Control</p>
                  <p className="mt-3 text-3xl font-semibold text-white">Direct</p>
                  <p className="mt-2 text-sm text-slate-400">Jump from transcripts to bans without context switching.</p>
                </div>
              </div>
            </section>

            <section className="rounded-[36px] border border-white/10 bg-slate-950/70 p-8 backdrop-blur-2xl shadow-[0_24px_100px_rgba(2,6,23,0.6)]">
              <div className="mb-8">
                <p className="text-xs uppercase tracking-[0.22em] text-slate-400">Secure Access</p>
                <h2 className="mt-3 text-2xl font-semibold text-white">Admin Login</h2>
              </div>
              <form onSubmit={login} className="space-y-5">
                <div>
                  <label className="mb-2 block text-sm font-medium text-slate-300">Username</label>
                  <input
                    type="text"
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                    className={inputClass}
                    required
                  />
                </div>
                <div>
                  <label className="mb-2 block text-sm font-medium text-slate-300">Password</label>
                  <div className="relative">
                    <input
                      type={showLoginPassword ? 'text' : 'password'}
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      className={`${inputClass} pr-24`}
                      required
                    />
                    <button
                      type="button"
                      onClick={() => setShowLoginPassword((current) => !current)}
                      className="absolute inset-y-1.5 right-1.5 rounded-xl border border-white/10 bg-white/8 px-3 text-xs font-semibold uppercase tracking-[0.16em] text-slate-300 transition hover:bg-white/12"
                    >
                      {showLoginPassword ? 'Hide' : 'Show'}
                    </button>
                  </div>
                </div>
                <button
                  type="submit"
                  className="w-full rounded-2xl bg-white px-4 py-3 text-sm font-semibold text-slate-950 transition hover:bg-cyan-100"
                >
                  Enter Dashboard
                </button>
              </form>
            </section>
          </div>
        </div>
      </div>
    );
  }

  const quickActionTitle =
    currentTab === 'reports' ? 'Review Controls' : currentTab === 'bans' ? 'Ban Controls' : 'Account Controls';
  const permanentBans = bans.filter((ban) => !ban.expires_at);
  const temporaryBans = bans.filter((ban) => Boolean(ban.expires_at));

  const renderBanCard = (ban: Ban) => (
    <article
      key={ban.id}
      className="rounded-[28px] border border-white/10 bg-white/5 p-5 shadow-[0_20px_60px_rgba(15,23,42,0.2)]"
    >
      <div className="flex flex-col gap-4 xl:flex-row xl:items-center xl:justify-between">
        <div className="space-y-4">
          <div className="flex flex-wrap items-center gap-3">
            <span className={`rounded-full px-3 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${banStatusClass(ban.is_active)}`}>
              {ban.is_active ? 'Active' : 'Inactive'}
            </span>
            <span className="rounded-full border border-white/10 bg-white/6 px-3 py-1 text-xs font-semibold uppercase tracking-[0.18em] text-slate-300">
              {ban.expires_at ? 'Temporary' : 'Permanent'}
            </span>
            <span className="text-xs uppercase tracking-[0.18em] text-slate-500">{formatDate(ban.created_at)}</span>
          </div>
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <div className="min-w-0">
              <p className="text-[11px] uppercase tracking-[0.18em] text-slate-500">Ban ID</p>
              <p className="mt-1 break-all text-sm font-medium text-slate-200">{formatShort(ban.id, 16)}</p>
            </div>
            <div className="min-w-0">
              <p className="text-[11px] uppercase tracking-[0.18em] text-slate-500">Session ID</p>
              <p className="mt-1 break-all text-sm font-medium text-slate-200">{formatShort(ban.session_id, 16)}</p>
            </div>
            <div className="min-w-0">
              <p className="text-[11px] uppercase tracking-[0.18em] text-slate-500">IP Address</p>
              <p className="mt-1 break-all text-sm font-medium text-slate-200">{ban.ip_address || 'N/A'}</p>
            </div>
            <div className="min-w-0">
              <p className="text-[11px] uppercase tracking-[0.18em] text-slate-500">Banned By</p>
              <p className="mt-1 break-all text-sm font-medium text-slate-200">{ban.banned_by_username || 'N/A'}</p>
            </div>
          </div>
          <div>
            <p className="text-[11px] uppercase tracking-[0.18em] text-slate-500">Reason</p>
            <p className="mt-1 text-sm text-slate-200">{ban.reason}</p>
          </div>
          <div>
            <p className="text-[11px] uppercase tracking-[0.18em] text-slate-500">Expires</p>
            <p className="mt-1 text-sm text-slate-200">{ban.expires_at ? formatDate(ban.expires_at) : 'Permanent'}</p>
          </div>
        </div>

        <div className="xl:text-right">
          {ban.is_active && canManageBans ? (
            <button
              onClick={() => unban(ban.id)}
              className={`${actionButtonClass} bg-emerald-400/15 text-emerald-100 hover:bg-emerald-400/25`}
            >
              Unban
            </button>
          ) : ban.is_active ? (
            <div className="text-sm text-slate-400">
              Admin or root access required to remove bans
            </div>
          ) : (
            <div className="text-sm text-slate-400">
              Unbanned by {ban.unbanned_by_username || 'Unknown'}
            </div>
          )}
        </div>
      </div>
    </article>
  );

  return (
    <div className="min-h-screen bg-[#050816] text-white">
      <div className="fixed inset-0 bg-[radial-gradient(circle_at_top_left,_rgba(34,211,238,0.15),_transparent_28%),radial-gradient(circle_at_80%_20%,_rgba(244,114,182,0.14),_transparent_22%),radial-gradient(circle_at_50%_100%,_rgba(56,189,248,0.12),_transparent_28%),linear-gradient(180deg,_#0a1020_0%,_#050816_55%,_#02040b_100%)]" />
      <div className="relative mx-auto max-w-7xl px-4 py-6 sm:px-6 lg:px-8">
        <header className={`${surfaceCardClass} sticky top-4 z-20 mb-6 overflow-hidden`}>
          <div className="absolute inset-0 bg-[linear-gradient(135deg,rgba(255,255,255,0.09),transparent_35%,transparent_65%,rgba(34,211,238,0.12))]" />
          <div className="relative flex flex-col gap-5 px-6 py-6 lg:flex-row lg:items-center lg:justify-between">
            <div>
              <div className="inline-flex rounded-full border border-cyan-300/20 bg-cyan-300/10 px-3 py-1 text-[11px] font-semibold uppercase tracking-[0.22em] text-cyan-100">
                Admin Panel
              </div>
              <h1 className="mt-3 text-3xl font-semibold text-white">Trust & Safety Dashboard</h1>
              <p className="mt-2 max-w-2xl text-sm leading-6 text-slate-400">
                Review incoming reports, inspect transcripts, and enforce session or IP bans from a single moderation workspace.
              </p>
            </div>

            <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
              <div className="inline-flex rounded-full border border-white/10 bg-white/6 px-3 py-2 text-xs font-semibold uppercase tracking-[0.22em] text-slate-300">
                Role: {role || 'unknown'}
              </div>
              <div className="inline-flex rounded-full border border-white/10 bg-white/6 p-1">
                <button onClick={() => setCurrentTab('reports')} className={tabButtonClass(currentTab === 'reports')}>
                  Reports
                </button>
                <button onClick={() => setCurrentTab('bans')} className={tabButtonClass(currentTab === 'bans')}>
                  Bans
                </button>
                {canManageAccounts && (
                  <button onClick={() => setCurrentTab('accounts')} className={tabButtonClass(currentTab === 'accounts')}>
                    Accounts
                  </button>
                )}
              </div>
              <button
                onClick={logout}
                className="rounded-full border border-rose-300/20 bg-rose-400/10 px-4 py-2 text-sm font-semibold text-rose-100 transition hover:bg-rose-400/20"
              >
                Logout
              </button>
            </div>
          </div>
        </header>

        <section className="mb-6 grid gap-4 md:grid-cols-2 xl:grid-cols-5">
          <div className={metricCardClass}>
            <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Pending Reports</p>
            <p className="mt-3 text-3xl font-semibold text-white">{serverReportMetrics.pending}</p>
            <p className="mt-2 text-sm text-slate-400">Items still waiting for human review.</p>
          </div>
          <div className={metricCardClass}>
            <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Approved</p>
            <p className="mt-3 text-3xl font-semibold text-white">{serverReportMetrics.approved}</p>
            <p className="mt-2 text-sm text-slate-400">Reports already acted on.</p>
          </div>
          <div className={metricCardClass}>
            <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Rejected</p>
            <p className="mt-3 text-3xl font-semibold text-white">{serverReportMetrics.rejected}</p>
            <p className="mt-2 text-sm text-slate-400">Reports closed without action.</p>
          </div>
          <div className={metricCardClass}>
            <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Active Bans</p>
            <p className="mt-3 text-3xl font-semibold text-white">{serverBanMetrics.active}</p>
            <p className="mt-2 text-sm text-slate-400">Currently enforced session or IP blocks.</p>
          </div>
          <div className={metricCardClass}>
            <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Ban History</p>
            <p className="mt-3 text-3xl font-semibold text-white">{serverBanMetrics.total}</p>
            <p className="mt-2 text-sm text-slate-400">Includes active and inactive records.</p>
          </div>
        </section>

        <div className="flex flex-col gap-6">
          <aside className="space-y-6">
            <section className={`${surfaceCardClass} p-6`}>
              <div className="flex items-start justify-between gap-4">
                <div>
                  <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Quick Actions</p>
                  <h2 className="mt-2 text-xl font-semibold text-white">{quickActionTitle}</h2>
                </div>
              </div>

              {currentTab === 'reports' ? (
                <div className="mt-6 flex flex-col gap-4 lg:flex-row lg:items-end">
                  <div className="flex flex-col gap-2">
                    <label className="text-xs uppercase tracking-[0.2em] text-slate-400">Status Filter</label>
                    <div className="flex flex-wrap gap-2">
                      <button onClick={() => setReportStatusFilter('pending')} className={filterButtonClass(reportStatusFilter === 'pending')}>Pending</button>
                      <button onClick={() => setReportStatusFilter('decided')} className={filterButtonClass(reportStatusFilter === 'decided')}>Decided</button>
                      <button onClick={() => setReportStatusFilter('all')} className={filterButtonClass(reportStatusFilter === 'all')}>All</button>
                    </div>
                  </div>
                  <div className="flex flex-col gap-2">
                    <label className="text-xs uppercase tracking-[0.2em] text-slate-400">Show Limit</label>
                    <select
                      value={reportLimit}
                      onChange={(e) => setReportLimit(e.target.value)}
                      className={`${inputClass} appearance-none bg-white/5 [&>option]:bg-slate-900 w-full lg:w-48`}
                    >
                      <option value="10">10 entries</option>
                      <option value="20">20 entries</option>
                      <option value="50">50 entries</option>
                      <option value="all">All entries (Max)</option>
                    </select>
                  </div>
                  <div className="flex flex-wrap gap-2 lg:ml-auto">
                    <button
                      onClick={fetchReports}
                      className={`${actionButtonClass} bg-white text-slate-950 hover:bg-cyan-100`}
                    >
                      Refresh Reports
                    </button>
                    <button
                      onClick={() => {
                        selectedReports.forEach((id) => updateReportStatus(id, 'approved'));
                        setSelectedReports(new Set());
                      }}
                      disabled={selectedReports.size === 0}
                      className={`${actionButtonClass} bg-emerald-400/15 text-emerald-100 hover:bg-emerald-400/25`}
                    >
                      Approve Selected ({selectedReports.size})
                    </button>
                    <button
                      onClick={() => {
                        selectedReports.forEach((id) => updateReportStatus(id, 'rejected'));
                        setSelectedReports(new Set());
                      }}
                      disabled={selectedReports.size === 0}
                      className={`${actionButtonClass} bg-rose-400/15 text-rose-100 hover:bg-rose-400/25`}
                    >
                      Reject Selected ({selectedReports.size})
                    </button>
                  </div>
                </div>
              ) : currentTab === 'bans' ? (
                <div className="mt-6 flex flex-col gap-4 lg:flex-row lg:items-end">
                  <div className="flex flex-col gap-2">
                    <label className="text-xs uppercase tracking-[0.2em] text-slate-400">Ban Filter</label>
                    <div className="flex flex-wrap gap-2">
                      <button onClick={() => setBanFilter('active')} className={filterButtonClass(banFilter === 'active')}>
                        Active
                      </button>
                      <button onClick={() => setBanFilter('inactive')} className={filterButtonClass(banFilter === 'inactive')}>
                        Inactive
                      </button>
                      <button onClick={() => setBanFilter('all')} className={filterButtonClass(banFilter === 'all')}>
                        All
                      </button>
                    </div>
                  </div>
                  <div className="flex flex-col gap-2">
                    <label className="text-xs uppercase tracking-[0.2em] text-slate-400">Show Limit</label>
                    <select
                      value={banLimit}
                      onChange={(e) => setBanLimit(e.target.value)}
                      className={`${inputClass} appearance-none bg-white/5 [&>option]:bg-slate-900 w-full lg:w-48`}
                    >
                      <option value="10">10 entries</option>
                      <option value="20">20 entries</option>
                      <option value="50">50 entries</option>
                      <option value="all">All entries (Max)</option>
                    </select>
                  </div>
                  <div className="flex flex-wrap gap-2 lg:ml-auto">
                    <button
                      onClick={fetchBans}
                      className={`${actionButtonClass} bg-white text-slate-950 hover:bg-cyan-100`}
                    >
                      Refresh Bans
                    </button>
                  </div>
                </div>
              ) : (
                <div className="mt-6 flex flex-wrap gap-2">
                  <button
                    onClick={() => void fetchAccounts()}
                    className={`${actionButtonClass} bg-white text-slate-950 hover:bg-cyan-100`}
                  >
                    Refresh Accounts
                  </button>
                </div>
              )}
            </section>

            {currentTab === 'bans' && canCreateBans && (
              <section className={`${surfaceCardClass} p-6`}>
                <div className="mb-5">
                  <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Manual Ban</p>
                  <h2 className="mt-2 text-xl font-semibold text-white">Create a session or IP ban</h2>
                </div>

                <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4 items-end">
                  <div>
                    <label className="mb-2 block text-sm font-medium text-slate-300">Session ID</label>
                    <input
                      type="text"
                      value={manualBanSessionId}
                      onChange={(e) => setManualBanSessionId(e.target.value)}
                      className={inputClass}
                      placeholder="Paste a session ID"
                    />
                  </div>
                  <div>
                    <label className="mb-2 block text-sm font-medium text-slate-300">IP Address</label>
                    <input
                      type="text"
                      value={manualBanIP}
                      onChange={(e) => setManualBanIP(e.target.value)}
                      className={inputClass}
                      placeholder="Or block an IP"
                    />
                  </div>
                  <div>
                    <label className="mb-2 block text-sm font-medium text-slate-300">Reason</label>
                    <input
                      type="text"
                      value={manualBanReason}
                      onChange={(e) => setManualBanReason(e.target.value)}
                      className={inputClass}
                      placeholder="Explain why this user is being blocked"
                      required
                    />
                  </div>
                  <button
                    onClick={() => {
                      if (!manualBanSessionId && !manualBanIP) {
                        alert('Please enter either Session ID or IP Address');
                        return;
                      }
                      if (!manualBanReason) {
                        alert('Please enter a reason');
                        return;
                      }
                      openBanModal({
                        sessionId: manualBanSessionId,
                        ip: manualBanIP,
                        target: manualBanSessionId ? 'session' : 'ip',
                        reason: manualBanReason,
                        clearManualInputsOnSubmit: true,
                      });
                    }}
                    className={`${actionButtonClass} w-full bg-rose-500 text-white hover:bg-rose-400`}
                  >
                    Ban
                  </button>
                </div>
              </section>
            )}

            {currentTab === 'accounts' && canManageAccounts && (
              <section className={`${surfaceCardClass} p-6`}>
                <div className="mb-5">
                  <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Account Management</p>
                  <h2 className="mt-2 text-xl font-semibold text-white">Create admin and moderator accounts</h2>
                </div>

                <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4 items-end">
                  <div>
                    <label className="mb-2 block text-sm font-medium text-slate-300">Username</label>
                    <input
                      type="text"
                      value={accountUsername}
                      onChange={(e) => setAccountUsername(e.target.value)}
                      className={inputClass}
                      placeholder="moderator_name"
                    />
                  </div>
                  <div>
                    <label className="mb-2 block text-sm font-medium text-slate-300">Password</label>
                    <div className="relative">
                      <input
                        type={showCreateAccountPassword ? 'text' : 'password'}
                        value={accountPassword}
                        onChange={(e) => setAccountPassword(e.target.value)}
                        className={`${inputClass} pr-24`}
                        placeholder="Minimum 8 characters"
                      />
                      <button
                        type="button"
                        onClick={() => setShowCreateAccountPassword((current) => !current)}
                        className="absolute inset-y-1.5 right-1.5 rounded-xl border border-white/10 bg-white/8 px-3 text-xs font-semibold uppercase tracking-[0.16em] text-slate-300 transition hover:bg-white/12"
                      >
                        {showCreateAccountPassword ? 'Hide' : 'Show'}
                      </button>
                    </div>
                  </div>
                  <div>
                    <label className="mb-2 block text-sm font-medium text-slate-300">Role</label>
                    <select
                      value={accountRole}
                      onChange={(e) => setAccountRole(e.target.value as AdminRole)}
                      className={`${inputClass} appearance-none bg-white/5 [&>option]:bg-slate-900`}
                    >
                      <option value="moderator">Moderator</option>
                      <option value="admin" disabled={role !== 'root'}>Admin</option>
                      <option value="root" disabled={role !== 'root'}>Root</option>
                    </select>
                  </div>
                  <button
                    onClick={() => void createAccount()}
                    disabled={submittingAccount}
                    className={`${actionButtonClass} w-full bg-cyan-400 text-slate-950 hover:bg-cyan-300`}
                  >
                    {submittingAccount ? 'Creating...' : 'Create Account'}
                  </button>
                </div>
              </section>
            )}
          </aside>

          <main className={`${surfaceCardClass} overflow-hidden`}>
            {currentTab === 'reports' && (
              <div>
                <div className="flex flex-col gap-2 border-b border-white/10 px-6 py-5 sm:flex-row sm:items-center sm:justify-between">
                  <div>
                    <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Reports Queue</p>
                    <h2 className="mt-2 text-2xl font-semibold text-white">Recent reports</h2>
                  </div>
                  <label className="inline-flex items-center gap-3 rounded-full border border-white/10 bg-white/6 px-4 py-2 text-sm text-slate-300">
                    <input
                      type="checkbox"
                      checked={reports.length > 0 && selectedReports.size === reports.length}
                      onChange={(e) => {
                        setSelectedReports(e.target.checked ? new Set(reports.map((report) => report.id)) : new Set());
                      }}
                      className="h-4 w-4 rounded border-white/20 bg-transparent text-cyan-300 focus:ring-cyan-300"
                    />
                    Select all
                  </label>
                </div>

                <div className="space-y-4 p-4 sm:p-6">
                  {reports.length === 0 && (
                    <div className="rounded-[28px] border border-dashed border-white/10 bg-white/4 px-6 py-16 text-center text-slate-400">
                      No reports found
                    </div>
                  )}

                  {reports.map((report) => (
                    <React.Fragment key={report.id}>
                      <article className="rounded-[28px] border border-white/10 bg-white/5 p-5 shadow-[0_20px_60px_rgba(15,23,42,0.2)]">
                        <div className="flex flex-col gap-4 xl:flex-row xl:items-start xl:justify-between">
                          <div className="flex gap-4">
                            <input
                              type="checkbox"
                              checked={selectedReports.has(report.id)}
                              onChange={(e) => {
                                const newSet = new Set(selectedReports);
                                if (e.target.checked) {
                                  newSet.add(report.id);
                                } else {
                                  newSet.delete(report.id);
                                }
                                setSelectedReports(newSet);
                              }}
                              className="mt-1 h-4 w-4 rounded border-white/20 bg-transparent text-cyan-300 focus:ring-cyan-300"
                            />
                            <div className="space-y-4">
                              <div className="flex flex-wrap items-center gap-3">
                                <span className={`rounded-full px-3 py-1 text-xs font-semibold uppercase tracking-[0.18em] ${reportStatusClass(report.status)}`}>
                                  {report.status}
                                </span>
                                <span className="text-xs uppercase tracking-[0.18em] text-slate-500">{formatDate(report.created_at)}</span>
                              </div>
                              <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
                                <div className="min-w-0">
                                  <p className="text-[11px] uppercase tracking-[0.18em] text-slate-500">Report ID</p>
                                  <p className="mt-1 break-all text-sm font-medium text-slate-200">{formatShort(report.id, 16)}</p>
                                </div>
                                <div className="min-w-0">
                                  <p className="text-[11px] uppercase tracking-[0.18em] text-slate-500">Reported Session</p>
                                  <p className="mt-1 break-all text-sm font-medium text-slate-200">{formatShort(report.reported_session_id, 16)}</p>
                                </div>
                                <div className="min-w-0">
                                  <p className="text-[11px] uppercase tracking-[0.18em] text-slate-500">Reporter IP</p>
                                  <p className="mt-1 break-all text-sm font-medium text-slate-200">{report.reporter_ip || 'N/A'}</p>
                                </div>
                                <div className="min-w-0">
                                  <p className="text-[11px] uppercase tracking-[0.18em] text-slate-500">Reported IP</p>
                                  <p className="mt-1 break-all text-sm font-medium text-slate-200">{report.reported_ip || 'N/A'}</p>
                                </div>
                              </div>
                              <div>
                                <p className="text-[11px] uppercase tracking-[0.18em] text-slate-500">Reason</p>
                                <p className="mt-1 text-sm text-slate-200">{report.reason}</p>
                              </div>
                            </div>
                          </div>

                          <div className="flex flex-wrap gap-2 xl:max-w-sm xl:justify-end">
                            {report.chat_log && report.chat_log.length > 0 && (
                              <button
                                onClick={() => setExpandedReport(expandedReport === report.id ? null : report.id)}
                                className={`${actionButtonClass} bg-cyan-300/12 text-cyan-100 hover:bg-cyan-300/20`}
                              >
                                {expandedReport === report.id ? 'Hide Transcript' : `Transcript (${report.chat_log.length})`}
                              </button>
                            )}
                            <div className="flex flex-col gap-2 min-w-[140px]">
                              {canCreateBans && (
                                <button
                                  onClick={() =>
                                    openBanModal({
                                      sessionId: report.reported_session_id,
                                      ip: report.reported_ip,
                                      target: 'session',
                                      reason: report.reason,
                                    })
                                  }
                                  className={`${actionButtonClass} bg-rose-400/15 text-rose-100 hover:bg-rose-400/25`}
                                >
                                  Ban
                                </button>
                              )}
                              {report.status === 'pending' && (
                                <>
                                  <button
                                    onClick={() => updateReportStatus(report.id, 'rejected')}
                                    className={`${actionButtonClass} bg-slate-400/15 text-slate-200 hover:bg-slate-400/25`}
                                  >
                                    Reject
                                  </button>
                                  <button
                                    onClick={() => updateReportStatus(report.id, 'approved')}
                                    className={`${actionButtonClass} bg-emerald-400/15 text-emerald-100 hover:bg-emerald-400/25`}
                                  >
                                    Approve
                                  </button>
                                </>
                              )}
                            </div>
                          </div>
                        </div>
                      </article>

                      {expandedReport === report.id && report.chat_log && report.chat_log.length > 0 && (
                        <div className="rounded-[28px] border border-white/10 bg-slate-950/70 p-5">
                          <div className="mb-4 text-xs font-semibold uppercase tracking-[0.22em] text-slate-400">
                            Chat Transcript
                          </div>
                          <div className="max-h-80 space-y-2 overflow-y-auto pr-1">
                            {report.chat_log.map((msg) => (
                              <div key={msg.id} className={`flex ${msg.sender === 'me' ? 'justify-end' : 'justify-start'}`}>
                                <span
                                  className={`max-w-[85%] rounded-full px-4 py-2 text-sm break-words ${
                                    msg.sender === 'me'
                                      ? 'bg-cyan-300/15 text-cyan-100'
                                      : 'bg-white/8 text-slate-200'
                                  }`}
                                >
                                  <span className="mr-1 font-semibold">{msg.sender === 'me' ? 'Reporter:' : 'Reported:'}</span>
                                  {DOMPurify.sanitize(msg.text)}
                                </span>
                              </div>
                            ))}
                          </div>
                        </div>
                      )}
                    </React.Fragment>
                  ))}
                </div>
              </div>
            )}

            {currentTab === 'bans' && (
              <div>
                <div className="flex flex-col gap-2 border-b border-white/10 px-6 py-5 sm:flex-row sm:items-center sm:justify-between">
                  <div>
                    <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Ban Registry</p>
                    <h2 className="mt-2 text-2xl font-semibold text-white">Current and historical bans</h2>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <button onClick={() => setBanFilter('active')} className={filterButtonClass(banFilter === 'active')}>
                      Active
                    </button>
                    <button onClick={() => setBanFilter('inactive')} className={filterButtonClass(banFilter === 'inactive')}>
                      Inactive
                    </button>
                    <button onClick={() => setBanFilter('all')} className={filterButtonClass(banFilter === 'all')}>
                      All
                    </button>
                  </div>
                </div>

                <div className="space-y-4 p-4 sm:p-6">
                  <div className="flex flex-col gap-2">
                    <label className="text-xs uppercase tracking-[0.2em] text-slate-400">Search IP Address</label>
                    <input
                      type="text"
                      value={banSearch}
                      onChange={(e) => setBanSearch(e.target.value)}
                      className={`${inputClass} max-w-md`}
                      placeholder="Search bans by IP"
                      maxLength={64}
                    />
                  </div>

                  {bans.length === 0 && (
                    <div className="rounded-[28px] border border-dashed border-white/10 bg-white/4 px-6 py-16 text-center text-slate-400">
                      No bans found for this filter
                    </div>
                  )}

                  {permanentBans.length > 0 && (
                    <section className="space-y-4">
                      <div className="flex items-center justify-between rounded-[24px] border border-white/10 bg-white/4 px-5 py-4">
                        <div>
                          <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Permanent Bans</p>
                          <h3 className="mt-2 text-lg font-semibold text-white">Manual removal required</h3>
                        </div>
                        <div className="rounded-full border border-white/10 bg-white/6 px-3 py-1 text-sm font-semibold text-slate-200">
                          {permanentBans.length}
                        </div>
                      </div>
                      {permanentBans.map(renderBanCard)}
                    </section>
                  )}

                  {temporaryBans.length > 0 && (
                    <section className="space-y-4">
                      <div className="flex items-center justify-between rounded-[24px] border border-white/10 bg-white/4 px-5 py-4">
                        <div>
                          <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Temporary Bans</p>
                          <h3 className="mt-2 text-lg font-semibold text-white">Expire automatically</h3>
                        </div>
                        <div className="rounded-full border border-white/10 bg-white/6 px-3 py-1 text-sm font-semibold text-slate-200">
                          {temporaryBans.length}
                        </div>
                      </div>
                      {temporaryBans.map(renderBanCard)}
                    </section>
                  )}
                </div>
              </div>
            )}

            {currentTab === 'accounts' && canManageAccounts && (
              <div>
                <div className="flex flex-col gap-2 border-b border-white/10 px-6 py-5 sm:flex-row sm:items-center sm:justify-between">
                  <div>
                    <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Admin Accounts</p>
                    <h2 className="mt-2 text-2xl font-semibold text-white">Moderation operators</h2>
                  </div>
                </div>

                <div className="space-y-4 p-4 sm:p-6">
                  {accounts.length === 0 && (
                    <div className="rounded-[28px] border border-dashed border-white/10 bg-white/4 px-6 py-16 text-center text-slate-400">
                      No admin accounts found
                    </div>
                  )}

                  {accounts.map((account) => (
                    <article key={account.id} className="rounded-[28px] border border-white/10 bg-white/5 p-5 shadow-[0_20px_60px_rgba(15,23,42,0.2)]">
                      <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
                        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
                          <div>
                            <p className="text-[11px] uppercase tracking-[0.18em] text-slate-500">Username</p>
                            <p className="mt-1 break-all text-sm font-medium text-slate-200">{account.username}</p>
                          </div>
                          <div>
                            <p className="text-[11px] uppercase tracking-[0.18em] text-slate-500">Role</p>
                            <p className="mt-1 text-sm font-medium text-slate-200">{account.role}</p>
                          </div>
                          <div>
                            <p className="text-[11px] uppercase tracking-[0.18em] text-slate-500">Created By</p>
                            <p className="mt-1 text-sm font-medium text-slate-200">{account.created_by_username || 'system'}</p>
                          </div>
                          <div>
                            <p className="text-[11px] uppercase tracking-[0.18em] text-slate-500">Created At</p>
                            <p className="mt-1 text-sm font-medium text-slate-200">{formatDate(account.created_at)}</p>
                          </div>
                        </div>

                        <div className="flex flex-wrap gap-2">
                          {account.username !== currentAdminUsername && (
                            <button
                              onClick={() => void deleteAccount(account.username)}
                              disabled={role !== 'root' && (account.role === 'admin' || account.role === 'root')}
                              className={`${actionButtonClass} bg-rose-400/15 text-rose-100 hover:bg-rose-400/25`}
                            >
                              Delete
                            </button>
                          )}
                        </div>
                      </div>
                    </article>
                  ))}
                </div>
              </div>
            )}
          </main>
        </div>
      </div>

      {banModal.open && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/70 p-4 backdrop-blur-sm">
          <div className="w-full max-w-lg rounded-[30px] border border-white/10 bg-slate-950/95 p-6 shadow-[0_30px_120px_rgba(8,15,30,0.75)]">
            <div className="mb-6 flex items-start justify-between gap-4">
              <div>
                <p className="text-xs uppercase tracking-[0.2em] text-slate-400">Ban User</p>
                <h2 className="mt-2 text-2xl font-semibold text-white">Choose ban type</h2>
                <p className="mt-2 text-sm text-slate-400">
                  Permanent bans stay active until you manually unban them. Temporary bans expire automatically after the duration you choose.
                </p>
              </div>
              <button
                onClick={closeBanModal}
                className="rounded-full border border-white/10 bg-white/5 px-3 py-1.5 text-xs font-semibold uppercase tracking-[0.18em] text-slate-300 transition hover:bg-white/10"
              >
                Close
              </button>
            </div>

            {(banModal.sessionId || banModal.ip) && (
              <div className="mb-5 rounded-2xl border border-white/10 bg-white/5 p-4 text-sm text-slate-300">
                {banModal.sessionId && <p>Session: <span className="break-all text-white">{banModal.sessionId}</span></p>}
                {banModal.ip && <p className={banModal.sessionId ? 'mt-2' : ''}>IP: <span className="break-all text-white">{banModal.ip}</span></p>}
              </div>
            )}

            {banModal.sessionId && banModal.ip && (
              <div className="mb-5">
                <p className="mb-2 text-sm font-medium text-slate-300">Ban target</p>
                <div className="flex gap-2">
                  <button
                    onClick={() => setBanModal((current) => ({ ...current, target: 'session' }))}
                    className={tabButtonClass(banModal.target === 'session')}
                  >
                    Session Ban
                  </button>
                  <button
                    onClick={() => setBanModal((current) => ({ ...current, target: 'ip' }))}
                    className={tabButtonClass(banModal.target === 'ip')}
                  >
                    IP Ban
                  </button>
                </div>
              </div>
            )}

            <div className="mb-5">
              <p className="mb-2 text-sm font-medium text-slate-300">Ban length</p>
              <div className="flex gap-2">
                <button
                  onClick={() => setBanModal((current) => ({ ...current, mode: 'permanent' }))}
                  className={tabButtonClass(banModal.mode === 'permanent')}
                >
                  Permanent Ban
                </button>
                <button
                  onClick={() => setBanModal((current) => ({ ...current, mode: 'temporary' }))}
                  className={tabButtonClass(banModal.mode === 'temporary')}
                >
                  Temporary Ban
                </button>
              </div>
            </div>

            {banModal.mode === 'temporary' && (
              <div className="mb-5 grid gap-3 sm:grid-cols-[1fr_auto]">
                <div>
                  <label className="mb-2 block text-sm font-medium text-slate-300">Duration</label>
                  <input
                    type="number"
                    min="1"
                    value={banModal.durationValue}
                    onChange={(e) => setBanModal((current) => ({ ...current, durationValue: e.target.value }))}
                    className={inputClass}
                  />
                </div>
                <div>
                  <label className="mb-2 block text-sm font-medium text-slate-300">Unit</label>
                  <select
                    value={banModal.durationUnit}
                    onChange={(e) =>
                      setBanModal((current) => ({
                        ...current,
                        durationUnit: e.target.value as 'hours' | 'days',
                      }))
                    }
                    className={inputClass}
                  >
                    <option value="hours">Hours</option>
                    <option value="days">Days</option>
                  </select>
                </div>
              </div>
            )}

            <div className="mb-6">
              <label className="mb-2 block text-sm font-medium text-slate-300">Reason</label>
              <textarea
                value={banModal.reason}
                onChange={(e) => setBanModal((current) => ({ ...current, reason: e.target.value }))}
                rows={3}
                className={inputClass}
                placeholder="Document why this ban is being applied"
              />
            </div>

            <div className="flex gap-3">
              <button
                onClick={submitBanModal}
                disabled={submittingBan}
                className={`${actionButtonClass} flex-1 bg-rose-500 text-white hover:bg-rose-400`}
              >
                {submittingBan ? 'Applying...' : 'Confirm Ban'}
              </button>
              <button
                onClick={closeBanModal}
                disabled={submittingBan}
                className={`${actionButtonClass} flex-1 bg-white/8 text-slate-200 hover:bg-white/12`}
              >
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
