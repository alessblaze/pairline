/// <reference types="vite/client" />
/**
 * Pairline - Open Source Video Chat and Matchmaking
 * Enhanced Admin Dashboard
 */

import React, { useDeferredValue, useEffect, useEffectEvent, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import DOMPurify from 'dompurify';
import { motion, AnimatePresence } from 'motion/react';
import { useTheme } from '../context/ThemeContext';
import { AuthLoadingScreen } from './admin-panel/AuthLoadingScreen';
import { LoginScreen } from './admin-panel/LoginScreen';
import {
  actionButtonClass,
  compactSelectClass,
  filterButtonClass,
  inputClass,
  metricCardClass,
  segmentedToggleButtonClass,
  surfaceCardClass,
  tabButtonClass,
} from './admin-panel/styles';
import type { AdminPanelMockState } from './admin-panel/storyTypes';
import type { AdminPanelProps, BanModalState, RawReport } from './admin-panel/types';
import {
  buildAdminHeaders,
  buildExpiryDate,
  clearAdminSession,
  formatBytes,
  formatDate,
  formatLatency,
  goMemorySummary,
  healthStatusClass,
  persistAdminSession,
  phoenixMemorySummary,
  reportStatusClass,
  topRedisCommandStats,
} from './admin-panel/utils';
import {
  Shield,
  AlertTriangle,
  Ban as BanIcon,
  Users,
  LogOut,
  RefreshCw,
  CheckCircle2,
  XCircle,
  MessageSquare,
  Search,
  Plus,
  Trash2,
  Clock,
  Globe,
  UserPlus,
  Eye,
  EyeOff,
  Activity,
  Server,
  Database,
  Network,
  Menu,
  Moon,
  Sun,
  X
} from 'lucide-react';
import type { AdminAccount, AdminRole, Ban, BannedWord, CreateBanRequest, InfraHealthResponse, LoginResponse, Report } from '../types';

interface AdminPanelRuntimeProps extends AdminPanelProps {
  __mockState?: AdminPanelMockState;
}

export function AdminPanelRuntime({ loginRoute = '/', __mockState }: AdminPanelRuntimeProps) {
  const navigate = useNavigate();
  const { isDark, toggleTheme } = useTheme();
  const [isAuthenticated, setIsAuthenticated] = useState(__mockState?.isAuthenticated ?? false);
  const [authReady, setAuthReady] = useState(__mockState?.authReady ?? false);
  const [role, setRole] = useState<AdminRole | null>(__mockState?.role ?? null);
  const [currentAdminUsername, setCurrentAdminUsername] = useState(__mockState?.currentAdminUsername ?? '');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [showLoginPassword, setShowLoginPassword] = useState(false);
  const [reports, setReports] = useState<Report[]>(__mockState?.reports ?? []);
  const [accounts, setAccounts] = useState<AdminAccount[]>(__mockState?.accounts ?? []);
  const [infraHealth, setInfraHealth] = useState<InfraHealthResponse | null>(__mockState?.infraHealth ?? null);
  const [selectedReports, setSelectedReports] = useState<Set<string>>(new Set());
  const [expandedReport, setExpandedReport] = useState<string | null>(__mockState?.expandedReport ?? null);
  const [viewingDescription, setViewingDescription] = useState<string | null>(__mockState?.viewingDescription ?? null);
  const [isRedisModalOpen, setIsRedisModalOpen] = useState(__mockState?.isRedisModalOpen ?? false);
  const [isUserMenuOpen, setIsUserMenuOpen] = useState(__mockState?.isUserMenuOpen ?? false);
  const [isMobileMenuOpen, setIsMobileMenuOpen] = useState(__mockState?.isMobileMenuOpen ?? false);
  const [bans, setBans] = useState<Ban[]>(__mockState?.bans ?? []);
  const [bannedWords, setBannedWords] = useState<BannedWord[]>(__mockState?.bannedWords ?? []);
  const [currentTab, setCurrentTab] = useState<'reports' | 'bans' | 'bannedWords' | 'accounts' | 'infra'>(__mockState?.currentTab ?? 'reports');
  const mainContentRef = useRef<HTMLElement>(null);

  useEffect(() => {
    if (mainContentRef.current && typeof mainContentRef.current.scrollTo === 'function') {
      mainContentRef.current.scrollTo(0, 0);
    }
  }, [currentTab]);

  const [reportStatusFilter, setReportStatusFilter] = useState<'pending' | 'decided' | 'all'>(__mockState?.reportStatusFilter ?? 'pending');
  const [reportLimit, setReportLimit] = useState<string>('10');
  const [serverReportMetrics, setServerReportMetrics] = useState(__mockState?.serverReportMetrics ?? { pending: 0, approved: 0, rejected: 0 });
  const [banFilter, setBanFilter] = useState<'all' | 'active' | 'inactive'>(__mockState?.banFilter ?? 'active');
  const [banLimit, setBanLimit] = useState<string>('10');
  const [banSearch, setBanSearch] = useState('');
  const [serverBanMetrics, setServerBanMetrics] = useState(__mockState?.serverBanMetrics ?? { active: 0, inactive: 0, total: 0 });
  const [manualBanSessionId, setManualBanSessionId] = useState('');
  const [manualBanIP, setManualBanIP] = useState('');
  const [manualBanReason, setManualBanReason] = useState('');
  const [bannedWordInput, setBannedWordInput] = useState('');
  const [bannedWordSearch, setBannedWordSearch] = useState('');
  const [bannedWordLimit, setBannedWordLimit] = useState<string>('25');
  const [bannedWordTotal, setBannedWordTotal] = useState(__mockState?.bannedWords?.length ?? 0);
  const [accountUsername, setAccountUsername] = useState('');
  const [accountPassword, setAccountPassword] = useState('');
  const [showCreateAccountPassword, setShowCreateAccountPassword] = useState(__mockState?.showCreateAccountPassword ?? false);
  const [accountRole, setAccountRole] = useState<AdminRole>('moderator');
  const [accountSearch, setAccountSearch] = useState('');
  const [accountLimit, setAccountLimit] = useState<string>('25');
  const [accountPage, setAccountPage] = useState(1);
  const [accountTotal, setAccountTotal] = useState(0);
  const [submittingAccount, setSubmittingAccount] = useState(false);
  const [submittingBan, setSubmittingBan] = useState(false);
  const [banModal, setBanModal] = useState<BanModalState>(
    __mockState?.banModal ?? {
      open: false,
      sessionId: '',
      ip: '',
      sourceReportId: '',
      target: 'session',
      reason: '',
      mode: 'permanent',
      durationValue: '24',
      durationUnit: 'hours',
    }
  );

  const canCreateBans = role === 'moderator' || role === 'admin' || role === 'root';
  const canManageBans = role === 'admin' || role === 'root';
  const canManageBannedWords = role === 'moderator' || role === 'admin' || role === 'root';
  const canManageAccounts = role === 'admin' || role === 'root';
  const canViewInfraHealth = role === 'root';
  const deferredBanSearch = useDeferredValue(banSearch);
  const deferredBannedWordSearch = useDeferredValue(bannedWordSearch);
  const deferredAccountSearch = useDeferredValue(accountSearch);
  const accountPageSize = accountLimit === 'all' ? Math.max(accountTotal, accounts.length, 1) : Number(accountLimit);
  const accountTotalPages = Math.max(1, Math.ceil(accountTotal / accountPageSize));
  const selectableVisibleReports = reports.filter((report) => report.status === 'pending');
  const selectedPendingReportIds = selectableVisibleReports
    .filter((report) => selectedReports.has(report.id))
    .map((report) => report.id);
  const selectedPendingReportsCount = selectedPendingReportIds.length;
  const allVisibleReportsSelected =
    selectableVisibleReports.length > 0 &&
    selectableVisibleReports.every((report) => selectedReports.has(report.id));

  // Authentication Logic
  useEffect(() => {
    if (__mockState) return;
    const bootstrapAuth = async () => {
      const rememberedAuth = localStorage.getItem('admin_auth') === 'true';
      const csrfToken = sessionStorage.getItem('admin_csrf');
      if (!rememberedAuth || !csrfToken) {
        clearAdminSession();
        setIsAuthenticated(false);
        setRole(null);
        setCurrentAdminUsername('');
        setAuthReady(true);
        return;
      }
      const refreshed = await refreshSession();
      if (!refreshed) {
        clearAdminSession();
        setIsAuthenticated(false);
        setRole(null);
        setCurrentAdminUsername('');
      }
      setAuthReady(true);
    };
    void bootstrapAuth();
  }, [__mockState]);

  const refreshSession = async () => {
    const csrfToken = window.sessionStorage.getItem('admin_csrf') || '';
    try {
      const response = await fetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/refresh`, {
        method: 'POST',
        headers: csrfToken ? { 'X-CSRF-Token': csrfToken } : {},
        credentials: 'include',
      });
      if (!response.ok) return false;
      const csrfTokenHeader = response.headers.get('X-CSRF-Token');
      if (csrfTokenHeader) sessionStorage.setItem('admin_csrf', csrfTokenHeader);
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
    if (response.status !== 401 || !retryOnUnauthorized) return response;
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
        body: JSON.stringify({ username: username.trim(), password: password.trim() }),
        credentials: 'include',
      });
      if (response.ok) {
        const csrfTokenHeader = response.headers.get('X-CSRF-Token');
        if (csrfTokenHeader) sessionStorage.setItem('admin_csrf', csrfTokenHeader);
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
    setInfraHealth(null);
    setIsRedisModalOpen(false);
    setSelectedReports(new Set());
    navigate(loginRoute);
  };

  // Data Fetching
  const fetchReports = async () => {
    try {
      const response = await adminFetch(
        `${import.meta.env.VITE_API_URL}/api/v1/admin/reports?status=${reportStatusFilter}&limit=${reportLimit}`
      );
      if (response.status === 401) return logout();
      if (response.ok) {
        const data: {
          reports?: RawReport[];
          metrics?: Partial<Record<'pending' | 'approved' | 'rejected', number>>;
        } = await response.json();
        if (data.metrics) {
          setServerReportMetrics({
            pending: data.metrics.pending || 0,
            approved: data.metrics.approved || 0,
            rejected: data.metrics.rejected || 0,
          });
        }
        const normalized = (data.reports || []).map((r) => ({
          ...r,
          chat_log: (() => {
            if (typeof r.chat_log !== 'string') return r.chat_log || [];
            try {
              return JSON.parse(r.chat_log);
            } catch (error) {
              console.warn('Failed to parse report chat_log', r.id, error);
              return [];
            }
          })(),
        }));
        setReports(normalized);
        setSelectedReports((current) => {
          const visibleIds = new Set(
            normalized
              .filter((report: Report) => report.status === 'pending')
              .map((report: Report) => report.id)
          );
          return new Set([...current].filter((id) => visibleIds.has(id)));
        });
      }
    } catch (error) {
      console.error('Failed to fetch reports:', error);
    }
  };

  const fetchBans = async () => {
    try {
      const params = new URLSearchParams({ status: banFilter, limit: banLimit });
      if (deferredBanSearch.trim()) params.set('q', deferredBanSearch.trim());
      const response = await adminFetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/bans?${params.toString()}`);
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

  const fetchBannedWords = async () => {
    if (!canManageBannedWords) return;
    try {
      const params = new URLSearchParams({ limit: bannedWordLimit });
      if (deferredBannedWordSearch.trim()) params.set('q', deferredBannedWordSearch.trim());
      const response = await adminFetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/banned-words?${params.toString()}`);
      if (response.status === 401) return logout();
      if (response.ok) {
        const data = await response.json();
        setBannedWords(data.words || []);
        setBannedWordTotal(Number(data.total || 0));
      }
    } catch (error) {
      console.error('Failed to fetch banned words:', error);
    }
  };

  const fetchAccounts = async () => {
    if (!canManageAccounts) return;
    try {
      const params = new URLSearchParams({ page: String(accountPage), limit: accountLimit });
      if (deferredAccountSearch.trim()) params.set('q', deferredAccountSearch.trim());
      const response = await adminFetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/accounts?${params.toString()}`);
      if (response.status === 401) return logout();
      if (response.ok) {
        const data = await response.json();
        setAccounts(data.accounts || []);
        setAccountTotal(Number(data.total || 0));
      }
    } catch (error) {
      console.error('Failed to fetch admin accounts:', error);
    }
  };

  const fetchInfraHealth = async () => {
    if (!canViewInfraHealth) return;
    try {
      const response = await adminFetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/infra/health`);
      if (response.status === 401) return logout();
      if (response.ok) {
        const data: InfraHealthResponse = await response.json();
        setInfraHealth(data);
      } else if (response.status === 403) {
        setInfraHealth(null);
      }
    } catch (error) {
      console.error('Failed to fetch infra health:', error);
    }
  };

  // Actions
  const updateReportStatus = async (reportId: string, newStatus: 'approved' | 'rejected') => {
    try {
      const response = await adminFetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/reports/${reportId}`, {
        method: 'PUT',
        body: JSON.stringify({ status: newStatus }),
      });
      if (response.status === 401) return logout();
      if (response.ok) {
        fetchReports();
      } else {
        const data = await response.json().catch(() => ({}));
        alert(data.error || 'Failed to update report');
      }
    } catch (error) {
      console.error('Failed to update report:', error);
    }
  };

  const handleSingleReportAction = (
    event: React.MouseEvent<HTMLButtonElement>,
    reportId: string,
    newStatus: 'approved' | 'rejected'
  ) => {
    event.preventDefault();
    event.stopPropagation();
    setSelectedReports((current) => {
      if (!current.has(reportId)) return current;
      const next = new Set(current);
      next.delete(reportId);
      return next;
    });
    void updateReportStatus(reportId, newStatus);
  };

  const handleBanModalSubmit = (event: React.MouseEvent<HTMLButtonElement>) => {
    event.preventDefault();
    event.stopPropagation();
    void submitBanModal();
  };

  const toggleSelectAllVisibleReports = () => {
    setSelectedReports((current) => {
      if (selectableVisibleReports.length === 0) return current;
      if (selectableVisibleReports.every((report) => current.has(report.id))) {
        const next = new Set(current);
        selectableVisibleReports.forEach((report) => next.delete(report.id));
        return next;
      }
      const next = new Set(current);
      selectableVisibleReports.forEach((report) => next.add(report.id));
      return next;
    });
  };

  const createBan = async (request: CreateBanRequest) => {
    if (!canCreateBans) return false;
    try {
      const response = await adminFetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/ban`, {
        method: 'POST',
        body: JSON.stringify(request),
      });
      if (response.status === 401) { logout(); return false; }
      if (response.ok) {
        fetchBans();
        fetchReports();
        return true;
      }
      const data = await response.json().catch(() => ({}));
      alert(data.error || 'Failed to create ban');
    } catch (error) {
      console.error('Failed to create ban:', error);
      alert('Failed to create ban');
    }
    return false;
  };

  const createBannedWord = async () => {
    if (!canManageBannedWords) return;
    const word = bannedWordInput.trim();
    if (!word) {
      alert('Please enter a banned word or phrase');
      return;
    }

    try {
      const response = await adminFetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/banned-words`, {
        method: 'POST',
        body: JSON.stringify({ word }),
      });
      if (response.status === 401) return logout();
      if (response.ok) {
        setBannedWordInput('');
        fetchBannedWords();
        return;
      }
      const data = await response.json().catch(() => ({}));
      alert(data.error || 'Failed to create banned word');
    } catch (error) {
      console.error('Failed to create banned word:', error);
      alert('Failed to create banned word');
    }
  };

  const deleteBannedWord = async (id: string) => {
    if (!canManageBannedWords) return;
    try {
      const response = await adminFetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/banned-words/${id}`, {
        method: 'DELETE',
      });
      if (response.status === 401) return logout();
      if (response.ok) {
        fetchBannedWords();
        return;
      }
      const data = await response.json().catch(() => ({}));
      alert(data.error || 'Failed to delete banned word');
    } catch (error) {
      console.error('Failed to delete banned word:', error);
      alert('Failed to delete banned word');
    }
  };

  const openBanModal = ({
    sessionId = '',
    ip = '',
    sourceReportId = '',
    target,
    reason = '',
    clearManualInputsOnSubmit = false,
  }: {
    sessionId?: string;
    ip?: string;
    sourceReportId?: string;
    target?: 'session' | 'ip';
    reason?: string;
    clearManualInputsOnSubmit?: boolean;
  }) => {
    const nextTarget = target || (sessionId ? 'session' : 'ip');
    setBanModal({
      open: true,
      sessionId,
      ip,
      sourceReportId,
      target: nextTarget,
      reason,
      mode: 'permanent',
      durationValue: '24',
      durationUnit: 'hours',
      clearManualInputsOnSubmit,
    });
  };

  const unban = async (banId: string) => {
    if (!canManageBans) return;
    try {
      const response = await adminFetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/ban/${banId}`, {
        method: 'DELETE',
      });
      if (response.status === 401) return logout();
      if (response.ok) {
        fetchBans();
        fetchReports();
        return;
      }
      const data = await response.json().catch(() => ({}));
      alert(data.error || 'Failed to unban');
    } catch (error) {
      console.error('Failed to unban:', error);
      alert('Failed to unban');
    }
  };

  const createAccount = async () => {
    if (!canManageAccounts) return;
    setSubmittingAccount(true);
    try {
      const response = await adminFetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/accounts`, {
        method: 'POST',
        body: JSON.stringify({ username: accountUsername.trim(), password: accountPassword.trim(), role: accountRole }),
      });
      if (response.status === 401) {
        logout();
        return;
      }
      if (response.ok) {
        setAccountUsername('');
        setAccountPassword('');
        setAccountPage(1);
        fetchAccounts();
        return;
      }
      const data = await response.json().catch(() => ({}));
      alert(data.error || 'Failed to create account');
    } catch (error) {
      console.error('Failed to create account:', error);
      alert('Failed to create account');
    } finally {
      setSubmittingAccount(false);
    }
  };

  const deleteAccount = async (targetUsername: string) => {
    if (!canManageAccounts || !window.confirm(`Delete ${targetUsername}?`)) return;
    try {
      const response = await adminFetch(`${import.meta.env.VITE_API_URL}/api/v1/admin/accounts/${encodeURIComponent(targetUsername)}`, {
        method: 'DELETE',
      });
      if (response.status === 401) return logout();
      if (response.ok) {
        fetchAccounts();
        return;
      }
      const data = await response.json().catch(() => ({}));
      alert(data.error || 'Failed to delete account');
    } catch (error) {
      console.error('Failed to delete account:', error);
      alert('Failed to delete account');
    }
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
    if (banModal.sourceReportId) {
      request.report_id = banModal.sourceReportId;
    }
    if (banModal.target === 'session') {
      request.session_id = banModal.sessionId.trim();
      if (banModal.ip) request.ip = banModal.ip.trim();
    } else {
      request.ip = banModal.ip.trim();
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
    if (success) {
      if (banModal.clearManualInputsOnSubmit) {
        setManualBanSessionId('');
        setManualBanIP('');
        setManualBanReason('');
      }
      closeBanModal();
    }
  };

  const syncReports = useEffectEvent(() => {
    void fetchReports();
  });

  const syncBans = useEffectEvent(() => {
    void fetchBans();
  });

  const syncBannedWords = useEffectEvent(() => {
    void fetchBannedWords();
  });

  const syncAccounts = useEffectEvent(() => {
    void fetchAccounts();
  });

  const syncInfraHealth = useEffectEvent(() => {
    void fetchInfraHealth();
  });

  // Effects for data sync
  useEffect(() => {
    setAccountPage(1);
  }, [deferredAccountSearch]);

  useEffect(() => {
    setAccountPage(1);
  }, [accountLimit]);

  useEffect(() => { if (authReady && isAuthenticated && !__mockState) syncReports(); }, [authReady, isAuthenticated, reportStatusFilter, reportLimit, __mockState]);
  useEffect(() => { if (authReady && isAuthenticated && !__mockState) syncBans(); }, [authReady, isAuthenticated, banFilter, banLimit, deferredBanSearch, __mockState]);
  useEffect(() => { if (authReady && isAuthenticated && canManageBannedWords && !__mockState) syncBannedWords(); }, [authReady, isAuthenticated, canManageBannedWords, bannedWordLimit, deferredBannedWordSearch, __mockState]);
  useEffect(() => { if (authReady && isAuthenticated && canManageAccounts && !__mockState) syncAccounts(); }, [authReady, isAuthenticated, canManageAccounts, accountPage, accountLimit, deferredAccountSearch, __mockState]);
  useEffect(() => { if (authReady && isAuthenticated && canViewInfraHealth && !__mockState) syncInfraHealth(); }, [authReady, isAuthenticated, canViewInfraHealth, __mockState]);

  if (!authReady) {
    return <AuthLoadingScreen />;
  }

  if (!isAuthenticated) {
    return (
      <LoginScreen
        username={username}
        password={password}
        showPassword={showLoginPassword}
        onUsernameChange={setUsername}
        onPasswordChange={setPassword}
        onTogglePassword={() => setShowLoginPassword((current) => !current)}
        onSubmit={login}
      />
    );
  }

  return (
    <div className="admin-console fixed inset-0 flex h-[100dvh] w-full flex-col overflow-hidden bg-[var(--admin-bg)] text-[var(--admin-text)] transition-colors duration-300">
      {/* Background Gradients */}
      <div className="fixed inset-0 z-0">
        <div className="absolute top-0 left-0 h-[500px] w-[500px] rounded-full bg-cyan-500/5 blur-[100px]" />
        <div className="absolute bottom-0 right-0 h-[500px] w-[500px] rounded-full bg-rose-500/5 blur-[100px]" />
      </div>

      {/* Mobile Top Bar */}
      <div className="admin-sidebar relative z-40 flex h-16 shrink-0 items-center justify-between border-b px-6 backdrop-blur-xl lg:hidden">
        <div className="flex items-center gap-3">
          <div className="flex h-8 w-8 items-center justify-center rounded-none bg-[var(--admin-text)] text-[var(--admin-bg)]">
            <Shield size={16} />
          </div>
          <h2 className="text-base font-bold text-[var(--admin-text)]">Pairline</h2>
        </div>
        <button
          onClick={() => setIsMobileMenuOpen(true)}
          className="flex h-10 w-10 items-center justify-center rounded-none border border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] text-[var(--admin-text-soft)] transition-all hover:bg-[var(--admin-muted-surface-hover)] hover:text-[var(--admin-text)]"
        >
          <Menu size={20} />
        </button>
      </div>

      {/* Mobile Menu Drawer */}
      <AnimatePresence>
        {isMobileMenuOpen && (
          <>
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              onClick={() => setIsMobileMenuOpen(false)}
              className="fixed inset-0 z-[60] bg-[var(--admin-bg)]/60 backdrop-blur-sm lg:hidden"
            />
            <motion.div
              initial={{ x: '-100%' }}
              animate={{ x: 0 }}
              exit={{ x: '-100%' }}
              transition={{ type: 'spring', damping: 25, stiffness: 200 }}
              className="admin-sidebar fixed inset-y-0 left-0 z-[70] flex w-[280px] flex-col border-r p-6 shadow-2xl lg:hidden"
            >
              <div className="mb-10 flex items-center justify-between">
                <div className="flex items-center gap-3">
                  <div className="flex h-10 w-10 items-center justify-center rounded-none bg-[var(--admin-text)] text-[var(--admin-bg)]">
                    <Shield size={20} />
                  </div>
                  <div>
                    <h2 className="text-lg font-bold text-[var(--admin-text)]">Pairline</h2>
                    <p className="text-[10px] uppercase tracking-widest text-[var(--admin-text-muted)]">Admin Console</p>
                  </div>
                </div>
                <button
                  onClick={() => setIsMobileMenuOpen(false)}
                  className="flex h-10 w-10 items-center justify-center rounded-none border border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] text-[var(--admin-text-soft)] transition-all hover:bg-[var(--admin-muted-surface-hover)] hover:text-[var(--admin-text)]"
                >
                  <X size={20} />
                </button>
              </div>

              <nav className="flex-1 space-y-2 overflow-y-auto pr-2 custom-scrollbar">
                <button
                  onClick={() => {
                    setCurrentTab('reports');
                    setIsMobileMenuOpen(false);
                  }}
                  className={tabButtonClass(currentTab === 'reports')}
                >
                  <AlertTriangle size={18} />
                  Reports Queue
                  {serverReportMetrics.pending > 0 && (
                    <span className="ml-auto flex h-5 w-5 items-center justify-center rounded-full bg-rose-500 text-[10px] font-bold text-[var(--admin-text)]">
                      {serverReportMetrics.pending}
                    </span>
                  )}
                </button>
                <button
                  onClick={() => {
                    setCurrentTab('bans');
                    setIsMobileMenuOpen(false);
                  }}
                  className={tabButtonClass(currentTab === 'bans')}
                >
                  <BanIcon size={18} />
                  Ban Registry
                </button>
                {canManageBannedWords && (
                  <button
                    onClick={() => {
                      setCurrentTab('bannedWords');
                      setIsMobileMenuOpen(false);
                    }}
                    className={tabButtonClass(currentTab === 'bannedWords')}
                  >
                    <MessageSquare size={18} />
                    Banned Words
                  </button>
                )}
                {canViewInfraHealth && (
                  <button
                    onClick={() => {
                      setCurrentTab('infra');
                      setIsMobileMenuOpen(false);
                    }}
                    className={tabButtonClass(currentTab === 'infra')}
                  >
                    <Server size={18} />
                    Infra Health
                  </button>
                )}
                {canManageAccounts && (
                  <button
                    onClick={() => {
                      setCurrentTab('accounts');
                      setIsMobileMenuOpen(false);
                    }}
                    className={tabButtonClass(currentTab === 'accounts')}
                  >
                    <Users size={18} />
                    Admin Accounts
                  </button>
                )}
              </nav>

              <div className="mt-auto border-t border-[var(--admin-sidebar-border)] pt-6">
                <div className="flex flex-col gap-4">
                  <button
                    type="button"
                    onClick={toggleTheme}
                    className="flex w-full items-center gap-3 rounded-none border border-[var(--admin-input-border)] bg-[var(--admin-input-bg)] px-4 py-3 text-sm font-bold uppercase tracking-[0.14em] text-[var(--admin-text)] transition-all hover:bg-[var(--admin-input-focus-bg)]"
                  >
                    {isDark ? <Sun size={16} /> : <Moon size={16} />}
                    {isDark ? 'Light Mode' : 'Dark Mode'}
                  </button>
                  <div className="flex items-center gap-3 px-2">
                    <div className="h-10 w-10 rounded-none bg-gradient-to-br from-cyan-400 to-blue-500 shadow-lg" />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-bold text-[var(--admin-text)]">{currentAdminUsername}</p>
                      <p className="text-[10px] font-bold uppercase tracking-widest text-[var(--admin-text-muted)] leading-none mt-1">{role}</p>
                    </div>
                  </div>
                  <button
                    onClick={logout}
                    className="flex w-full items-center gap-3 rounded-none bg-rose-500/10 px-4 py-3 text-sm font-bold uppercase tracking-[0.14em] text-rose-400 transition-all hover:bg-rose-500/20 active:scale-[0.98]"
                  >
                    <LogOut size={16} />
                    Sign Out
                  </button>
                </div>
              </div>
            </motion.div>
          </>
        )}
      </AnimatePresence>

      <div className="relative z-10 flex h-full overflow-hidden">
        {/* Sidebar */}
        <aside className="admin-sidebar hidden w-72 border-r backdrop-blur-xl lg:block">
          <div className="flex h-full flex-col p-6">
            <div className="mb-10 flex items-center gap-3 px-2">
              <div className="flex h-10 w-10 items-center justify-center rounded-none bg-[var(--admin-text)] text-[var(--admin-bg)]">
                <Shield size={20} />
              </div>
              <div>
                <h2 className="text-lg font-bold text-[var(--admin-text)]">Pairline</h2>
                <p className="text-[10px] uppercase tracking-widest text-[var(--admin-text-muted)]">Admin Console</p>
              </div>
            </div>

            <nav className="flex-1 space-y-2 overflow-y-auto pr-2 custom-scrollbar">
              <button onClick={() => setCurrentTab('reports')} className={tabButtonClass(currentTab === 'reports')}>
                <AlertTriangle size={18} />
                Reports Queue
                {serverReportMetrics.pending > 0 && (
                  <span className="ml-auto flex h-5 w-5 items-center justify-center rounded-full bg-rose-500 text-[10px] font-bold text-[var(--admin-text)]">
                    {serverReportMetrics.pending}
                  </span>
                )}
              </button>
              <button onClick={() => setCurrentTab('bans')} className={tabButtonClass(currentTab === 'bans')}>
                <BanIcon size={18} />
                Ban Registry
              </button>
              {canManageBannedWords && (
                <button onClick={() => setCurrentTab('bannedWords')} className={tabButtonClass(currentTab === 'bannedWords')}>
                  <MessageSquare size={18} />
                  Banned Words
                </button>
              )}
              {canViewInfraHealth && (
                <button onClick={() => setCurrentTab('infra')} className={tabButtonClass(currentTab === 'infra')}>
                  <Server size={18} />
                  Infra Health
                </button>
              )}
              {canManageAccounts && (
                <button onClick={() => setCurrentTab('accounts')} className={tabButtonClass(currentTab === 'accounts')}>
                  <Users size={18} />
                  Admin Accounts
                </button>
              )}
            </nav>

            <div className="mt-auto relative">
              <button
                type="button"
                onClick={toggleTheme}
                className="mb-4 flex w-full items-center gap-3 rounded-none border border-[var(--admin-input-border)] bg-[var(--admin-input-bg)] px-4 py-3 text-sm font-bold uppercase tracking-[0.14em] text-[var(--admin-text)] transition-all hover:bg-[var(--admin-input-focus-bg)]"
              >
                {isDark ? <Sun size={16} /> : <Moon size={16} />}
                {isDark ? 'Light Mode' : 'Dark Mode'}
              </button>
              <AnimatePresence>
                {isUserMenuOpen && (
                  <motion.div
                    initial={{ opacity: 0, scale: 0.95, y: 10 }}
                    animate={{ opacity: 1, scale: 1, y: 0 }}
                    exit={{ opacity: 0, scale: 0.95, y: 10 }}
                    className="absolute bottom-full left-0 mb-4 w-full rounded-none border border-[var(--admin-outline-soft)] bg-[var(--admin-surface-bg)] p-2 shadow-2xl backdrop-blur-xl"
                  >
                    <div className="mb-2 border-b border-[var(--admin-sidebar-border)] px-4 py-3">
                      <p className="truncate text-xs font-bold text-[var(--admin-text)] tracking-wide uppercase">{currentAdminUsername}</p>
                      <p className="mt-0.5 text-[10px] font-bold uppercase tracking-widest text-[var(--admin-text-muted)]">{role}</p>
                    </div>
                    <button
                      onClick={(e) => {
                        e.stopPropagation();
                        logout();
                      }}
                      className="flex w-full items-center gap-3 rounded-none px-4 py-3 text-sm font-bold uppercase tracking-[0.14em] text-rose-400 transition-all hover:bg-rose-500/10 active:scale-[0.98]"
                    >
                      <LogOut size={16} />
                      Sign Out
                    </button>
                  </motion.div>
                )}
              </AnimatePresence>

              <button
                onClick={() => setIsUserMenuOpen(!isUserMenuOpen)}
                className={`group flex w-full items-center gap-3 rounded-none border p-4 transition-all duration-300 ${isUserMenuOpen
                  ? 'border-electric-cyan/40 bg-electric-cyan/10 shadow-[0_0_15px_rgba(34,211,238,0.1)]'
                  : 'border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] hover:border-[var(--admin-outline-strong)] hover:bg-[var(--admin-muted-surface-hover)]'
                  }`}
              >
                <div className="relative">
                  <div className="h-10 w-10 rounded-none bg-gradient-to-br from-cyan-400 to-blue-500 shadow-lg" />
                  <div className={`absolute -bottom-0.5 -right-0.5 h-3.5 w-3.5 rounded-none border-2 border-[var(--admin-bg)] bg-emerald-500 transition-transform ${isUserMenuOpen ? 'scale-110' : 'scale-100'}`} />
                </div>
                <div className="min-w-0 flex-1 text-left">
                  <p className="truncate text-sm font-bold text-[var(--admin-text)]">{currentAdminUsername}</p>
                  <p className="text-[10px] font-bold uppercase tracking-widest text-[var(--admin-text-muted)] leading-none mt-1">{role}</p>
                </div>
                <div className={`text-[var(--admin-icon-muted)] transition-transform duration-300 ${isUserMenuOpen ? 'rotate-180' : ''}`}>
                  <RefreshCw size={14} className={isUserMenuOpen ? 'animate-spin-slow' : ''} />
                </div>
              </button>
            </div>
          </div>
        </aside>

        {/* Main Content */}
        <main ref={mainContentRef} className="flex-1 overflow-y-auto">
          <div className="mx-auto max-w-6xl p-6 lg:p-10">
            {/* Header */}
            <header className="mb-10 flex flex-col gap-6 sm:flex-row sm:items-center sm:justify-between">
              <div>
                <h1 className="text-3xl font-bold text-[var(--admin-text)]">
                  {currentTab === 'reports'
                    ? 'Reports Queue'
                    : currentTab === 'bans'
                      ? 'Ban Registry'
                      : currentTab === 'bannedWords'
                        ? 'Banned Words'
                      : currentTab === 'infra'
                        ? 'Infra Health'
                        : 'Admin Accounts'}
                </h1>
                <p className="mt-1 text-[var(--admin-text-soft)]">
                  {currentTab === 'reports'
                    ? 'Review and act on user reports in real-time.'
                    : currentTab === 'bans'
                      ? 'Manage active and historical user bans.'
                      : currentTab === 'bannedWords'
                        ? 'Block delivery of messages containing restricted words or phrases.'
                      : currentTab === 'infra'
                        ? 'Inspect cluster topology, service health, data stores, and observability lanes.'
                        : 'Manage moderation team access.'}
                </p>
              </div>

            </header>

            {/* Metrics Grid */}
            {/* Tab Content */}
            <AnimatePresence mode="wait">
              <motion.div
                key={currentTab}
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: -10 }}
                className="space-y-6"
              >
                {currentTab === 'reports' && (
                  <div className="space-y-6">
                    {/* Reports Metrics */}
                    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                      <div className={metricCardClass('pending')}>
                        <div className="relative flex h-11 w-11 shrink-0 items-center justify-center rounded-none bg-danger-rose/10 text-danger-rose border border-danger-rose/20 shadow-[inset_0_0_20px_rgba(244,63,94,0.05)]">
                          <Activity size={20} />
                          <div className="hud-bracket hud-bracket-tl" />
                          <div className="hud-bracket-br" />
                        </div>
                        <div className="flex flex-col">
                          <p className="font-heading text-[10px] font-bold uppercase tracking-[0.15em] text-[var(--admin-text-muted)] mb-0.5">Pending Reports</p>
                          <p className="font-heading text-2xl font-bold text-[var(--admin-text)]">{serverReportMetrics.pending}</p>
                        </div>
                      </div>
                      <div className={metricCardClass('approved')}>
                        <div className="relative flex h-11 w-11 shrink-0 items-center justify-center rounded-none bg-success-emerald/10 text-success-emerald border border-success-emerald/20 shadow-[inset_0_0_20px_rgba(52,211,153,0.05)]">
                          <CheckCircle2 size={20} />
                          <div className="hud-bracket hud-bracket-tl" />
                          <div className="hud-bracket-br" />
                        </div>
                        <div className="flex flex-col">
                          <p className="font-heading text-[10px] font-bold uppercase tracking-[0.15em] text-[var(--admin-text-muted)] mb-0.5">Approved Actions</p>
                          <p className="font-heading text-2xl font-bold text-[var(--admin-text)]">{serverReportMetrics.approved}</p>
                        </div>
                      </div>
                      <div className={metricCardClass('inactive')}>
                        <div className="relative flex h-11 w-11 shrink-0 items-center justify-center rounded-none bg-amber-500/10 text-amber-400 border border-amber-500/20 shadow-[inset_0_0_20px_rgba(245,158,11,0.05)]">
                          <XCircle size={20} />
                          <div className="hud-bracket hud-bracket-tl" />
                          <div className="hud-bracket-br" />
                        </div>
                        <div className="flex flex-col">
                          <p className="font-heading text-[10px] font-bold uppercase tracking-[0.15em] text-[var(--admin-text-muted)] mb-0.5">Rejected Actions</p>
                          <p className="font-heading text-2xl font-bold text-[var(--admin-text)]">{serverReportMetrics.rejected}</p>
                        </div>
                      </div>
                    </div>
                    <div className={`${surfaceCardClass} p-5`}>
                      <div className="grid gap-4 lg:grid-cols-[auto_auto_1fr] lg:items-end">
                        <div>
                          <p className="mb-2 text-xs font-bold uppercase tracking-widest text-[var(--admin-text-muted)]">Status Filter</p>
                          <div className="flex flex-wrap gap-2">
                            <button type="button" onClick={() => setReportStatusFilter('pending')} className={filterButtonClass(reportStatusFilter === 'pending')}>Pending</button>
                            <button type="button" onClick={() => setReportStatusFilter('decided')} className={filterButtonClass(reportStatusFilter === 'decided')}>Decided</button>
                            <button type="button" onClick={() => setReportStatusFilter('all')} className={filterButtonClass(reportStatusFilter === 'all')}>All</button>
                          </div>
                        </div>
                        <div>
                          <label className="mb-2 block text-xs font-bold uppercase tracking-widest text-[var(--admin-text-muted)]">Show Limit</label>
                          <select
                            value={reportLimit}
                            onChange={(e) => setReportLimit(e.target.value)}
                            className={`${compactSelectClass} min-w-[150px] appearance-none`}
                          >
                            <option value="10">10 entries</option>
                            <option value="20">20 entries</option>
                            <option value="50">50 entries</option>
                            <option value="all">All entries</option>
                          </select>
                        </div>
                        <div className="flex flex-wrap gap-2 lg:justify-end">
                          <button
                            type="button"
                            onClick={fetchReports}
                            className={`${actionButtonClass} bg-[var(--admin-text)] text-[var(--admin-bg)] hover:opacity-90`}
                          >
                            <RefreshCw size={16} />
                            Refresh Reports
                          </button>
                          <button
                            type="button"
                            onClick={toggleSelectAllVisibleReports}
                            disabled={selectableVisibleReports.length === 0}
                            className={`${actionButtonClass} border border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] text-[var(--admin-text)] hover:bg-[var(--admin-muted-surface-hover)]`}
                          >
                            {allVisibleReportsSelected ? 'Clear Selection' : 'Select All'}
                          </button>
                          <button
                            type="button"
                            onClick={() => {
                              void Promise.all(selectedPendingReportIds.map((id) => updateReportStatus(id, 'approved')));
                              setSelectedReports(new Set());
                            }}
                            disabled={selectedPendingReportsCount === 0}
                            className={`${actionButtonClass} bg-emerald-500/10 text-emerald-300 hover:bg-emerald-500/20`}
                          >
                            <CheckCircle2 size={16} />
                            Approve Selected ({selectedPendingReportsCount})
                          </button>
                          <button
                            type="button"
                            onClick={() => {
                              void Promise.all(selectedPendingReportIds.map((id) => updateReportStatus(id, 'rejected')));
                              setSelectedReports(new Set());
                            }}
                            disabled={selectedPendingReportsCount === 0}
                            className={`${actionButtonClass} bg-rose-500/10 text-rose-300 hover:bg-rose-500/20`}
                          >
                            <XCircle size={16} />
                            Reject Selected ({selectedPendingReportsCount})
                          </button>
                        </div>
                      </div>
                    </div>

                    {reports.length === 0 ? (
                      <div className="flex flex-col items-center justify-center rounded-none border border-dashed border-[var(--admin-outline-strong)] py-20">
                        <div className="mb-4 rounded-full bg-[var(--admin-muted-surface)] p-4 text-[var(--admin-text-muted)]">
                          <CheckCircle2 size={40} />
                        </div>
                        <p className="text-lg font-medium text-[var(--admin-text)]">{reportStatusFilter === 'pending' ? 'Queue Clear' : 'No Reports Found'}</p>
                        <p className="text-sm text-[var(--admin-text-muted)]">
                          {reportStatusFilter === 'pending' ? 'No pending reports at the moment.' : 'There are no reports for the selected filter.'}
                        </p>
                      </div>
                    ) : (
                      reports.map((report) => (
                        <div key={report.id} className="surface-card rounded-none overflow-hidden p-0 transition-all duration-300 hover:border-[var(--admin-outline-strong)]">
                          <div className="hud-bracket hud-bracket-tl" />
                          <div className="hud-bracket-tr" />
                          <div className="hud-bracket-bl" />
                          <div className="hud-bracket-br" />

                          <div className="flex flex-col lg:grid lg:grid-cols-[56px_1fr_320px_200px] lg:items-stretch overflow-hidden">
                            {/* Checkbox Column */}
                            <div className="flex items-center justify-center py-4 lg:py-0 border-b lg:border-b-0 lg:border-r border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)]">
                              {report.status === 'pending' ? (
                                <input
                                  type="checkbox"
                                  checked={selectedReports.has(report.id)}
                                  onChange={(e) => {
                                    setSelectedReports((current) => {
                                      const next = new Set(current);
                                      if (e.target.checked) next.add(report.id);
                                      else next.delete(report.id);
                                      return next;
                                    });
                                  }}
                                  className="h-5 w-5 cursor-pointer appearance-none rounded-none border border-[var(--admin-outline-strong)] bg-[var(--admin-input-bg)] text-electric-cyan transition-all checked:border-electric-cyan checked:bg-electric-cyan focus:ring-2 focus:ring-electric-cyan/30 focus:ring-offset-0"
                                />
                              ) : (
                                <span className="font-mono text-xs font-bold uppercase tracking-widest text-[var(--admin-text-muted)]">
                                  done
                                </span>
                              )}
                            </div>

                            {/* Content Column */}
                            <div className="flex-1 flex flex-col p-5 sm:p-6 lg:p-7 min-w-0 overflow-hidden">
                              {/* Top Bar */}
                              <div className="flex items-center gap-4 mb-2">
                                <span className={`rounded-none px-2.5 py-1 text-[10px] font-bold uppercase tracking-[0.14em] ${reportStatusClass(report.status)} text-[var(--admin-text)]`}>
                                  {report.status}
                                </span>
                                <div className="flex items-center gap-2 font-heading text-[11px] text-[var(--admin-text-muted)] font-bold uppercase tracking-[0.14em]">
                                  <Clock size={12} className="text-[var(--admin-text-muted)]" />
                                  <span>{formatDate(report.created_at)}</span>
                                </div>
                              </div>

                              {/* Incident Reason (compact) */}
                              <div className="mb-4">
                                <p className="text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Incident Reason</p>
                                <p className="text-lg font-bold text-[var(--admin-text)] leading-tight">{report.reason}</p>
                              </div>

                              {/* Description Area */}
                              <div className="flex-1 min-w-0">
                                {report.description && (
                                  <div className="group relative">
                                    <p className="text-sm text-[var(--admin-text-soft)] font-medium leading-relaxed line-clamp-4 break-words whitespace-pre-wrap overflow-hidden">
                                      {report.description}
                                    </p>
                                    {(report.description.length > 100) && (
                                      <button
                                        type="button"
                                        onClick={() => setViewingDescription(report.id)}
                                        className="mt-2 text-[10px] font-bold uppercase tracking-widest text-electric-cyan/70 hover:text-electric-cyan transition-colors flex items-center gap-1"
                                      >
                                        <Eye size={12} />
                                        READ_FULL_DESCRIPTION
                                      </button>
                                    )}
                                  </div>
                                )}
                              </div>
                            </div>

                            {/* Detailed Data Column */}
                            <div className="flex flex-col gap-3 p-5 sm:p-6 lg:p-7 lg:justify-center border-t lg:border-t-0 lg:border-l border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] min-w-0 overflow-hidden">
                              <div className="detail-panel rounded-none overflow-hidden flex flex-col justify-center">
                                <div className="flex min-h-[64px] flex-col justify-center gap-1 border-b border-[var(--admin-detail-border)] px-5 py-3">
                                  <span className="text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Reported Identity</span>
                                  <span className="font-mono text-sm text-[var(--admin-text)] tabular-nums break-all leading-relaxed whitespace-pre-wrap">{report.reported_ip || 'N/A'}</span>
                                </div>
                                <div className="flex min-h-[64px] flex-col justify-center gap-1 border-b border-[var(--admin-detail-border)] px-5 py-3">
                                  <span className="text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Session ID Reference</span>
                                  <span className="font-mono text-sm text-[var(--admin-text-soft)] tabular-nums break-all leading-relaxed whitespace-pre-wrap">{report.reported_session_id}</span>
                                </div>
                                <div className="flex min-h-[64px] flex-col justify-center gap-1 px-5 py-3">
                                  <span className="text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Reporter Context</span>
                                  <span className="font-mono text-sm text-[var(--admin-text-soft)] tabular-nums break-all leading-relaxed whitespace-pre-wrap">{report.reporter_ip || 'N/A'}</span>
                                </div>
                              </div>
                            </div>

                            {/* Action Column */}
                            <div className="flex flex-col gap-3 p-5 sm:p-6 lg:p-7 lg:justify-center border-t lg:border-t-0 lg:border-l border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)]">
                              {report.chat_log.length > 0 && (
                                <button
                                  type="button"
                                  onClick={() => setExpandedReport(expandedReport === report.id ? null : report.id)}
                                  className={`${actionButtonClass} w-full bg-[var(--admin-muted-surface)] border border-[var(--admin-outline-strong)] text-[var(--admin-text)] hover:bg-[var(--admin-muted-surface)] hover:text-[var(--admin-text)] transition-all`}
                                >
                                  <MessageSquare size={14} className="mr-0.5" />
                                  Transcript
                                </button>
                              )}

                              {report.status === 'pending' && (
                                <div className="grid grid-cols-2 gap-3 lg:grid-cols-1">
                                  <button
                                    type="button"
                                    onClick={(event) => handleSingleReportAction(event, report.id, 'approved')}
                                    className={`${actionButtonClass} w-full bg-emerald-500/10 border border-emerald-500/20 text-emerald-400 hover:bg-emerald-500/20 shadow-[0_0_12px_rgba(52,211,153,0.1)]`}
                                  >
                                    <CheckCircle2 size={14} className="mr-0.5" />
                                    Approve
                                  </button>
                                  <button
                                    type="button"
                                    onClick={(event) => handleSingleReportAction(event, report.id, 'rejected')}
                                    className={`${actionButtonClass} w-full bg-rose-500/10 border border-rose-500/20 text-rose-400 hover:bg-rose-500/20 shadow-[0_0_12px_rgba(244,63,94,0.1)]`}
                                  >
                                    <XCircle size={14} className="mr-0.5" />
                                    Reject
                                  </button>
                                </div>
                              )}

                              {canCreateBans && (
                                <button
                                  type="button"
                                  onClick={() => openBanModal({
                                    sourceReportId: report.id,
                                    sessionId: report.reported_session_id,
                                    ip: report.reported_ip,
                                    reason: report.reason,
                                  })}
                                  className={`${actionButtonClass} w-full bg-danger-rose text-[var(--admin-text)] shadow-[0_0_16px_rgba(244,63,94,0.25)] hover:bg-rose-600 hover:shadow-[0_0_24px_rgba(244,63,94,0.4)] transition-all`}
                                >
                                  <BanIcon size={14} className="mr-0.5" />
                                  Ban User
                                </button>
                              )}
                            </div>
                          </div>


                        </div>
                      ))
                    )}
                  </div>
                )}

                {currentTab === 'bans' && (
                  <div className="space-y-4">
                    {/* Bans Metrics Row */}
                    <div className="grid gap-4 sm:grid-cols-3">
                      <div className={metricCardClass('active')}>
                        <div className="relative flex h-11 w-11 shrink-0 items-center justify-center rounded-none bg-rose-500/10 text-rose-400 border border-rose-500/20 shadow-[inset_0_0_20px_rgba(244,63,94,0.05)]">
                          <BanIcon size={20} />
                          <div className="hud-bracket hud-bracket-tl" />
                          <div className="hud-bracket-tr" />
                          <div className="hud-bracket-bl" />
                          <div className="hud-bracket-br" />
                        </div>
                        <div className="flex flex-col">
                          <p className="font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)] mb-1">Active Bans</p>
                          <p className="font-heading text-2xl font-bold text-[var(--admin-text)] tracking-tight">{serverBanMetrics.active}</p>
                        </div>
                      </div>
                      <div className={metricCardClass('inactive')}>
                        <div className="relative flex h-11 w-11 shrink-0 items-center justify-center rounded-none bg-[var(--admin-muted-surface)] text-[var(--admin-text-soft)] border border-[var(--admin-outline-soft)]">
                          <Clock size={20} />
                          <div className="hud-bracket hud-bracket-tl" />
                          <div className="hud-bracket-tr" />
                          <div className="hud-bracket-bl" />
                          <div className="hud-bracket-br" />
                        </div>
                        <div className="flex flex-col">
                          <p className="font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)] mb-1">Inactive Bans</p>
                          <p className="font-heading text-2xl font-bold text-[var(--admin-text)] tracking-tight">{serverBanMetrics.total - serverBanMetrics.active}</p>
                        </div>
                      </div>
                      <div className={metricCardClass('total')}>
                        <div className="relative flex h-11 w-11 shrink-0 items-center justify-center rounded-none bg-cyan-500/10 text-cyan-400 border border-cyan-500/20 shadow-[inset_0_0_20px_rgba(34,211,238,0.05)]">
                          <Globe size={20} />
                          <div className="hud-bracket hud-bracket-tl" />
                          <div className="hud-bracket-tr" />
                          <div className="hud-bracket-bl" />
                          <div className="hud-bracket-br" />
                        </div>
                        <div className="flex flex-col">
                          <p className="font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)] mb-1">Total Bans</p>
                          <p className="font-heading text-2xl font-bold text-[var(--admin-text)] tracking-tight">{serverBanMetrics.total}</p>
                        </div>
                      </div>
                    </div>
                    {/* Ban Controls Panel */}
                    <div className="surface-card rounded-none p-5">
                      <div className="hud-bracket hud-bracket-tl" />
                      <div className="hud-bracket-tr" />
                      <div className="hud-bracket-bl" />
                      <div className="hud-bracket-br" />

                      <div className="flex flex-col gap-5">
                        {/* Search & Filters Grid */}
                        <div className="grid gap-4 lg:grid-cols-[1fr_auto_auto] lg:items-end">
                          <div className="flex flex-col gap-2">
                            <label className="font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Search Registry</label>
                            <div className="relative">
                              <Search className="absolute left-3 top-3 text-[var(--admin-text-muted)]" size={16} />
                              <input
                                type="text"
                                value={banSearch}
                                onChange={(e) => setBanSearch(e.target.value)}
                                className={`${inputClass} pl-10`}
                                placeholder="SEARCH IP, SESSION, REASON, OR ADMIN..."
                              />
                            </div>
                          </div>

                          <div className="flex flex-col gap-2">
                            <label className="font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Status Filter</label>
                            <div className="flex items-center gap-2">
                              <button type="button" onClick={() => setBanFilter('active')} className={filterButtonClass(banFilter === 'active', 'rose')}>Active</button>
                              <button type="button" onClick={() => setBanFilter('inactive')} className={filterButtonClass(banFilter === 'inactive')}>Inactive</button>
                              <button type="button" onClick={() => setBanFilter('all')} className={filterButtonClass(banFilter === 'all')}>All</button>
                            </div>
                          </div>

                          <div className="flex items-center gap-3">
                            <div className="flex-1 lg:w-[140px]">
                              <label className="mb-2 block font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Show Limit</label>
                              <select
                                value={banLimit}
                                onChange={(e) => setBanLimit(e.target.value)}
                                className={compactSelectClass}
                              >
                                <option value="10">10 ENTRIES</option>
                                <option value="20">20 ENTRIES</option>
                                <option value="50">50 ENTRIES</option>
                                <option value="100">100 ENTRIES</option>
                                <option value="all">ALL ENTRIES</option>
                              </select>
                            </div>
                            <div className="pt-[22px]">
                              <button
                                type="button"
                                onClick={fetchBans}
                                className="flex h-11 w-11 shrink-0 items-center justify-center rounded-none border border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] text-[var(--admin-text-soft)] transition-all hover:bg-[var(--admin-muted-surface)] hover:text-[var(--admin-text)]"
                                title="Refresh Registry"
                              >
                                <RefreshCw size={16} />
                              </button>
                            </div>
                          </div>
                        </div>
                      </div>
                    </div>

                    {/* Manual Enforcement Panel */}
                    {canCreateBans && (
                      <div className="surface-card rounded-none p-5">
                        <div className="hud-bracket hud-bracket-tl" />
                        <div className="hud-bracket-tr" />
                        <div className="hud-bracket-bl" />
                        <div className="hud-bracket-br" />

                        <div className="flex items-center gap-3 mb-6">
                          <h3 className="section-prefix font-heading text-sm font-bold uppercase tracking-[0.14em] text-[var(--admin-text)]">Manual Enforcement</h3>
                          <span className="ml-auto font-heading text-[10px] text-[var(--admin-text-muted)] tracking-wider font-bold uppercase tracking-[0.14em] whitespace-nowrap">// OPERATOR INPUT</span>
                        </div>

                        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-[1fr_1fr_1.5fr_auto] lg:items-end">
                          <div className="flex flex-col gap-2">
                            <label className="font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Session Identifier</label>
                            <input
                              type="text"
                              value={manualBanSessionId}
                              onChange={(e) => setManualBanSessionId(e.target.value)}
                              className={inputClass}
                              placeholder="SESSION_ID"
                            />
                          </div>
                          <div className="flex flex-col gap-2">
                            <label className="font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Network Address</label>
                            <input
                              type="text"
                              value={manualBanIP}
                              onChange={(e) => setManualBanIP(e.target.value)}
                              className={inputClass}
                              placeholder="192.168.X.X"
                            />
                          </div>
                          <div className="flex flex-col gap-2">
                            <label className="font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Reason for Restriction</label>
                            <input
                              type="text"
                              value={manualBanReason}
                              onChange={(e) => setManualBanReason(e.target.value)}
                              className="h-11 w-full rounded-none border border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] px-4 font-heading text-sm text-[var(--admin-text)] placeholder:text-[var(--admin-text-muted)] outline-none transition-all duration-200 focus:border-electric-cyan/50 focus:ring-2 focus:ring-electric-cyan/15 focus:bg-[var(--admin-muted-surface)] scanlines"
                              placeholder="EXPLAIN INCIDENT..."
                            />
                          </div>
                          <div className="pt-2 sm:pt-0">
                            <button
                              type="button"
                              onClick={() => {
                                if (!manualBanSessionId && !manualBanIP) {
                                  alert('Please enter either Session ID or IP Address');
                                  return;
                                }
                                if (!manualBanReason.trim()) {
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
                              className={`${actionButtonClass} w-full lg:w-auto bg-danger-rose text-[var(--admin-text)] hover:bg-rose-600 hover:shadow-[0_0_20px_rgba(244,63,94,0.35)]`}
                            >
                              <Plus size={16} />
                              Apply Ban
                            </button>
                          </div>
                        </div>
                      </div>
                    )}

                    {/* Bans List */}
                    <div className="space-y-4">
                      {bans.length === 0 ? (
                        <div className="surface-card rounded-none flex flex-col items-center justify-center px-8 py-16 text-center">
                          <div className="hud-bracket hud-bracket-tl" />
                          <div className="hud-bracket-tr" />
                          <div className="hud-bracket-bl" />
                          <div className="hud-bracket-br" />

                          <div className="mb-6 flex h-16 w-16 items-center justify-center rounded-none border border-[var(--admin-outline-strong)] bg-[var(--admin-muted-surface)]">
                            <BanIcon size={28} className="text-[var(--admin-text-muted)]" />
                          </div>
                          <h4 className="font-heading text-lg font-semibold text-[var(--admin-text-soft)] mb-2 font-heading tracking-wide">NO RESTRICTION RECORDS</h4>
                          <p className="font-heading text-[11px] text-[var(--admin-text-muted)] uppercase tracking-[0.14em] font-bold">SYSTEM_REGISTRY_CLEAR // ALL FILTERS VALID</p>
                        </div>
                      ) : (
                        <AnimatePresence mode="popLayout">
                          {bans.map((ban, index) => (
                            <motion.div
                              key={ban.id}
                              initial={{ opacity: 0, y: 10 }}
                              animate={{ opacity: 1, y: 0 }}
                              exit={{ opacity: 0, y: -10 }}
                              transition={{ delay: index * 0.03, duration: 0.2 }}
                              className="surface-card group rounded-none p-0 overflow-hidden transition-all duration-300 hover:border-[var(--admin-outline-strong)] hover:translate-y-[-1px] hover:shadow-[0_8px_32px_rgba(0,0,0,0.4)]"
                            >
                              <div className="hud-bracket hud-bracket-tl" />
                              <div className="hud-bracket-tr" />
                              <div className="hud-bracket-bl" />
                              <div className="hud-bracket-br" />

                              <div className="flex flex-col md:grid md:grid-cols-[4px_1fr_120px] md:items-stretch h-full">
                                {/* Status Stripe */}
                                <div className={`w-full h-1 md:w-1 md:h-auto shrink-0 ${ban.is_active
                                  ? 'bg-gradient-to-b from-rose-500 to-rose-700 shadow-[2px_0_12px_rgba(244,63,94,0.2)]'
                                  : 'bg-[var(--admin-muted-surface)]'
                                  }`} />

                                <div className="flex flex-1 flex-col px-4 py-4 md:px-5 md:py-4">
                                  {/* Meta Row */}
                                  <div className="flex items-center justify-between gap-3 flex-wrap mb-3">
                                    <div className={`inline-flex items-center gap-1.5 rounded-none px-2.5 py-1 text-[10px] font-bold uppercase tracking-[0.14em] ${ban.is_active
                                      ? 'border border-rose-500/20 bg-rose-500/10 text-rose-300'
                                      : 'border border-[var(--admin-outline-strong)] bg-[var(--admin-muted-surface)] text-[var(--admin-text-muted)]'
                                      }`}>
                                      <div className={`h-1.5 w-1.5 rounded-none ${ban.is_active ? 'bg-rose-400 animate-pulse' : 'bg-[var(--admin-text-muted)]'}`} />
                                      {ban.is_active ? 'Active' : 'Inactive'}
                                    </div>
                                    <div className="flex items-center gap-1.5 font-heading text-[11px] text-[var(--admin-text-muted)] font-bold uppercase tracking-[0.14em]">
                                      <Clock size={12} className="text-[var(--admin-text-muted)]" />
                                      <span>{formatDate(ban.created_at)}</span>
                                    </div>
                                  </div>

                                  {/* Data Panel */}
                                  <div className="detail-panel rounded-none px-4 py-3 md:px-5">
                                    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-x-8 gap-y-4">
                                      <div className="flex flex-col">
                                        <p className="mb-1 text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Network Address</p>
                                        <p className="font-mono text-sm font-medium text-[var(--admin-text)] tabular-nums break-all">{ban.ip_address || 'UNAVAILABLE'}</p>
                                      </div>
                                      <div className="flex flex-col">
                                        <p className="mb-1 text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Incident Reason</p>
                                        <p className="text-sm font-medium text-[var(--admin-text)] leading-5 line-clamp-2" title={ban.reason}>{ban.reason}</p>
                                      </div>
                                      <div className="flex flex-col sm:col-span-2 lg:col-span-1">
                                        <p className="mb-1 text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Restriction Expiry</p>
                                        <div className="flex items-center gap-2 text-sm font-medium">
                                          {!ban.expires_at ? (
                                            <span className="text-[var(--admin-text-muted)] font-bold uppercase tracking-wider text-[11px]">∞ PERMANENT_CELL</span>
                                          ) : (
                                            <div className="flex items-center gap-2 text-amber-400/90">
                                              <Clock size={13} />
                                              <span className="font-mono tabular-nums">{formatDate(ban.expires_at)}</span>
                                            </div>
                                          )}
                                        </div>
                                      </div>
                                    </div>
                                  </div>
                                </div>

                                {/* Action Area */}
                                <div className="flex items-center justify-start md:justify-center px-4 pb-4 md:pb-0 md:px-4 border-t border-[var(--admin-outline-soft)] md:border-t-0 md:border-l md:border-[var(--admin-outline-soft)]">
                                  {ban.is_active && canManageBans ? (
                                    <button
                                      type="button"
                                      onClick={() => unban(ban.id)}
                                      className="inline-flex h-9 min-w-[92px] items-center justify-center gap-1.5 rounded-none border border-emerald-500/20 bg-emerald-500/10 px-4 font-heading text-[11px] font-bold uppercase tracking-wide text-emerald-400 transition-all hover:bg-emerald-500/20 hover:border-emerald-500/40 hover:shadow-[0_0_16px_rgba(52,211,153,0.2)] active:scale-95"
                                    >
                                      <RefreshCw size={13} />
                                      Unban
                                    </button>
                                  ) : (
                                    <div className="h-9 w-[92px]" />
                                  )}
                                </div>
                              </div>
                            </motion.div>
                          ))}
                        </AnimatePresence>
                      )}
                    </div>
                  </div>
                )}

                {currentTab === 'infra' && canViewInfraHealth && (
                  <div className="space-y-6">
                    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                      <div className={metricCardClass('active')}>
                        <div className="relative flex h-11 w-11 shrink-0 items-center justify-center rounded-none bg-success-emerald/10 text-success-emerald border border-success-emerald/20 shadow-[inset_0_0_20px_rgba(52,211,153,0.05)]">
                          <CheckCircle2 size={20} />
                          <div className="hud-bracket hud-bracket-tl" />
                          <div className="hud-bracket-br" />
                        </div>
                        <div className="flex flex-col">
                          <p className="font-heading text-[10px] font-bold uppercase tracking-[0.15em] text-[var(--admin-text-muted)] mb-0.5">Healthy Services</p>
                          <p className="font-heading text-2xl font-bold text-[var(--admin-text)]">{infraHealth?.summary.healthy_services || 0}</p>
                        </div>
                      </div>
                      <div className={metricCardClass('inactive')}>
                        <div className="relative flex h-11 w-11 shrink-0 items-center justify-center rounded-none bg-amber-500/10 text-amber-400 border border-amber-500/20 shadow-[inset_0_0_20px_rgba(245,158,11,0.05)]">
                          <AlertTriangle size={20} />
                          <div className="hud-bracket hud-bracket-tl" />
                          <div className="hud-bracket-br" />
                        </div>
                        <div className="flex flex-col">
                          <p className="font-heading text-[10px] font-bold uppercase tracking-[0.15em] text-[var(--admin-text-muted)] mb-0.5">Degraded Services</p>
                          <p className="font-heading text-2xl font-bold text-[var(--admin-text)]">{infraHealth?.summary.degraded_services || 0}</p>
                        </div>
                      </div>
                      <div className={metricCardClass('total')}>
                        <div className="relative flex h-11 w-11 shrink-0 items-center justify-center rounded-none bg-electric-cyan/10 text-electric-cyan border border-electric-cyan/20 shadow-[inset_0_0_20px_rgba(34,211,238,0.05)]">
                          <Network size={20} />
                          <div className="hud-bracket hud-bracket-tl" />
                          <div className="hud-bracket-br" />
                        </div>
                        <div className="flex flex-col">
                          <p className="font-heading text-[10px] font-bold uppercase tracking-[0.15em] text-[var(--admin-text-muted)] mb-0.5">Phoenix Nodes</p>
                          <p className="font-heading text-2xl font-bold text-[var(--admin-text)]">{infraHealth?.topology.phoenix_connected_nodes || 0}</p>
                        </div>
                      </div>
                      <div className={metricCardClass('total')}>
                        <div className="relative flex h-11 w-11 shrink-0 items-center justify-center rounded-none bg-electric-cyan/10 text-electric-cyan border border-electric-cyan/20 shadow-[inset_0_0_20px_rgba(34,211,238,0.05)]">
                          <Database size={20} />
                          <div className="hud-bracket hud-bracket-tl" />
                          <div className="hud-bracket-br" />
                        </div>
                        <div className="flex flex-col">
                          <p className="font-heading text-[10px] font-bold uppercase tracking-[0.15em] text-[var(--admin-text-muted)] mb-0.5">Redis Reachable</p>
                          <p className="font-heading text-2xl font-bold text-[var(--admin-text)]">
                            {infraHealth ? `${infraHealth.topology.redis_reachable_nodes}/${infraHealth.topology.redis_configured_nodes}` : '0/0'}
                          </p>
                        </div>
                      </div>
                    </div>

                    <div className="grid gap-6 xl:grid-cols-3">
                      <div className={`${surfaceCardClass} xl:col-span-2`}>
                        <div className="mb-4 flex items-center justify-between">
                          <div>
                            <h2 className="text-sm font-bold uppercase tracking-[0.16em] text-[var(--admin-text)]">Service Health Matrix</h2>
                            <p className="mt-1 text-xs text-[var(--admin-text-muted)]">Live status for Phoenix and Go service endpoints.</p>
                          </div>
                          <button
                            type="button"
                            onClick={() => void fetchInfraHealth()}
                            className="flex h-10 items-center gap-2 rounded-none border border-[var(--admin-input-border)] bg-[var(--admin-input-bg)] px-4 text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text)] transition-all hover:bg-[var(--admin-input-focus-bg)]"
                          >
                            <RefreshCw size={14} />
                            Refresh
                          </button>
                        </div>
                        <div className="space-y-3">
                          {(infraHealth?.services || []).map((service) => {
                            const goMemory = service.kind === 'go' ? goMemorySummary(service.details) : null;
                            const phoenixMemory = service.kind === 'phoenix' ? phoenixMemorySummary(service.details) : null;

                            return (
                              <div key={`${service.kind}-${service.name}`} className="border border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] p-4">
                                <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                                  <div>
                                    <div className="flex items-center gap-3">
                                      <p className="text-sm font-bold uppercase tracking-[0.14em] text-[var(--admin-text)]">{service.name}</p>
                                      <span className={`px-2 py-1 text-[10px] font-bold uppercase tracking-[0.14em] ${healthStatusClass(service.status)}`}>{service.status}</span>
                                    </div>
                                    <p className="mt-1 text-xs text-[var(--admin-text-muted)]">{service.kind} · {service.url}</p>
                                  </div>
                                  <div className="text-left sm:text-right">
                                    <p className="text-xs text-[var(--admin-text-muted)]">Latency {formatLatency(service.latency_ms)}</p>
                                    <p className="text-xs text-[var(--admin-text-muted)]">HTTP {service.http_status || 'n/a'}</p>
                                  </div>
                                </div>
                                {service.details && (
                                  <div className="mt-3 grid gap-2 text-xs text-[var(--admin-text-soft)] sm:grid-cols-2">
                                    {'node' in service.details && <p>Node: {String(service.details.node)}</p>}
                                    {'status' in service.details && <p>Reported status: {String(service.details.status)}</p>}
                                    {goMemory && <p>Heap alloc: {goMemory.primary}</p>}
                                    {goMemory && <p>Runtime sys: {goMemory.secondary}</p>}
                                    {goMemory && <p>Goroutines: {goMemory.goroutines}</p>}
                                    {phoenixMemory && <p>Memory total: {phoenixMemory.total}</p>}
                                    {phoenixMemory && <p>Binary memory: {phoenixMemory.binary}</p>}
                                    {phoenixMemory && <p>Processes: {phoenixMemory.processes}</p>}
                                  </div>
                                )}
                                {service.error && <p className="mt-3 text-xs text-rose-400">{service.error}</p>}
                              </div>
                            );
                          })}
                        </div>
                      </div>

                      <div className="space-y-6">
                        <div className={surfaceCardClass}>
                          <h2 className="mb-4 text-sm font-bold uppercase tracking-[0.16em] text-[var(--admin-text)]">Topology</h2>
                          <div className="space-y-3 text-sm text-[var(--admin-text-soft)]">
                            <div className="flex items-center justify-between"><span>Phoenix configured</span><span className="font-bold text-[var(--admin-text)]">{infraHealth?.topology.phoenix_configured_nodes || 0}</span></div>
                            <div className="flex items-center justify-between"><span>Phoenix connected</span><span className="font-bold text-[var(--admin-text)]">{infraHealth?.topology.phoenix_connected_nodes || 0}</span></div>
                            <div className="flex items-center justify-between"><span>Go services</span><span className="font-bold text-[var(--admin-text)]">{infraHealth?.topology.go_configured_services || 0}</span></div>
                            <div className="flex items-center justify-between"><span>Redis configured</span><span className="font-bold text-[var(--admin-text)]">{infraHealth?.topology.redis_configured_nodes || 0}</span></div>
                          </div>
                          <div className="mt-4 flex flex-wrap gap-2">
                            {(infraHealth?.topology.phoenix_node_names || []).map((nodeName) => (
                              <span key={nodeName} className="border border-electric-cyan/20 bg-electric-cyan/10 px-2 py-1 text-[10px] font-bold uppercase tracking-[0.14em] text-electric-cyan">
                                {nodeName}
                              </span>
                            ))}
                          </div>
                        </div>

                        <div className={surfaceCardClass}>
                          <h2 className="mb-4 text-sm font-bold uppercase tracking-[0.16em] text-[var(--admin-text)]">Data Stores</h2>
                          <div className="space-y-4">
                            <div className="border border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] p-4">
                              <div className="flex items-center justify-between">
                                <p className="text-sm font-bold uppercase tracking-[0.14em] text-[var(--admin-text)]">Postgres</p>
                                <span className={`px-2 py-1 text-[10px] font-bold uppercase tracking-[0.14em] ${healthStatusClass(infraHealth?.postgres.status)}`}>{infraHealth?.postgres.status || 'unknown'}</span>
                              </div>
                              <p className="mt-2 text-xs text-[var(--admin-text-muted)]">Latency {formatLatency(infraHealth?.postgres.latency_ms)}</p>
                              <div className="mt-3 grid grid-cols-2 gap-2 text-xs text-[var(--admin-text-soft)]">
                                <p>Open: {infraHealth?.postgres.connections.open || 0}</p>
                                <p>In use: {infraHealth?.postgres.connections.in_use || 0}</p>
                                <p>Idle: {infraHealth?.postgres.connections.idle || 0}</p>
                                <p>Max: {infraHealth?.postgres.connections.max_open || 0}</p>
                              </div>
                            </div>

                            <div className="border border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] p-4">
                              <div className="flex items-center justify-between">
                                <p className="text-sm font-bold uppercase tracking-[0.14em] text-[var(--admin-text)]">Redis Cluster</p>
                                <span className={`px-2 py-1 text-[10px] font-bold uppercase tracking-[0.14em] ${healthStatusClass(infraHealth?.redis.status)}`}>{infraHealth?.redis.status || 'unknown'}</span>
                              </div>
                              <p className="mt-2 text-xs text-[var(--admin-text-muted)]">Latency {formatLatency(infraHealth?.redis.latency_ms)}</p>
                              <div className="mt-3 grid grid-cols-2 gap-2 text-xs text-[var(--admin-text-soft)]">
                                <p>Cluster state: {infraHealth?.redis.cluster.state || 'unknown'}</p>
                                <p>Known nodes: {infraHealth?.redis.cluster.known_nodes || 0}</p>
                                <p>Slots assigned: {infraHealth?.redis.cluster.slots_assigned || 0}</p>
                                <p>Slots ok: {infraHealth?.redis.cluster.slots_ok || 0}</p>
                                <p>Slots pfail: {infraHealth?.redis.cluster.slots_pfail || 0}</p>
                                <p>Slots fail: {infraHealth?.redis.cluster.slots_fail || 0}</p>
                                <p>Primary shards: {infraHealth?.redis.cluster.size || 0}</p>
                                <p>Epoch: {infraHealth?.redis.cluster.current_epoch || 0}</p>
                              </div>
                              {infraHealth?.redis.error && (
                                <p className="mt-3 break-all text-xs text-rose-400">{infraHealth.redis.error}</p>
                              )}
                              <div className="mt-4 flex items-center justify-between gap-3">
                                <p className="text-xs text-[var(--admin-text-muted)]">{infraHealth?.redis.nodes?.length || 0} node entries available for detailed inspection.</p>
                                <button
                                  type="button"
                                  onClick={() => setIsRedisModalOpen(true)}
                                  className="inline-flex h-10 items-center justify-center gap-2 rounded-none border border-[var(--admin-input-border)] bg-[var(--admin-input-bg)] px-4 text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text)] transition-all hover:bg-[var(--admin-input-focus-bg)]"
                                >
                                  Inspect Redis
                                </button>
                              </div>
                            </div>
                          </div>
                        </div>

                        <div className={surfaceCardClass}>
                          <h2 className="mb-4 text-sm font-bold uppercase tracking-[0.16em] text-[var(--admin-text)]">Observability</h2>
                          <div className="space-y-3 text-sm text-[var(--admin-text-soft)]">
                            <div className="flex items-center justify-between"><span>Traces configured</span><span className="font-bold text-[var(--admin-text)]">{infraHealth?.observability.traces_configured ? 'yes' : 'no'}</span></div>
                            <div className="flex items-center justify-between"><span>Metrics configured</span><span className="font-bold text-[var(--admin-text)]">{infraHealth?.observability.metrics_configured ? 'yes' : 'no'}</span></div>
                            <div className="flex items-center justify-between"><span>Collector</span><span className={`font-bold ${infraHealth?.observability.collector.status === 'ok' ? 'text-emerald-400' : 'text-amber-400'}`}>{infraHealth?.observability.collector.status || 'unknown'}</span></div>
                            <div className="flex items-center justify-between"><span>Collector latency</span><span className="font-bold text-[var(--admin-text)]">{formatLatency(infraHealth?.observability.collector.latency_ms)}</span></div>
                            <p className="break-all text-xs text-[var(--admin-text-muted)]">Health URL: {infraHealth?.observability.collector.url || 'No collector health URL configured'}</p>
                            <p className="break-all text-xs text-[var(--admin-text-muted)]">OTLP endpoint: {infraHealth?.observability.otlp_endpoint || 'No OTLP endpoint configured'}</p>
                            {infraHealth?.observability.collector.error && (
                              <p className="break-all text-xs text-rose-400">{infraHealth.observability.collector.error}</p>
                            )}
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>
                )}

                {currentTab === 'bannedWords' && canManageBannedWords && (
                  <div className="space-y-6">
                    <div className={`${surfaceCardClass} p-6`}>
                      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto_auto] lg:items-end">
                        <div>
                          <label className="mb-2 block text-xs font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Add Word Or Phrase</label>
                          <input
                            type="text"
                            value={bannedWordInput}
                            onChange={(e) => setBannedWordInput(e.target.value)}
                            className={inputClass}
                            placeholder="Enter banned word or phrase..."
                          />
                        </div>
                        <div>
                          <label className="mb-2 block text-xs font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Search Phrase Registry</label>
                          <div className="relative">
                            <Search className="absolute top-3.5 left-4 text-[var(--admin-text-muted)]" size={18} />
                            <input
                              type="text"
                              value={bannedWordSearch}
                              onChange={(e) => setBannedWordSearch(e.target.value)}
                              className={`${inputClass} pl-12`}
                              placeholder="Search phrase, normalized form, or admin..."
                            />
                          </div>
                        </div>
                        <button
                          type="button"
                          onClick={() => void createBannedWord()}
                          className={`${actionButtonClass} bg-danger-rose text-[var(--admin-text)] hover:bg-rose-600`}
                        >
                          <Plus size={16} />
                          Add Word
                        </button>
                        <button
                          type="button"
                          onClick={() => void fetchBannedWords()}
                          className={`${actionButtonClass} bg-[var(--admin-text)] text-[var(--admin-bg)] hover:opacity-90`}
                        >
                          <RefreshCw size={16} />
                          Refresh
                        </button>
                      </div>
                    </div>

                    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                      <div className={metricCardClass('total')}>
                        <div className="relative flex h-11 w-11 shrink-0 items-center justify-center rounded-none bg-cyan-500/10 text-cyan-400 border border-cyan-500/20 shadow-[inset_0_0_20px_rgba(34,211,238,0.05)]">
                          <MessageSquare size={20} />
                        </div>
                        <div className="flex flex-col">
                          <p className="font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)] mb-1">Registry Size</p>
                          <p className="font-heading text-2xl font-bold text-[var(--admin-text)] tracking-tight">{bannedWordTotal}</p>
                        </div>
                      </div>
                      <div className={metricCardClass('inactive')}>
                        <div className="relative flex h-11 w-11 shrink-0 items-center justify-center rounded-none bg-[var(--admin-muted-surface)] text-[var(--admin-text-soft)] border border-[var(--admin-outline-soft)]">
                          <Eye size={20} />
                        </div>
                        <div className="flex flex-col">
                          <p className="font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)] mb-1">Visible Results</p>
                          <p className="font-heading text-2xl font-bold text-[var(--admin-text)] tracking-tight">{bannedWords.length}</p>
                        </div>
                      </div>
                      <div className={metricCardClass('active')}>
                        <div className="relative flex h-11 w-11 shrink-0 items-center justify-center rounded-none bg-emerald-500/10 text-emerald-400 border border-emerald-500/20 shadow-[inset_0_0_20px_rgba(16,185,129,0.05)]">
                          <Search size={20} />
                        </div>
                        <div className="flex flex-col">
                          <p className="font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)] mb-1">Display Limit</p>
                          <select
                            value={bannedWordLimit}
                            onChange={(e) => setBannedWordLimit(e.target.value)}
                            className={`${compactSelectClass} mt-1`}
                          >
                            <option value="10">10 ENTRIES</option>
                            <option value="25">25 ENTRIES</option>
                            <option value="50">50 ENTRIES</option>
                            <option value="100">100 ENTRIES</option>
                            <option value="all">ALL ENTRIES</option>
                          </select>
                        </div>
                      </div>
                    </div>

                    {bannedWordTotal > 0 && (
                      <p className="text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)] text-right">
                        {bannedWords.length} / {bannedWordTotal} shown
                      </p>
                    )}

                    <div className="space-y-4">
                      {bannedWords.length === 0 ? (
                        <div className="surface-card rounded-none flex flex-col items-center justify-center px-8 py-16 text-center">
                          <div className="mb-6 flex h-16 w-16 items-center justify-center rounded-none border border-[var(--admin-outline-strong)] bg-[var(--admin-muted-surface)]">
                            <MessageSquare size={28} className="text-[var(--admin-text-muted)]" />
                          </div>
                          <h4 className="font-heading text-lg font-semibold text-[var(--admin-text-soft)] mb-2 tracking-wide">NO BANNED WORDS</h4>
                          <p className="font-heading text-[11px] text-[var(--admin-text-muted)] uppercase tracking-[0.14em] font-bold">MESSAGE_FILTER_CLEAR</p>
                        </div>
                      ) : (
                        bannedWords.map((word) => (
                          <div key={word.id} className="surface-card rounded-none p-5">
                            <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
                              <div className="min-w-0">
                                <p className="text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Blocked Token</p>
                                <p className="mt-2 break-words font-mono text-base text-[var(--admin-text)]">{word.word}</p>
                                <p className="mt-3 text-xs text-[var(--admin-text-muted)]">
                                  Normalized as <span className="font-mono text-[var(--admin-text-soft)]">{word.normalized_word}</span> · Added by {word.created_by_username || 'unknown'} · {formatDate(word.created_at)}
                                </p>
                              </div>
                              <button
                                type="button"
                                onClick={() => void deleteBannedWord(word.id)}
                                className="inline-flex h-10 items-center justify-center gap-2 rounded-none border border-rose-500/20 bg-rose-500/10 px-4 text-[11px] font-bold uppercase tracking-[0.14em] text-rose-400 transition-all hover:bg-rose-500/20"
                              >
                                <Trash2 size={14} />
                                Remove
                              </button>
                            </div>
                          </div>
                        ))
                      )}
                    </div>
                  </div>
                )}

                {currentTab === 'accounts' && canManageAccounts && (
                  <div className="space-y-6">
                    <div className={`${surfaceCardClass} p-6`}>
                      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_auto_auto] lg:items-end">
                        <div>
                          <label className="mb-2 block text-xs font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Search Username</label>
                          <div className="relative">
                            <Search className="absolute top-3.5 left-4 text-[var(--admin-text-muted)]" size={18} />
                            <input
                              type="text"
                              value={accountSearch}
                              onChange={(e) => setAccountSearch(e.target.value)}
                              className={`${inputClass} pl-12`}
                              placeholder="Search username..."
                            />
                          </div>
                        </div>
                        <div>
                          <label className="mb-2 block text-xs font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">Show Limit</label>
                          <select
                            value={accountLimit}
                            onChange={(e) => setAccountLimit(e.target.value)}
                            className={`${compactSelectClass} min-w-[150px] appearance-none`}
                          >
                            <option value="10">10 entries</option>
                            <option value="25">25 entries</option>
                            <option value="50">50 entries</option>
                            <option value="all">All entries</option>
                          </select>
                        </div>
                        <div className="flex items-center gap-3">
                          <button
                            onClick={() => {
                              setAccountPage(1);
                              fetchAccounts();
                            }}
                            className={`${actionButtonClass} bg-[var(--admin-text)] text-[var(--admin-bg)] hover:opacity-90 whitespace-nowrap`}
                          >
                            <RefreshCw size={16} />
                            Refresh Accounts
                          </button>
                        </div>
                      </div>
                    </div>

                    {/* Create Account */}
                    <div className={`${surfaceCardClass} p-6`}>
                      <h3 className="mb-6 text-lg font-bold text-[var(--admin-text)]">Add Team Member</h3>
                      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                        <input
                          type="text"
                          value={accountUsername}
                          onChange={(e) => setAccountUsername(e.target.value)}
                          className={inputClass}
                          placeholder="Username"
                        />
                        <div className="relative">
                          <input
                            type={showCreateAccountPassword ? 'text' : 'password'}
                            value={accountPassword}
                            onChange={(e) => setAccountPassword(e.target.value)}
                            className={inputClass}
                            placeholder="Password"
                          />
                          <button
                            onClick={() => setShowCreateAccountPassword(!showCreateAccountPassword)}
                            className="absolute top-3.5 right-4 text-[var(--admin-text-muted)] hover:text-[var(--admin-text)]"
                          >
                            {showCreateAccountPassword ? <EyeOff size={18} /> : <Eye size={18} />}
                          </button>
                        </div>
                        <select
                          value={accountRole}
                          onChange={(e) => setAccountRole(e.target.value as AdminRole)}
                          className={compactSelectClass}
                        >
                          <option value="moderator">Moderator</option>
                          <option value="admin">Admin</option>
                          <option value="root">Root</option>
                        </select>
                        <button
                          onClick={createAccount}
                          disabled={submittingAccount}
                          className={`${actionButtonClass} bg-cyan-400 text-[#050816] hover:bg-cyan-300`}
                        >
                          <UserPlus size={18} />
                          Add Member
                        </button>
                      </div>
                    </div>

                    {/* Accounts List */}
                    <div className="space-y-4">
                      {accounts.length === 0 && (
                        <div className="rounded-none border border-dashed border-[var(--admin-outline-strong)] py-20 text-center">
                          <p className="text-[var(--admin-text-muted)]">No admin accounts found.</p>
                        </div>
                      )}
                      {accounts.map((account) => (
                        <div key={account.id} className={`${surfaceCardClass} p-6`}>
                          <div className="flex items-center justify-between">
                            <div className="flex items-center gap-4">
                              <div className="flex h-12 w-12 items-center justify-center rounded-none bg-[var(--admin-muted-surface)] text-cyan-400">
                                <Users size={24} />
                              </div>
                              <div>
                                <h4 className="font-bold text-[var(--admin-text)]">{account.username}</h4>
                                <div className="mt-1 flex items-center gap-3">
                                  <span className="text-[10px] font-bold uppercase tracking-widest text-cyan-400">{account.role}</span>
                                  <span className={`rounded-full px-2 py-0.5 text-[9px] font-bold uppercase tracking-wider ${account.is_active ? 'bg-emerald-500/10 text-emerald-400' : 'bg-rose-500/10 text-rose-400'}`}>
                                    {account.is_active ? 'Active' : 'Inactive'}
                                  </span>
                                  <span className="text-[10px] text-[var(--admin-text-muted)]">Added {formatDate(account.created_at)}</span>
                                </div>
                              </div>
                            </div>
                            {account.username !== currentAdminUsername && (
                              <button
                                onClick={() => deleteAccount(account.username)}
                                disabled={role !== 'root' && (account.role === 'admin' || account.role === 'root')}
                                className="rounded-none p-2 text-[var(--admin-text-muted)] transition-colors hover:bg-rose-500/10 hover:text-rose-500 disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-[var(--admin-text-muted)]"
                              >
                                <Trash2 size={20} />
                              </button>
                            )}
                          </div>
                        </div>
                      ))}
                      {accountLimit !== 'all' && accountTotalPages > 1 && (
                        <div className={`${surfaceCardClass} flex items-center justify-between p-4`}>
                          <p className="text-sm text-[var(--admin-text-soft)]">
                            Page {accountPage} of {accountTotalPages}
                          </p>
                          <div className="flex gap-2">
                            <button
                              onClick={() => setAccountPage((page) => Math.max(1, page - 1))}
                              disabled={accountPage <= 1}
                              className={`${actionButtonClass} bg-[var(--admin-muted-surface)] text-[var(--admin-text)] hover:bg-[var(--admin-muted-surface-hover)]`}
                            >
                              Previous
                            </button>
                            <button
                              onClick={() => setAccountPage((page) => Math.min(accountTotalPages, page + 1))}
                              disabled={accountPage >= accountTotalPages}
                              className={`${actionButtonClass} bg-[var(--admin-muted-surface)] text-[var(--admin-text)] hover:bg-[var(--admin-muted-surface-hover)]`}
                            >
                              Next
                            </button>
                          </div>
                        </div>
                      )}
                    </div>
                  </div>
                )}
              </motion.div>
            </AnimatePresence>
          </div>
        </main>
      </div>

      {/* Ban Modal */}
      <AnimatePresence>
        {banModal.open && (
          <div className="fixed inset-0 z-[100] flex items-center justify-center p-4 sm:p-6">
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              onClick={closeBanModal}
              className="absolute inset-0 bg-[var(--admin-bg)]/90 backdrop-blur-sm"
            />

            <motion.div
              initial={{ opacity: 0, scale: 0.95, y: 20 }}
              animate={{ opacity: 1, scale: 1, y: 0 }}
              exit={{ opacity: 0, scale: 0.95, y: 20 }}
              transition={{ type: 'spring', stiffness: 350, damping: 30 }}
              className="relative flex flex-col w-full max-w-[560px] max-h-[min(85vh,760px)] rounded-none border border-[var(--admin-outline-soft)] bg-[var(--admin-surface-bg)] shadow-[0_32px_128px_rgba(0,0,0,0.8)] overflow-hidden"
            >
              <div className="hud-bracket hud-bracket-tl opacity-20" />
              <div className="hud-bracket-tr opacity-20" />
              <div className="hud-bracket-bl opacity-20" />
              <div className="hud-bracket-br opacity-20" />

              {/* Header */}
              <div className="px-6 py-5 sm:px-8 sm:py-6 border-b border-[var(--admin-outline-soft)] flex items-start gap-5">
                <div className="w-12 h-12 rounded-none bg-danger-rose/10 border border-danger-rose/20 flex items-center justify-center shrink-0 shadow-[0_0_20px_rgba(244,63,94,0.15)]">
                  <BanIcon size={22} className="text-danger-rose" />
                </div>
                <div className="flex flex-col">
                  <h3 className="font-heading text-lg font-bold text-[var(--admin-text)] tracking-wide uppercase">CONFIRM_RESTRICTION</h3>
                  <p className="font-heading text-sm text-[var(--admin-text-muted)] font-medium tracking-normal mt-0.5">Session-targeted enforcement also carries the linked IP when it is available.</p>
                </div>
              </div>

              {/* Body */}
              <div className="flex-1 overflow-y-auto px-6 py-6 sm:px-8 space-y-6">
                {/* Target Details */}
                <div className="rounded-none border border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] px-5 py-4">
                  <p className="font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)] mb-3">TARGET_IDENTITY_DATA</p>
                  <div className="space-y-2 text-left">
                    {banModal.sessionId && (
                      <div className="flex flex-col sm:flex-row sm:items-start gap-1 sm:gap-4">
                        <span className="sm:w-[72px] shrink-0 font-heading text-[10px] font-bold tracking-wider text-[var(--admin-text-muted)] uppercase">SESSION</span>
                        <span className="font-mono text-xs text-[var(--admin-text)] break-all leading-relaxed">{banModal.sessionId}</span>
                      </div>
                    )}
                    {banModal.ip && (
                      <div className="flex flex-col sm:flex-row sm:items-start gap-1 sm:gap-4">
                        <span className="sm:w-[72px] shrink-0 font-heading text-[10px] font-bold tracking-wider text-[var(--admin-text-muted)] uppercase">NETWORK</span>
                        <span className="font-mono text-xs text-[var(--admin-text)] break-all leading-relaxed">{banModal.ip}</span>
                      </div>
                    )}
                  </div>
                </div>

                {/* Ban Type Selector */}
                <div className="flex flex-col gap-3">
                  {banModal.sessionId && banModal.ip && (
                    <div className="flex flex-col gap-3">
                      <p className="font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">TARGET_SCOPE</p>
                      <div className="flex gap-1.5 rounded-none bg-[var(--admin-muted-surface)] p-1.5 border border-[var(--admin-outline-soft)]">
                        <button
                          type="button"
                          onClick={() => setBanModal({ ...banModal, target: 'session' })}
                          className={segmentedToggleButtonClass(banModal.target === 'session')}
                        >
                          SESSION_PLUS_IP
                        </button>
                        <button
                          type="button"
                          onClick={() => setBanModal({ ...banModal, target: 'ip' })}
                          className={segmentedToggleButtonClass(banModal.target === 'ip')}
                        >
                          IP_ONLY
                        </button>
                      </div>
                    </div>
                  )}
                  <p className="font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">ENFORCEMENT_MODE</p>
                  <div className="flex gap-1.5 rounded-none bg-[var(--admin-muted-surface)] p-1.5 border border-[var(--admin-outline-soft)]">
                    <button
                      type="button"
                      onClick={() => setBanModal({ ...banModal, mode: 'permanent' })}
                      className={segmentedToggleButtonClass(banModal.mode === 'permanent')}
                    >
                      PERMANENT_BAN
                    </button>
                    <button
                      type="button"
                      onClick={() => setBanModal({ ...banModal, mode: 'temporary' })}
                      className={segmentedToggleButtonClass(banModal.mode === 'temporary')}
                    >
                      TEMPORARY_BAN
                    </button>
                  </div>
                </div>

                {/* Duration Configuration */}
                <AnimatePresence initial={false}>
                  {banModal.mode === 'temporary' && (
                    <motion.div
                      initial={{ height: 0, opacity: 0 }}
                      animate={{ height: 'auto', opacity: 1 }}
                      exit={{ height: 0, opacity: 0 }}
                      transition={{ duration: 0.25, ease: "easeOut" }}
                      className="overflow-hidden"
                    >
                      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 pb-2">
                        <div className="flex flex-col gap-2">
                          <label className="font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">DURATION_VALUE</label>
                          <input
                            type="number"
                            min="1"
                            value={banModal.durationValue}
                            onChange={(e) => setBanModal({ ...banModal, durationValue: e.target.value })}
                            className={inputClass}
                            placeholder="AMOUNT"
                          />
                        </div>
                        <div className="flex flex-col gap-2">
                          <label className="font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">TEMPORAL_UNIT</label>
                          <select
                            value={banModal.durationUnit}
                            onChange={(e) => setBanModal({ ...banModal, durationUnit: e.target.value as 'hours' | 'days' })}
                            className={compactSelectClass}
                          >
                            <option value="hours">HOURS (H)</option>
                            <option value="days">DAYS (D)</option>
                          </select>
                        </div>
                      </div>
                    </motion.div>
                  )}
                </AnimatePresence>

                {/* Incident Justification */}
                <div className="flex flex-col gap-3">
                  <p className="font-heading text-[10px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text-muted)]">INCIDENT_JUSTIFICATION</p>
                  <textarea
                    value={banModal.reason}
                    onChange={(e) => setBanModal({ ...banModal, reason: e.target.value })}
                    className="w-full min-h-[160px] sm:min-h-[120px] max-h-[200px] resize-y rounded-none border border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] px-4 py-4 font-heading text-[16px] sm:text-sm text-[var(--admin-text)] placeholder-[var(--admin-text-muted)] outline-none transition-all duration-200 focus:border-electric-cyan/50 focus:ring-2 focus:ring-electric-cyan/15 scanlines"
                    placeholder="PROVIDE DETAILED LOGS OR REASONING..."
                  />
                </div>
              </div>

              {/* Footer */}
              <div className="shrink-0 px-6 py-5 sm:px-10 sm:py-6 border-t border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] flex flex-col sm:flex-row gap-3">
                <button
                  type="button"
                  onClick={closeBanModal}
                  className="shrink-0 sm:flex-1 w-full h-14 md:h-14 sm:h-14 rounded-none bg-[var(--admin-muted-surface)] border border-[var(--admin-outline-soft)] text-base sm:text-xs font-bold text-[var(--admin-text)] uppercase tracking-widest hover:bg-[var(--admin-muted-surface)] hover:text-[var(--admin-text)] transition-all active:scale-[0.98]"
                >
                  CANCEL_OPERATION
                </button>
                <button
                  type="button"
                  onClick={handleBanModalSubmit}
                  disabled={submittingBan}
                  className="shrink-0 sm:flex-1 w-full h-14 md:h-14 sm:h-14 rounded-none bg-danger-rose text-[var(--admin-text)] text-base sm:text-xs font-bold uppercase tracking-widest hover:bg-rose-600 shadow-[0_0_24px_rgba(244,63,94,0.25)] hover:shadow-[0_0_32px_rgba(244,63,94,0.4)] transition-all active:scale-[0.98] disabled:opacity-70 disabled:cursor-not-allowed"
                >
                  {submittingBan ? 'PROCESSING...' : 'CONFIRM_ENFORCEMENT'}
                </button>
              </div>
            </motion.div>
          </div>
        )}
      </AnimatePresence>

      {/* Transcript Modal */}
      <AnimatePresence>
        {expandedReport && (
          <div className="fixed inset-0 z-[100] flex items-center justify-center p-4 sm:p-6">
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              onClick={() => setExpandedReport(null)}
              className="absolute inset-0 bg-[var(--admin-bg)]/90 backdrop-blur-md"
            />

            <motion.div
              initial={{ opacity: 0, scale: 0.95, y: 20 }}
              animate={{ opacity: 1, scale: 1, y: 0 }}
              exit={{ opacity: 0, scale: 0.95, y: 20 }}
              transition={{ type: 'spring', stiffness: 350, damping: 30 }}
              className="relative flex flex-col w-full max-w-[640px] max-h-[85vh] rounded-none border border-[var(--admin-surface-border)] bg-[var(--admin-bg)] shadow-[0_32px_128px_rgba(0,0,0,0.8)] overflow-hidden"
            >
              <div className="hud-bracket hud-bracket-tl opacity-20" />
              <div className="hud-bracket-tr opacity-20" />
              <div className="hud-bracket-bl opacity-20" />
              <div className="hud-bracket-br opacity-20" />

              {/* Header */}
              <div className="flex items-center justify-between border-b border-[var(--admin-surface-border)] px-6 py-5 sm:px-8 sm:py-6" style={{ background: 'var(--admin-transcript-header-bg)' }}>
                <div className="flex items-center gap-4">
                  <div className="w-10 h-10 rounded-none bg-electric-cyan/10 border border-electric-cyan/20 flex items-center justify-center shrink-0 shadow-[0_0_15px_rgba(34,211,238,0.1)]">
                    <MessageSquare size={18} className="text-electric-cyan" />
                  </div>
                  <div className="flex flex-col">
                    <h3 className="font-heading text-lg font-bold text-[var(--admin-text)] tracking-wide uppercase">TRANSCRIPT_VIEW</h3>
                    <p className="font-heading text-[10px] text-[var(--admin-text-muted)] font-bold tracking-widest uppercase mt-0.5">
                      Session: {reports.find(r => r.id === expandedReport)?.reported_session_id.substring(0, 8)}...
                    </p>
                  </div>
                </div>
                <button
                  onClick={() => setExpandedReport(null)}
                  className="flex h-10 w-10 items-center justify-center rounded-none border border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] text-[var(--admin-text-soft)] transition-all hover:bg-[var(--admin-muted-surface-hover)] hover:text-[var(--admin-text)]"
                >
                  <XCircle size={18} />
                </button>
              </div>

              {/* Body */}
              <div className="flex-1 overflow-y-auto px-6 py-6 sm:px-8" style={{ background: 'var(--admin-transcript-bg)' }}>
                <div className="space-y-4">
                  {reports.find(r => r.id === expandedReport)?.chat_log.map((msg, idx) => (
                    <div key={idx} className={`flex ${msg.sender === 'me' ? 'justify-end' : msg.sender === 'system' ? 'justify-center' : 'justify-start'}`}>
                      <div
                        className={`max-w-[85%] rounded-none px-4 py-3 text-sm shadow-sm ${msg.sender === 'me'
                          ? ''
                          : msg.sender === 'system'
                            ? 'text-center text-[11px] px-6 py-1.5'
                            : ''
                          }`}
                        style={
                          msg.sender === 'me'
                            ? {
                              background: 'var(--admin-transcript-bubble-reporter-bg)',
                              borderColor: 'var(--admin-transcript-bubble-reporter-border)',
                              color: 'var(--admin-transcript-bubble-reporter-text)',
                            }
                            : msg.sender === 'peer'
                              ? {
                                background: 'var(--admin-transcript-bubble-peer-bg)',
                                borderColor: 'var(--admin-transcript-bubble-peer-border)',
                                color: 'var(--admin-transcript-bubble-peer-text)',
                              }
                              : msg.sender === 'system'
                                ? {
                                  background: 'var(--admin-transcript-bubble-system-bg)',
                                  borderColor: 'var(--admin-transcript-bubble-system-border)',
                                  color: 'var(--admin-transcript-bubble-system-text)',
                                }
                                : undefined
                        }
                      >
                        {msg.sender !== 'system' && (
                          <p className="mb-1.5 text-[10px] font-bold uppercase tracking-wider" style={{ color: 'var(--admin-transcript-label)' }}>
                            {msg.sender === 'me' ? 'Reporter' : 'Peer'}
                          </p>
                        )}
                        <div className="leading-relaxed">
                          {DOMPurify.sanitize(msg.text)}
                        </div>
                      </div>
                    </div>
                  ))}

                  {(!reports.find(r => r.id === expandedReport)?.chat_log || reports.find(r => r.id === expandedReport)?.chat_log.length === 0) && (
                    <div className="flex flex-col items-center justify-center py-12 text-[var(--admin-text-muted)] italic">
                      <MessageSquare size={48} className="opacity-10 mb-4" />
                      <p>No messages available for this session.</p>
                    </div>
                  )}
                </div>
              </div>

              {/* Footer */}
              <div className="border-t border-[var(--admin-surface-border)] bg-[var(--admin-muted-surface)] px-6 py-4 sm:px-8">
                <button
                  onClick={() => setExpandedReport(null)}
                  className="h-11 w-full rounded-none border border-[var(--admin-outline-soft)] bg-[var(--admin-input-bg)] text-xs font-bold uppercase tracking-widest text-[var(--admin-text)] transition-all hover:bg-[var(--admin-input-focus-bg)] active:scale-[0.98]"
                >
                  DISMISS_VIEW
                </button>
              </div>
            </motion.div>
          </div>
        )}
      </AnimatePresence>
      <AnimatePresence>
        {isRedisModalOpen && (
          <div className="fixed inset-0 z-[100] flex items-center justify-center p-4 sm:p-6">
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              onClick={() => setIsRedisModalOpen(false)}
              className="absolute inset-0 bg-[var(--admin-bg)]/90 backdrop-blur-md"
            />

            <motion.div
              initial={{ opacity: 0, scale: 0.95, y: 20 }}
              animate={{ opacity: 1, scale: 1, y: 0 }}
              exit={{ opacity: 0, scale: 0.95, y: 20 }}
              transition={{ type: 'spring', stiffness: 350, damping: 30 }}
              className="relative flex max-h-[85vh] w-full max-w-[1100px] flex-col overflow-hidden rounded-none border border-[var(--admin-surface-border)] bg-[var(--admin-bg)] shadow-[0_32px_128px_rgba(0,0,0,0.8)]"
            >
              <div className="hud-bracket hud-bracket-tl opacity-20" />
              <div className="hud-bracket-tr opacity-20" />
              <div className="hud-bracket hud-bracket-bl opacity-20" />
              <div className="hud-bracket-br opacity-20" />

              <div className="flex items-center justify-between border-b border-[var(--admin-surface-border)] px-6 py-5 sm:px-8 sm:py-6" style={{ background: 'var(--admin-transcript-header-bg)' }}>
                <div className="flex items-center gap-4">
                  <div className="flex h-10 w-10 items-center justify-center rounded-none border border-electric-cyan/20 bg-electric-cyan/10 text-electric-cyan shadow-[0_0_15px_rgba(34,211,238,0.1)]">
                    <Database size={18} />
                  </div>
                  <div className="flex flex-col">
                    <h3 className="font-heading text-lg font-bold uppercase tracking-wide text-[var(--admin-text)]">REDIS_CLUSTER_DETAILS</h3>
                    <p className="mt-0.5 font-heading text-[10px] font-bold uppercase tracking-widest text-[var(--admin-text-muted)]">
                      Nodes: {infraHealth?.redis.nodes.length || 0} · Cluster state: {infraHealth?.redis.cluster.state || 'unknown'}
                    </p>
                  </div>
                </div>
                <button
                  onClick={() => setIsRedisModalOpen(false)}
                  className="flex h-10 w-10 items-center justify-center rounded-none border border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] text-[var(--admin-text-soft)] transition-all hover:bg-[var(--admin-muted-surface-hover)] hover:text-[var(--admin-text)]"
                >
                  <XCircle size={18} />
                </button>
              </div>

              <div className="flex-1 overflow-y-auto px-6 py-6 sm:px-8" style={{ background: 'var(--admin-transcript-bg)' }}>
                <div className="space-y-4">
                  {(infraHealth?.redis.nodes || []).map((node) => (
                    <div key={node.node_id || node.address} className="detail-panel rounded-none p-5 scanlines">
                      <div className="flex flex-col gap-3 border-b border-[var(--admin-outline-soft)] pb-4 sm:flex-row sm:items-start sm:justify-between">
                        <div>
                          <p className="font-mono text-sm text-[var(--admin-text)]">{node.address}</p>
                          <p className="mt-1 break-all text-[11px] text-[var(--admin-text-muted)]">{node.node_id}</p>
                        </div>
                        <div className="flex flex-wrap gap-2">
                          <span className={`px-2 py-1 text-[10px] font-bold uppercase tracking-[0.14em] ${node.role === 'master'
                            ? 'border border-electric-cyan/20 bg-electric-cyan/10 text-electric-cyan'
                            : 'border border-[var(--admin-outline-strong)] bg-[var(--admin-muted-surface)] text-[var(--admin-text)]'
                            }`}>
                            {node.role}
                          </span>
                          <span className={`px-2 py-1 text-[10px] font-bold uppercase tracking-[0.14em] ${healthStatusClass(node.status)}`}>
                            {node.status}
                          </span>
                        </div>
                      </div>

                      <div className="mt-4 grid gap-6 lg:grid-cols-2">
                        <div className="space-y-3 text-xs text-[var(--admin-text-soft)]">
                          <h4 className="text-[11px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text)]">Topology</h4>
                          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                            <p>Link state: {node.link_state || 'unknown'}</p>
                            <p>Master ID: {node.master_id || 'self'}</p>
                            <p>Master link: {node.master_link_status || 'n/a'}</p>
                            <p>Replication lag: {node.replication_lag_seconds ?? 0}s</p>
                          </div>
                          {node.flags?.length > 0 && (
                            <p className="break-all text-[11px] text-[var(--admin-text-muted)]">Flags: {node.flags.join(', ')}</p>
                          )}
                          {node.slots && node.slots.length > 0 && (
                            <p className="break-all text-[11px] text-[var(--admin-text-muted)]">Slots: {node.slots.join(' ')}</p>
                          )}
                          {node.error && (
                            <p className="break-all text-[11px] text-rose-400">{node.error}</p>
                          )}
                        </div>

                        <div className="space-y-3 text-xs text-[var(--admin-text-soft)]">
                          <h4 className="text-[11px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text)]">Memory</h4>
                          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                            <p>Used: {node.memory.used_memory_human || formatBytes(node.memory.used_memory_bytes)}</p>
                            <p>RSS: {node.memory.used_memory_rss_human || formatBytes(node.memory.used_memory_rss_bytes)}</p>
                            <p>Peak: {node.memory.used_memory_peak_human || formatBytes(node.memory.used_memory_peak_bytes)}</p>
                            <p>Dataset: {formatBytes(node.memory.used_memory_dataset_bytes)}</p>
                            <p>Fragmentation ratio: {node.memory.fragmentation_ratio ? node.memory.fragmentation_ratio.toFixed(2) : '0.00'}</p>
                            <p>Fragmentation bytes: {formatBytes(node.memory.fragmentation_bytes)}</p>
                            <p>Max memory: {node.memory.maxmemory_human || formatBytes(node.memory.maxmemory_bytes)}</p>
                            <p>Policy: {node.memory.maxmemory_policy || 'unknown'}</p>
                            <p>Total system: {node.memory.total_system_memory_human || formatBytes(node.memory.total_system_memory_bytes)}</p>
                            <p>Allocator: {node.memory.allocator || 'unknown'}</p>
                          </div>
                        </div>
                      </div>

                      <div className="mt-5">
                        <h4 className="mb-3 text-[11px] font-bold uppercase tracking-[0.14em] text-[var(--admin-text)]">Top Command Totals Since Start</h4>
                        <div className="space-y-2">
                          {topRedisCommandStats(node).length > 0 ? topRedisCommandStats(node).map((command) => (
                            <div key={`${node.node_id}-${command.command}`} className="flex items-center justify-between gap-3 border border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] px-3 py-2 text-xs text-[var(--admin-text-soft)]">
                              <span className="font-mono text-[var(--admin-text)]">{command.command}</span>
                              <div className="flex flex-wrap items-center justify-end gap-3 text-right">
                                <span>{command.calls.toLocaleString()} calls</span>
                                <span>{command.usec_total.toLocaleString()} us total</span>
                                <span>{command.usec_per_call.toFixed(2)} us/call</span>
                              </div>
                            </div>
                          )) : (
                            <p className="text-xs text-[var(--admin-text-muted)]">No command stats available for this node.</p>
                          )}
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              </div>

              <div className="border-t border-[var(--admin-surface-border)] bg-[var(--admin-muted-surface)] px-6 py-4 sm:px-8">
                <button
                  onClick={() => setIsRedisModalOpen(false)}
                  className="h-11 w-full rounded-none border border-[var(--admin-outline-soft)] bg-[var(--admin-input-bg)] text-xs font-bold uppercase tracking-widest text-[var(--admin-text)] transition-all hover:bg-[var(--admin-input-focus-bg)] active:scale-[0.98]"
                >
                  CLOSE_REDIS_VIEW
                </button>
              </div>
            </motion.div>
          </div>
        )}
      </AnimatePresence>
      {/* Description Modal */}
      <AnimatePresence>
        {viewingDescription && (
          <div className="fixed inset-0 z-[100] flex items-center justify-center p-4 sm:p-6">
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              onClick={() => setViewingDescription(null)}
              className="absolute inset-0 bg-[var(--admin-bg)]/90 backdrop-blur-md"
            />

            <motion.div
              initial={{ opacity: 0, scale: 0.95, y: 20 }}
              animate={{ opacity: 1, scale: 1, y: 0 }}
              exit={{ opacity: 0, scale: 0.95, y: 20 }}
              transition={{ type: 'spring', stiffness: 350, damping: 30 }}
              className="relative flex flex-col w-full max-w-[640px] max-h-[80vh] rounded-none border border-[var(--admin-surface-border)] bg-[var(--admin-bg)] shadow-[0_32px_128px_rgba(0,0,0,0.8)] overflow-hidden"
            >
              <div className="hud-bracket hud-bracket-tl opacity-20" />
              <div className="hud-bracket-tr opacity-20" />
              <div className="hud-bracket-bl opacity-20" />
              <div className="hud-bracket-br opacity-20" />

              {/* Header */}
              <div className="flex items-center justify-between border-b border-[var(--admin-surface-border)] px-6 py-5 sm:px-8 sm:py-6" style={{ background: 'var(--admin-transcript-header-bg)' }}>
                <div className="flex items-center gap-4">
                  <div className="w-10 h-10 rounded-none bg-electric-cyan/10 border border-electric-cyan/20 flex items-center justify-center shrink-0 shadow-[0_0_15px_rgba(34,211,238,0.1)]">
                    <Shield size={18} className="text-electric-cyan" />
                  </div>
                  <div className="flex flex-col">
                    <h3 className="font-heading text-lg font-bold text-[var(--admin-text)] tracking-wide uppercase">INCIDENT_DESCRIPTION</h3>
                    <p className="font-heading text-[10px] text-[var(--admin-text-muted)] font-bold tracking-widest uppercase mt-0.5">
                      Report Ref: {reports.find(r => r.id === viewingDescription)?.id.substring(0, 8)}...
                    </p>
                  </div>
                </div>
                <button
                  onClick={() => setViewingDescription(null)}
                  className="flex h-10 w-10 items-center justify-center rounded-none border border-[var(--admin-outline-soft)] bg-[var(--admin-muted-surface)] text-[var(--admin-text-soft)] transition-all hover:bg-[var(--admin-muted-surface-hover)] hover:text-[var(--admin-text)]"
                >
                  <XCircle size={18} />
                </button>
              </div>

              {/* Body */}
              <div className="flex-1 overflow-y-auto px-6 py-8 sm:px-8" style={{ background: 'var(--admin-transcript-bg)' }}>
                <div className="detail-panel rounded-none p-6 scanlines">
                  <p className="text-base text-[var(--admin-text)] leading-relaxed break-words whitespace-pre-wrap font-medium">
                    {reports.find(r => r.id === viewingDescription)?.description}
                  </p>
                </div>
              </div>

              {/* Footer */}
              <div className="border-t border-[var(--admin-surface-border)] bg-[var(--admin-muted-surface)] px-6 py-4 sm:px-8">
                <button
                  onClick={() => setViewingDescription(null)}
                  className="h-11 w-full rounded-none border border-[var(--admin-outline-soft)] bg-[var(--admin-input-bg)] text-xs font-bold uppercase tracking-widest text-[var(--admin-text)] transition-all hover:bg-[var(--admin-input-focus-bg)] active:scale-[0.98]"
                >
                  CLOSE_ENTRY
                </button>
              </div>
            </motion.div>
          </div>
        )}
      </AnimatePresence>
    </div>
  );
}
