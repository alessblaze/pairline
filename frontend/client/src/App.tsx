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

import { Routes, Route, Navigate } from 'react-router-dom';
import { LandingPage } from './components/LandingPage';
import { VideoChat } from './components/VideoChat';
import { TextChat } from './components/TextChat';
import { ErrorBoundary } from './components/ErrorBoundary';
import { VideoDisabled } from './components/VideoDisabled';
import { ServiceUnavailable } from './components/ServiceUnavailable';
import { NetworkHealthProvider } from './hooks/useNetworkHealth';

function App() {
  const wsUrl = import.meta.env.VITE_WS_URL || 'ws://localhost:8080/ws';
  const enableVideoChat = import.meta.env.VITE_ENABLE_VIDEO_CHAT !== 'false';

  return (
    <div className="min-h-screen w-full overflow-x-hidden overflow-y-auto bg-gray-50 text-gray-900 dark:bg-gray-900 dark:text-white">
      <NetworkHealthProvider>
        <Routes>
          <Route path="/" element={<LandingPage />} />
          <Route 
            path="/text" 
            element={
              <ErrorBoundary>
                <TextChat wsUrl={wsUrl} />
              </ErrorBoundary>
            } 
          />
          <Route 
            path="/video" 
            element={
              enableVideoChat ? (
                <ErrorBoundary>
                  <VideoChat wsUrl={wsUrl} />
                </ErrorBoundary>
              ) : (
                <Navigate to="/video-disabled" replace />
              )
            } 
          />
          <Route path="/video-disabled" element={<VideoDisabled />} />
          <Route path="/unavailable" element={<ServiceUnavailable />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </NetworkHealthProvider>
    </div>
  );
}

export default App;
