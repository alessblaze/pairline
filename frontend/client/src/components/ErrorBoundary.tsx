// Pairline - Open Source Video Chat and Matchmaking
// Copyright (C) 2026 Albert Blasczykowski
// Aless Microsystems
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

import { Component, type ErrorInfo, type ReactNode } from 'react';
import unavailableGif from '../assets/unavailable.gif';

interface Props {
  children?: ReactNode;
}

interface State {
  hasError: boolean;
  error: Error | null;
}

export class ErrorBoundary extends Component<Props, State> {
  public state: State = {
    hasError: false,
    error: null
  };

  public static getDerivedStateFromError(error: Error): State {
    // Update state so the next render will show the fallback UI.
    return { hasError: true, error };
  }

  public componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    console.error('Uncaught component error:', error, errorInfo);
  }

  private handleReturnHome = () => {
    this.setState({ hasError: false, error: null });
    // Full page reload will clear any stubborn stuck states/sockets
    window.location.href = '/'; 
  };

  public render() {
    if (this.state.hasError) {
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
          <div className="w-full max-w-lg p-6 sm:p-10 flex flex-col items-center text-center bg-white dark:bg-gray-900 rounded-3xl shadow-2xl border border-gray-200 dark:border-gray-800/60 transition-all">
            <div className="mb-2 relative flex items-center justify-center">
              <img
                src={unavailableGif}
                alt="Error Anime Character"
                className="w-40 h-40 sm:w-48 sm:h-48 object-contain drop-shadow-[0_15px_15px_rgba(99,102,241,0.25)] dark:drop-shadow-[0_15px_15px_rgba(99,102,241,0.15)]"
              />
            </div>
            <div className="mb-5 h-8 sm:h-10 flex items-center justify-center">
              <p className="typing-text text-xl sm:text-2xl font-bold tracking-wide">
                やめてください
              </p>
            </div>
            <h2 className="text-2xl sm:text-3xl font-extrabold text-gray-900 dark:text-white mb-3 tracking-tight">Something went wrong</h2>
            <p className="text-base sm:text-lg text-gray-500 dark:text-gray-400 mb-8 leading-relaxed font-medium">
              {this.state.error?.message || "An unexpected critical error occurred while running the application."}
            </p>
            <button
              onClick={this.handleReturnHome}
              className="w-full py-3.5 sm:py-4 bg-indigo-600 hover:bg-indigo-700 active:scale-[0.98] text-white font-bold rounded-xl transition-all shadow-lg shadow-indigo-500/25 text-base sm:text-lg flex items-center justify-center gap-2"
            >
              <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6" />
              </svg>
              Return Home
            </button>
          </div>
        </div>
      );
    }

    return this.props.children;
  }
}
