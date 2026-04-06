import { Routes, Route, Navigate } from 'react-router-dom';
import { LandingPage } from './components/LandingPage';
import { VideoChat } from './components/VideoChat';
import { TextChat } from './components/TextChat';
import { ErrorBoundary } from './components/ErrorBoundary';
import { VideoDisabled } from './components/VideoDisabled';

function App() {
  const wsUrl = import.meta.env.VITE_WS_URL || 'ws://localhost:8080/ws';
  const enableVideoChat = import.meta.env.VITE_ENABLE_VIDEO_CHAT !== 'false';

  return (
    <div className="min-h-screen w-full overflow-x-hidden overflow-y-auto bg-gray-50 text-gray-900 dark:bg-gray-900 dark:text-white">
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
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </div>
  );
}

export default App;
