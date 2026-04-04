import { useNavigate } from 'react-router-dom';
import { ThemeToggle } from './ThemeToggle';
import promoVideo from '../assets/promo.mp4';
import promoImg from '../assets/promo.webp';
import promoFeaturesImg from '../assets/promofeatures.webp';

export function LandingPage() {
  const navigate = useNavigate();

  return (
    <div className="relative w-full min-h-screen flex flex-col overflow-x-hidden font-nunito bg-slate-50 dark:bg-slate-950 transition-colors duration-500">
      {/* Import fonts and styles */}
      <style>{`
        @import url('https://fonts.googleapis.com/css2?family=Nunito:wght@400;600;700;800;900&family=M+PLUS+Rounded+1c:wght@400;700;800&display=swap');
        .font-nunito { font-family: 'Nunito', sans-serif; }
        .font-anime { font-family: 'M PLUS Rounded 1c', sans-serif; }

        /* Glass Cards */
        .glass-card {
           background: rgba(255, 255, 255, 0.7);
           backdrop-filter: blur(12px);
           -webkit-backdrop-filter: blur(12px);
           border: 2px solid rgba(255, 255, 255, 0.9);
           border-radius: 1.5rem;
           box-shadow: 0 10px 30px rgba(255, 154, 158, 0.1), inset 0 0 0 2px rgba(255, 255, 255, 0.6);
           transition: all 0.3s cubic-bezier(0.25, 0.8, 0.25, 1);
        }
        .dark .glass-card {
           background: rgba(30, 41, 59, 0.8);
           border: 2px solid rgba(255, 255, 255, 0.1);
           box-shadow: 0 10px 30px rgba(0, 0, 0, 0.4), inset 0 0 0 1px rgba(255, 255, 255, 0.05);
        }
        .glass-card:hover {
           transform: translateY(-8px) scale(1.02);
           box-shadow: 0 15px 40px rgba(255, 154, 158, 0.2), inset 0 0 0 2px rgba(255, 255, 255, 0.9);
           background: rgba(255, 255, 255, 0.9);
        }
        .dark .glass-card:hover {
           box-shadow: 0 15px 40px rgba(0, 0, 0, 0.6), inset 0 0 0 1px rgba(255, 255, 255, 0.15);
           background: rgba(30, 41, 59, 0.95);
        }

        /* Anime Style Buttons */
        .anime-button {
           background: linear-gradient(135deg, #ff9a9e 0%, #fecfef 99%, #fecfef 100%);
           border: 3px solid rgba(255, 255, 255, 0.9);
           box-shadow: 0 8px 20px rgba(255, 154, 158, 0.5), inset 0 0 10px rgba(255,255,255,0.5);
           color: #d6336c;
           transition: all 0.2s ease-in-out;
        }
        .anime-button:hover {
           transform: translateY(-3px) scale(1.03);
           box-shadow: 0 12px 25px rgba(255, 154, 158, 0.7), inset 0 0 15px rgba(255,255,255,0.8);
        }
        
        .anime-button-video {
           background: rgba(255, 255, 255, 0.9);
           backdrop-filter: blur(8px);
           border: 3px solid rgba(255, 255, 255, 0.9);
           box-shadow: 0 8px 20px rgba(255, 255, 255, 0.4);
           color: #4f46e5;
           transition: all 0.2s ease-in-out;
        }
        .anime-button-video:hover {
           background: rgba(255, 255, 255, 1);
           transform: translateY(-3px) scale(1.03);
           box-shadow: 0 12px 25px rgba(255, 255, 255, 0.6);
        }

        /* Cute Anime Text Outline */
        .anime-outline-title {
           color: white;
           -webkit-text-stroke: 3px #ffb3c6; /* Light pink */
           paint-order: stroke fill;
        }
        .dark .anime-outline-title {
           -webkit-text-stroke: 3px #c2255c; /* Darker pink */
        }
        
        .anime-outline-text {
           color: white;
           -webkit-text-stroke: 1.5px #ffb3c6; /* Light pink */
           paint-order: stroke fill;
           font-weight: 800;
        }
        .dark .anime-outline-text {
           -webkit-text-stroke: 1.5px #c2255c; /* Darker pink */
        }
      `}</style>

      {/* Floating Controls */}
      <div className="fixed bottom-6 right-6 z-50 flex items-center gap-2 p-2 rounded-full bg-white/80 dark:bg-slate-800/80 backdrop-blur-md border border-gray-200 dark:border-slate-700 shadow-xl">
        <a
          href="https://github.com/alessblaze/pairline"
          target="_blank"
          rel="noopener noreferrer"
          className="hidden sm:flex items-center justify-center w-10 h-10 rounded-full text-indigo-500 dark:text-indigo-400 hover:bg-black/5 dark:hover:bg-white/10 transition-all font-bold"
          title="GitHub"
        >
          <svg className="w-6 h-6" fill="currentColor" viewBox="0 0 24 24">
            <path fillRule="evenodd" d="M12 2C6.477 2 2 6.484 2 12.017c0 4.425 2.865 8.18 6.839 9.504.5.092.682-.217.682-.483 0-.237-.008-.868-.013-1.703-2.782.605-3.369-1.343-3.369-1.343-.454-1.158-1.11-1.466-1.11-1.466-.908-.62.069-.608.069-.608 1.003.07 1.531 1.032 1.531 1.032.892 1.53 2.341 1.088 2.91.832.092-.647.35-1.088.636-1.338-2.22-.253-4.555-1.113-4.555-4.951 0-1.093.39-1.988 1.029-2.688-.103-.253-.446-1.272.098-2.65 0 0 .84-.27 2.75 1.026A9.564 9.564 0 0 1 12 6.844c.85.004 1.705.115 2.504.337 1.909-1.296 2.747-1.027 2.747-1.027.546 1.379.202 2.398.1 2.651.64.7 1.028 1.595 1.028 2.688 0 3.848-2.339 4.695-4.566 4.943.359.309.678.92.678 1.855 0 1.338-.012 2.419-.012 2.747 0 .268.18.58.688.482A10.019 10.019 0 0 0 22 12.017C22 6.484 17.522 2 12 2Z" clipRule="evenodd" />
          </svg>
        </a>
        <ThemeToggle />
      </div>

      {/* Top Video Header */}
      <div className="w-full relative bg-gray-950 flex justify-center items-center">
        <video
          src={promoVideo}
          autoPlay
          loop
          muted
          playsInline
          className="w-full h-auto block"
        />
      </div>

      {/* Hero Interactive Section with User's Promo Image */}
      <section className="relative z-10 w-full flex flex-col items-center justify-center bg-slate-50 dark:bg-slate-950">
        <div className="relative w-full flex flex-col items-center">
          {/* Full-width responsive image */}
          <img
            src={promoImg}
            alt="Pairline"
            className="w-full h-auto object-contain"
          />

          {/* CTA Buttons absolutely positioned over the 'clear' bottom part of the image */}
          <div className="absolute top-[60%] sm:top-[65%] left-0 right-0 w-full flex flex-col items-center px-4">
            <div className="flex flex-col gap-3 sm:gap-5 justify-center items-center w-full max-w-3xl">
              <button
                onClick={() => navigate('/text')}
                className="anime-button w-48 sm:w-72 inline-flex items-center justify-center gap-1.5 sm:gap-2 px-4 py-2 sm:px-10 sm:py-5 rounded-xl sm:rounded-2xl font-anime font-bold text-sm sm:text-xl"
              >
                <svg className="w-4 h-4 sm:w-6 sm:h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2.5">
                  <path strokeLinecap="round" strokeLinejoin="round" d="M7.5 8.25h9m-9 3H12m-9.75 1.51c0 1.6 1.123 2.994 2.707 3.227 1.129.166 2.27.293 3.423.379.35.026.67.21.865.501L12 21l2.755-4.133a1.14 1.14 0 0 1 .865-.501 48.172 48.172 0 0 0 3.423-.379c1.584-.233 2.707-1.626 2.707-3.228V6.741c0-1.602-1.123-2.995-2.707-3.228A48.394 48.394 0 0 0 12 3c-2.392 0-4.744.175-7.043.513C3.373 3.746 2.25 5.14 2.25 6.741v6.018Z" />
                </svg>
                Start Text Chat
              </button>

              <button
                onClick={() => navigate('/video')}
                className="anime-button-video text-gray-900 w-48 sm:w-72 inline-flex items-center justify-center gap-1.5 sm:gap-2 px-4 py-2 sm:px-10 sm:py-5 rounded-xl sm:rounded-2xl font-anime font-bold text-sm sm:text-xl"
              >
                <svg className="w-4 h-4 sm:w-6 sm:h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2.5">
                  <path strokeLinecap="round" strokeLinejoin="round" d="m15.75 10.5 4.72-4.72a.75.75 0 0 1 1.28.53v11.38a.75.75 0 0 1-1.28.53l-4.72-4.72M4.5 18.75h9a2.25 2.25 0 0 0 2.25-2.25v-9a2.25 2.25 0 0 0-2.25-2.25h-9A2.25 2.25 0 0 0 2.25 7.5v9a2.25 2.25 0 0 0 2.25 2.25Z" />
                </svg>
                Video Chat
              </button>
            </div>
          </div>
        </div>
      </section>

      {/* Features Section with User Promo Features Image & Footer */}
      <section className="relative z-10 w-full bg-slate-50 dark:bg-slate-950">
        <div className="w-full relative">
          <img
            src={promoFeaturesImg}
            alt="Pairline Features"
            className="w-full h-auto block"
          />
          {/* Footer Overlaid on Image with Gradient Shadow */}
          <footer className="absolute bottom-0 left-0 right-0 w-full px-4 sm:px-6 lg:px-8 pt-16 sm:pt-32 pb-4 sm:pb-10 bg-gradient-to-t from-black/80 via-black/40 to-transparent">
            <div className="max-w-6xl mx-auto text-center">
              <p className="text-xs sm:text-base font-bold text-white mb-1 sm:mb-2 font-anime drop-shadow-md">
                With ❤️ From Aless
              </p>
              <p className="text-[10px] sm:text-sm font-semibold text-slate-200 drop-shadow-md">
                © 2026 Pairline. All rights reserved. Let your dreams take flight!
              </p>
            </div>
          </footer>
        </div>
      </section>
    </div>
  );
}