import { useNavigate } from 'react-router-dom';
import { useNetworkHealth } from '../hooks/useNetworkHealth';
import { ThemeToggle } from './ThemeToggle';
import unavailableGif from '../assets/unavailable.gif';

export function ServiceUnavailable() {
  const navigate = useNavigate();
  const { resetHealthStatus } = useNetworkHealth();

  const handleRetry = () => {
    resetHealthStatus();
    navigate('/', { replace: true });
  };

  return (
    <div className="fixed inset-0 flex items-center justify-center p-2 sm:p-4 bg-gradient-to-br from-gray-50 to-gray-100 dark:from-gray-900 dark:to-gray-800">
      <style>{`
        @keyframes typing {
          from { width: 0 }
          to { width: 17ch }
        }
        @keyframes blink-caret {
          from, to { border-color: transparent }
          50% { border-color: currentColor; }
        }
        .typing-text {
          overflow: hidden;
          white-space: nowrap;
          border-right: 0.15em solid;
          width: 17ch;
          margin: 0 auto;
          animation: 
            typing 2s steps(17, end),
            blink-caret .75s step-end infinite;
          background: linear-gradient(135deg, #6366f1, #a855f7, #ec4899);
          -webkit-background-clip: text;
          -webkit-text-fill-color: transparent;
          background-clip: text;
          filter: drop-shadow(0 0 12px rgba(99, 102, 241, 0.3));
        }
      `}</style>
      <div className="absolute top-4 right-4">
        <ThemeToggle />
      </div>
      <div className="w-full max-w-lg p-6 sm:p-10 flex flex-col items-center text-center bg-white dark:bg-gray-900 rounded-3xl shadow-2xl border border-gray-200 dark:border-gray-800/60 transition-all">
        <div className="mb-2 relative flex items-center justify-center">
          <img
            src={unavailableGif}
            alt="Service Unavailable Anime Character"
            className="w-40 h-40 sm:w-48 sm:h-48 object-contain drop-shadow-[0_15px_15px_rgba(99,102,241,0.25)] dark:drop-shadow-[0_15px_15px_rgba(99,102,241,0.15)]"
          />
        </div>
        <div className="mb-5 h-8 sm:h-10 flex items-center justify-center">
          <p className="typing-text text-xl sm:text-2xl font-bold tracking-wide">
            やめてください
          </p>
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
