import { Navigate, Route, Routes } from 'react-router-dom'
import { AdminPanel } from './components/AdminPanel'

function App() {
  return (
    <div className="min-h-screen bg-[#050816] text-white">
      <Routes>
        <Route path="/" element={<AdminPanel loginRoute="/" />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </div>
  )
}

export default App
