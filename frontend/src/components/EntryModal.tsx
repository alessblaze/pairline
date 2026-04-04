import { useState, useRef } from 'react';
import { Turnstile, type TurnstileInstance } from '@marsidev/react-turnstile';
import { useTheme } from '../hooks/useTheme';

interface EntryModalProps {
  onClose: () => void;
  onConfirm: (token: string) => void;
}

export function EntryModal({ onClose, onConfirm }: EntryModalProps) {
  const [termsAccepted, setTermsAccepted] = useState(false);
  const [turnstileToken, setTurnstileToken] = useState<string | null>(null);
  const [widgetReady, setWidgetReady] = useState(false);
  const turnstileRef = useRef<TurnstileInstance | null>(null);
  const { theme } = useTheme();

  const siteKey = import.meta.env.VITE_TURNSTILE_SITE_KEY
    || (import.meta.env.DEV ? '1x00000000000000000000AA' : '');

  const handleConfirm = () => {
    if (turnstileToken && termsAccepted) {
      onConfirm(turnstileToken);
    }
  };

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/60 backdrop-blur-sm px-4">
      <div className="bg-white dark:bg-slate-900 rounded-2xl shadow-2xl max-w-lg w-full p-6 border border-gray-200 dark:border-slate-800 flex flex-col gap-5 animate-in fade-in zoom-in duration-300">
        <div className="flex items-start justify-between">
          <div>
            <h2 className="text-2xl font-bold text-gray-900 dark:text-white font-nunito">Before you continue...</h2>
            <p className="text-sm text-gray-500 dark:text-gray-400 mt-1 font-nunito">We need to make sure you're human and agree to our terms.</p>
          </div>
          <button onClick={onClose} className="p-2 text-gray-400 hover:text-gray-600 dark:hover:text-white bg-gray-100 dark:bg-slate-800 rounded-full transition-colors">
            <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12"/></svg>
          </button>
        </div>

        <div className="flex items-center gap-3 bg-gray-50 dark:bg-slate-800/50 p-4 rounded-xl border border-gray-100 dark:border-slate-800">
          <input 
            type="checkbox" 
            id="terms" 
            checked={termsAccepted} 
            onChange={(e) => setTermsAccepted(e.target.checked)}
            className="w-5 h-5 text-indigo-600 rounded border-gray-300 focus:ring-indigo-600 dark:bg-slate-800 dark:border-slate-600 focus:ring-offset-slate-900 cursor-pointer"
          />
          <label htmlFor="terms" className="text-sm text-gray-700 dark:text-gray-300 cursor-pointer font-nunito leading-tight">
            I have read and agree to the <a href="#" className="text-indigo-600 dark:text-indigo-400 hover:underline">Terms of Service</a> and <a href="#" className="text-indigo-600 dark:text-indigo-400 hover:underline">Privacy Policy</a>. I am 18+ years old.
          </label>
        </div>

        <div className="flex justify-center my-2">
          {!widgetReady && (
            <div className="flex items-center justify-center h-[65px] w-[300px] bg-gray-100 dark:bg-slate-800 rounded-lg animate-pulse">
              <span className="text-xs text-gray-400 dark:text-gray-500">Loading verification...</span>
            </div>
          )}
          {siteKey ? (
            <Turnstile
              key={theme}
              ref={turnstileRef}
              siteKey={siteKey}
              options={{ theme }}
              onSuccess={(token) => {
                setTurnstileToken(token);
                setWidgetReady(true);
              }}
              onError={() => {
                setTurnstileToken(null);
                setWidgetReady(true);
              }}
              onExpire={() => {
                setTurnstileToken(null);
                turnstileRef.current?.reset();
              }}
              onWidgetLoad={() => setWidgetReady(true)}
            />
          ) : (
            <div className="text-sm text-red-500 font-medium p-3 bg-red-50 dark:bg-red-900/20 rounded-lg">
              CAPTCHA not configured. Please set VITE_TURNSTILE_SITE_KEY.
            </div>
          )}
        </div>

        <div className="flex justify-end gap-3 mt-2">
          <button 
            onClick={onClose}
            className="px-5 py-2.5 rounded-xl font-bold text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-slate-800 transition-colors"
          >
            Cancel
          </button>
          <button 
            onClick={handleConfirm}
            disabled={!turnstileToken || !termsAccepted}
            className="px-6 py-2.5 rounded-xl font-bold bg-indigo-600 text-white hover:bg-indigo-700 disabled:bg-gray-300 dark:disabled:bg-slate-700 disabled:text-gray-500 transition-all active:scale-[0.98]"
          >
            Confirm &amp; Continue
          </button>
        </div>
      </div>
    </div>
  );
}
