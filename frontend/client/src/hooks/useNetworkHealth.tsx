import { createContext, useContext, useState, useLayoutEffect, useCallback, useMemo } from 'react';
import type { ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';

export type ServiceStatus = 'ok' | 'degraded' | 'offline';

interface NetworkHealthContextType {
  phoenixStatus: ServiceStatus;
  apiStatus: ServiceStatus;
  setChannelStatus: (channel: string, status: ServiceStatus) => void;
  removeChannel: (channel: string) => void;
  setApiStatus: (status: ServiceStatus) => void;
  resetHealthStatus: () => void;
}

const NetworkHealthContext = createContext<NetworkHealthContextType | undefined>(undefined);

interface NetworkHealthProviderProps {
  children: ReactNode;
  /** Seed channel statuses for Storybook — omit in production. */
  initialChannelStatuses?: Record<string, ServiceStatus>;
  /** Seed API status for Storybook — omit in production. */
  initialApiStatus?: ServiceStatus;
}

export function NetworkHealthProvider({
  children,
  initialChannelStatuses,
  initialApiStatus,
}: NetworkHealthProviderProps) {
  const [channelStatuses, setChannelStatuses] = useState<Record<string, ServiceStatus>>(initialChannelStatuses ?? {});
  const [apiStatus, setApiStatus] = useState<ServiceStatus>(initialApiStatus ?? 'ok');
  const navigate = useNavigate();

  // Derive aggregate Phoenix status from individual channel statuses.
  // Worst-status-wins: offline > degraded > ok.
  const phoenixStatus: ServiceStatus = useMemo(() => {
    const statuses = Object.values(channelStatuses);
    if (statuses.length === 0) return 'ok';
    if (statuses.some(s => s === 'offline')) return 'offline';
    if (statuses.some(s => s === 'degraded')) return 'degraded';
    return 'ok';
  }, [channelStatuses]);

  const setChannelStatus = useCallback((channel: string, status: ServiceStatus) => {
    setChannelStatuses(prev => {
      if (prev[channel] === status) return prev;
      return { ...prev, [channel]: status };
    });
  }, []);

  // Remove a channel from tracking (e.g. on component unmount).
  // This prevents stale 'degraded' state from persisting after navigation.
  const removeChannel = useCallback((channel: string) => {
    setChannelStatuses(prev => {
      if (!(channel in prev)) return prev;
      const next = { ...prev };
      delete next[channel];
      return next;
    });
  }, []);

  const resetHealthStatus = useCallback(() => {
    setChannelStatuses({});
    setApiStatus('ok');
  }, []);

  // useLayoutEffect avoids a visible frame flicker when cascading from
  // apiStatus offline -> phoenixStatus offline -> redirect.
  useLayoutEffect(() => {
    if (phoenixStatus === 'offline') {
      navigate('/unavailable', { replace: true });
    }
  }, [phoenixStatus, navigate]);

  return (
    <NetworkHealthContext.Provider
      value={{
        phoenixStatus,
        apiStatus,
        setChannelStatus,
        removeChannel,
        setApiStatus,
        resetHealthStatus,
      }}
    >
      {/* Degraded Status Banner */}
      {(phoenixStatus === 'degraded' || apiStatus === 'degraded') && phoenixStatus !== 'offline' && apiStatus !== 'offline' && (
        <div className="fixed top-0 left-0 right-0 z-[100] flex justify-center p-2 sm:p-4 pointer-events-none animate-in slide-in-from-top-10 duration-300">
          <div className="bg-orange-100 hover:bg-orange-200 dark:bg-orange-900/80 dark:hover:bg-orange-900 border border-orange-300 dark:border-orange-800 text-orange-800 dark:text-orange-200 font-semibold px-4 py-2 rounded-xl shadow-lg flex items-center gap-3 pointer-events-auto transition-colors backdrop-blur-md">
            <div className="w-5 h-5 rounded-full border-2 border-orange-500 border-t-transparent animate-spin shrink-0" />
            <span className="text-sm shadow-sm font-medium">
              {phoenixStatus === 'degraded' && apiStatus === 'degraded' 
                ? 'Reconnecting to network services...'
                : phoenixStatus === 'degraded' 
                  ? 'Reconnecting to chat server...'
                  : 'Reconnecting to signaling server...'
              }
            </span>
          </div>
        </div>
      )}
      {/* API Offline Banner */}
      {apiStatus === 'offline' && phoenixStatus !== 'offline' && (
        <div className="fixed top-0 left-0 right-0 z-[100] flex justify-center p-2 sm:p-4 pointer-events-none animate-in slide-in-from-top-10 duration-300">
          <div className="bg-red-100 dark:bg-red-900/80 border border-red-300 dark:border-red-800 text-red-800 dark:text-red-200 font-semibold px-4 py-2 rounded-xl shadow-lg flex items-center gap-3 pointer-events-auto backdrop-blur-md">
            <svg className="w-5 h-5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
            </svg>           
            <span className="text-sm shadow-sm font-medium">
              Video services offline. Text chat is still available.
            </span>
          </div>
        </div>
      )}
      {children}
    </NetworkHealthContext.Provider>
  );
}

export function useNetworkHealth() {
  const context = useContext(NetworkHealthContext);
  if (!context) {
    throw new Error('useNetworkHealth must be used within a NetworkHealthProvider');
  }
  return context;
}
