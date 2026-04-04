import { useNavigate } from 'react-router-dom';

export function VideoDisabled() {
  const navigate = useNavigate();

  return (
    <div className="min-h-screen w-full flex flex-col items-center justify-center bg-slate-50 dark:bg-slate-950 font-nunito p-4">
      <div className="max-w-md w-full glass-card p-8 rounded-3xl flex flex-col items-center text-center relative z-10 transition-colors duration-500">
        <div className="w-20 h-20 bg-indigo-100 dark:bg-slate-800 rounded-full flex items-center justify-center mb-6 text-indigo-500 dark:text-indigo-400">
          <svg className="w-10 h-10" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2.5">
            <path strokeLinecap="round" strokeLinejoin="round" d="m15.75 10.5 4.72-4.72a.75.75 0 0 1 1.28.53v11.38a.75.75 0 0 1-1.28.53l-4.72-4.72M4.5 18.75h9a2.25 2.25 0 0 0 2.25-2.25v-9a2.25 2.25 0 0 0-2.25-2.25h-9A2.25 2.25 0 0 0 2.25 7.5v9a2.25 2.25 0 0 0 2.25 2.25Z" />
          </svg>
        </div>
        <h1 className="text-3xl font-extrabold text-slate-800 dark:text-white mb-4 tracking-tight">Video Chat Coming Soon</h1>
        <p className="text-slate-600 dark:text-slate-400 mb-8 leading-relaxed font-medium">
          Hosting millions of video streams is terribly expensive! We are currently setting up dedicated high-bandwidth infrastructure. Video Chat will be available soon.
        </p>
        <button
          onClick={() => navigate('/')}
          className="w-full bg-slate-900 text-white dark:bg-white dark:text-slate-900 font-bold py-3.5 px-6 rounded-2xl hover:bg-slate-800 dark:hover:bg-slate-100 transition-colors"
        >
          Return to Text Chat
        </button>
      </div>

      <style>{`
        .font-nunito { font-family: 'Nunito', sans-serif; }
        
        .glass-card {
           background: rgba(255, 255, 255, 0.7);
           backdrop-filter: blur(12px);
           -webkit-backdrop-filter: blur(12px);
           border: 2px solid rgba(255, 255, 255, 0.9);
           box-shadow: 0 10px 30px rgba(0, 0, 0, 0.05), inset 0 0 0 2px rgba(255, 255, 255, 0.6);
        }
        .dark .glass-card {
           background: rgba(30, 41, 59, 0.8);
           border: 2px solid rgba(255, 255, 255, 0.1);
           box-shadow: 0 10px 30px rgba(0, 0, 0, 0.4), inset 0 0 0 1px rgba(255, 255, 255, 0.05);
        }
      `}</style>
    </div>
  );
}
