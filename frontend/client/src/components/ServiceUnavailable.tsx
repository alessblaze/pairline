import { useNavigate } from 'react-router-dom';
import { useNetworkHealth } from '../hooks/useNetworkHealth';
import { ThemeToggle } from './ThemeToggle';

export function ServiceUnavailable() {
  const navigate = useNavigate();
  const { resetHealthStatus } = useNetworkHealth();

  const handleRetry = () => {
    resetHealthStatus();
    navigate('/', { replace: true });
  };

  return (
    <div className="fixed inset-0 flex items-center justify-center p-2 sm:p-4 bg-gradient-to-br from-gray-50 to-gray-100 dark:from-gray-900 dark:to-gray-800">
      <div className="absolute top-4 right-4">
        <ThemeToggle />
      </div>
      <div className="w-full max-w-lg p-6 sm:p-10 flex flex-col items-center text-center bg-white dark:bg-gray-900 rounded-3xl shadow-2xl border border-gray-200 dark:border-gray-800/60 transition-all">
        <div className="w-20 h-20 sm:w-24 sm:h-24 bg-red-100 dark:bg-red-900/30 rounded-full flex items-center justify-center mb-6 animate-pulse">
          <svg className="w-10 h-10 sm:w-12 sm:h-12 text-red-500 dark:text-red-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
          </svg>
        </div>
        <h2 className="text-2xl sm:text-3xl font-extrabold text-gray-900 dark:text-white mb-3 tracking-tight">Service Unavailable</h2>
        <p className="text-base sm:text-lg text-gray-500 dark:text-gray-400 mb-8 leading-relaxed font-medium">
          We're having trouble connecting to the Pairline network. Our servers might be undergoing maintenance or dropping connections.
        </p>
        <button
          onClick={handleRetry}
          className="w-full py-3.5 sm:py-4 bg-indigo-600 hover:bg-indigo-700 active:scale-[0.98] text-white font-bold rounded-xl transition-all shadow-lg shadow-indigo-500/25 text-base sm:text-lg flex items-center justify-center gap-2"
        >
          <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
          Retry Connection
        </button>
      </div>
    </div>
  );
}
