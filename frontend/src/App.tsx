import { Routes, Route, Navigate } from 'react-router-dom';
import { LandingPage } from './components/LandingPage';
import { VideoChat } from './components/VideoChat';
import { TextChat } from './components/TextChat';
import { AdminPanel } from './components/AdminPanel';

function App() {
  const wsUrl = import.meta.env.VITE_WS_URL || 'ws://localhost:8080/ws';

  return (
    <div className="min-h-screen w-full overflow-x-hidden overflow-y-auto bg-gray-50 text-gray-900 dark:bg-gray-900 dark:text-white">
      <Routes>
        <Route path="/" element={<LandingPage />} />
        <Route path="/text" element={<TextChat wsUrl={wsUrl} />} />
        <Route path="/video" element={<VideoChat wsUrl={wsUrl} />} />
        <Route path="/admin-login" element={<AdminPanel />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </div>
  );
}

export default App;
