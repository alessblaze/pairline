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

import { useState, useRef, useEffect, useCallback, memo } from 'react';
import { useNavigate, useLocation } from 'react-router-dom';
import { useTextChat } from '../hooks/useTextChat';
import { ThemeToggle } from './ThemeToggle';
import { ReportDialog } from './ReportDialog';
import { EntryModal } from './EntryModal';
import { BANNED_PHRASE_REASON } from '../chatModeration';
import DOMPurify from 'dompurify';
import type { ChatMessage } from '../types';

// ---------------------------------------------------------------------------
// Shared type for the hook return value, used by both the component and stories.
// ---------------------------------------------------------------------------
export interface TextChatState {
  connected: boolean;
  status: 'idle' | 'searching' | 'connected' | 'disconnected';
  messages: ChatMessage[];
  peerTyping: boolean;
  startSearch: (interests?: string, turnstileToken?: string) => void;
  stopSearch: () => void;
  disconnect: () => void;
  sendMessage: (text: string) => void;
  sendTyping: (isTyping: boolean) => void;
  reportPeerId: string | null;
  sessionId: string | null;
  sessionToken: string | null;
}

// ---------------------------------------------------------------------------
// ChatInput — isolated so keystrokes ONLY re-render this small component,
// not the entire TextChat (messages list, status, etc.).
// ---------------------------------------------------------------------------
interface ChatInputProps {
  onSend: (text: string) => void;
  onTyping: (isTyping: boolean) => void;
  onDisconnect: () => void;
  onReport: () => void;
  confirmStop: boolean;
}

const ChatInput = memo(function ChatInput({
  onSend, onTyping, onDisconnect, onReport, confirmStop,
}: ChatInputProps) {
  const [value, setValue] = useState('');
  const typingTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Clean up typing timeout on unmount
  useEffect(() => () => {
    if (typingTimeoutRef.current) clearTimeout(typingTimeoutRef.current);
  }, []);

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const v = e.target.value;
    setValue(v);
    // Defer WS send past the paint frame so the character appears instantly
    setTimeout(() => {
      if (v.trim()) {
        onTyping(true);
        if (typingTimeoutRef.current) clearTimeout(typingTimeoutRef.current);
        typingTimeoutRef.current = setTimeout(() => {
          onTyping(false);
          typingTimeoutRef.current = null;
        }, 2000);
      } else {
        onTyping(false);
        if (typingTimeoutRef.current) {
          clearTimeout(typingTimeoutRef.current);
          typingTimeoutRef.current = null;
        }
      }
    }, 0);
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (value.trim()) {
      onSend(value);
      setValue('');
      onTyping(false);
      if (typingTimeoutRef.current) {
        clearTimeout(typingTimeoutRef.current);
        typingTimeoutRef.current = null;
      }
    }
  };

  return (
    <div className="flex flex-col gap-2 sm:gap-3">
      <form onSubmit={handleSubmit} className="flex gap-2 items-center">
        <input
          id="chat-message-input"
          type="text"
          value={value}
          onChange={handleChange}
          placeholder="Type a message..."
          maxLength={2000}
          autoComplete="off"
          autoCorrect="off"
          spellCheck={false}
          className="flex-1 px-3 py-2 sm:px-4 sm:py-3 bg-white dark:bg-gray-700 border border-gray-300 dark:border-gray-600 rounded-xl text-base text-gray-900 dark:text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-indigo-500"
          autoFocus
        />
        <button
          type="submit"
          disabled={!value.trim()}
          className="shrink-0 px-4 py-2 sm:px-6 sm:py-3 bg-indigo-600 hover:bg-indigo-700 disabled:bg-gray-300 dark:disabled:bg-gray-600 disabled:text-gray-500 text-white font-semibold rounded-xl transition-colors text-sm sm:text-base h-full"
        >
          Send
        </button>
      </form>
      {value.length > 0 && (
        <div className={`text-center text-xs font-medium transition-all duration-200 ${
          value.length >= 1800 ? 'text-orange-500' : 'text-gray-400 dark:text-gray-500'
        }`}>
          {value.length} / 2000 characters
        </div>
      )}
      <div className="flex gap-2">
        <button
          onClick={onReport}
          className="flex-1 py-2.5 sm:py-3 bg-orange-100 dark:bg-orange-900/30 text-orange-600 dark:text-orange-400 font-semibold rounded-xl hover:bg-orange-200 dark:hover:bg-orange-900/50 transition-colors text-sm sm:text-base"
        >
          Report
        </button>
        <button
          onClick={onDisconnect}
          className={`flex-1 py-2.5 sm:py-3 font-semibold rounded-xl transition-colors text-sm sm:text-base ${
            confirmStop
              ? 'bg-red-600 hover:bg-red-700 text-white'
              : 'bg-red-100 dark:bg-red-900/30 text-red-600 dark:text-red-400 hover:bg-red-200 dark:hover:bg-red-900/50'
          }`}
        >
          {confirmStop ? 'Tap again to stop' : 'Stop Chat'}
        </button>
      </div>
    </div>
  );
});

// ---------------------------------------------------------------------------
// TextChatView — the presentational component.
// Exported so Storybook stories can render it directly with mock state,
// without triggering the real WebSocket hook.
// ---------------------------------------------------------------------------
export function TextChatView({ state }: { state: TextChatState }) {
  const navigate = useNavigate();
  const location = useLocation();
  const turnstileToken = location.state?.turnstileToken as string | undefined;

  const { connected, status, messages, startSearch, stopSearch, disconnect, sendMessage, reportPeerId, sessionId, sessionToken, peerTyping, sendTyping: rawSendTyping } = state;

  // Stabilise sendTyping identity so the memoised ChatInput doesn't re-render on every parent state change
  const sendTyping = useCallback((isTyping: boolean) => rawSendTyping(isTyping), [rawSendTyping]);

  // Mirrors the backend's captcha_verified socket flag.
  // Once verified, subsequent searches on the same WS connection skip the modal.
  // Resets when the WebSocket reconnects (new socket = new captcha_verified state).
  const [captchaVerified, setCaptchaVerified] = useState(false);

  // Reset captchaVerified when WebSocket reconnects (new socket = fresh captcha state)
  useEffect(() => {
    if (!connected && captchaVerified) {
      setCaptchaVerified(false);
    }
  }, [connected, captchaVerified]);

  // Reset captchaVerified if the backend rejected our token
  useEffect(() => {
    if (!captchaVerified) return;
    const lastMsg = messages[messages.length - 1];
    if (lastMsg?.sender === 'system' && lastMsg?.text?.includes('CAPTCHA')) {
      setCaptchaVerified(false);
    }
  }, [messages, captchaVerified]);

  // input state now lives in <ChatInput> — removed from parent to isolate keystroke re-renders
  const [interestTags, setInterestTags] = useState<string[]>([]);
  const [tagInput, setTagInput] = useState('');
  const [showEntryModal, setShowEntryModal] = useState(false);
  const [pendingInterests, setPendingInterests] = useState('');
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const [confirmStop, setConfirmStop] = useState(false);
  const [showReport, setShowReport] = useState(false);
  const canReportLastChat = !!reportPeerId;

  const systemMessageClass = (message: ChatMessage) => (
    message.systemReason === BANNED_PHRASE_REASON
      ? 'bg-red-100 text-red-700 shadow-[0_0_18px_rgba(239,68,68,0.28)] ring-1 ring-red-300/80 dark:bg-red-950/70 dark:text-red-200 dark:ring-red-500/40 dark:shadow-[0_0_22px_rgba(248,113,113,0.28)]'
      : 'bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400'
  );
  const myMessageClass = (deliveryStatus?: ChatMessage['deliveryStatus']) => (
    deliveryStatus === 'pending'
      ? 'bg-indigo-500/70 text-white'
      : deliveryStatus === 'failed'
        ? 'bg-amber-100 text-amber-800 ring-1 ring-amber-300/80 dark:bg-amber-950/60 dark:text-amber-100 dark:ring-amber-500/40'
        : deliveryStatus === 'blocked'
          ? 'bg-red-100 text-red-700 ring-1 ring-red-300/80 shadow-[0_0_18px_rgba(239,68,68,0.22)] dark:bg-red-950/70 dark:text-red-100 dark:ring-red-500/40 dark:shadow-[0_0_22px_rgba(248,113,113,0.24)]'
          : 'bg-indigo-600 text-white'
  );

  // handleSend is now inside ChatInput; parent just needs a callback wrapper
  const handleSend = useCallback((text: string) => {
    sendMessage(text);
    setConfirmStop(false);
  }, [sendMessage]);

  const scrollToBottom = () => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'instant' });
  };

  useEffect(() => { scrollToBottom(); }, [messages]);

  useEffect(() => {
    if (status !== 'connected') setConfirmStop(false);
  }, [status]);

  // handleInputChange is now inside ChatInput — removed from parent

  const handleDisconnect = () => {
    if (!confirmStop) {
      setConfirmStop(true);
      setTimeout(() => setConfirmStop(false), 3000);
    } else {
      disconnect();
      setConfirmStop(false);
    }
  };

  const addTag = () => {
    let trimmed = tagInput.trim().replace(/[,,;]/g, '');
    if (trimmed.length > 30) {
      trimmed = trimmed.substring(0, 30);
    }
    if (trimmed && !interestTags.includes(trimmed) && interestTags.length < 10) {
      setInterestTags([...interestTags, trimmed]);
      setTagInput('');
    } else {
      setTagInput('');
    }
  };

  const removeTag = (index: number) => {
    setInterestTags(interestTags.filter((_, i) => i !== index));
  };

  const handleTagInputKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' || e.key === ',' || e.key === ';') {
      e.preventDefault();
      addTag();
    } else if (e.key === 'Backspace' && !tagInput && interestTags.length > 0) {
      removeTag(interestTags.length - 1);
    }
  };

  const handleStartSearchClick = useCallback((interestsStr: string) => {
    if (captchaVerified) {
      startSearch(interestsStr);
    } else if (turnstileToken) {
      startSearch(interestsStr, turnstileToken);
      setCaptchaVerified(true);
      navigate(location.pathname, { replace: true, state: {} });
    } else {
      setPendingInterests(interestsStr);
      setShowEntryModal(true);
    }
  }, [
    captchaVerified,
    turnstileToken,
    startSearch,
    navigate,
    location.pathname,
  ]);

  const handleModalConfirm = useCallback((token: string) => {
    setShowEntryModal(false);
    startSearch(pendingInterests, token);
    setCaptchaVerified(true);
  }, [startSearch, pendingInterests]);

  return (
    <div className="fixed inset-0 flex items-center justify-center p-2 sm:p-4 bg-gradient-to-br from-gray-50 to-gray-100 dark:from-gray-900 dark:to-gray-800">
      <div className="w-full max-w-4xl h-full max-h-[calc(100dvh-1rem)] sm:max-h-[calc(100dvh-2rem)] flex flex-col bg-white dark:bg-gray-900 rounded-2xl shadow-2xl overflow-hidden border border-gray-200 dark:border-gray-700">

        {/* Header */}
        <div className="flex items-center justify-between px-3 sm:px-4 py-2 sm:py-3 border-b border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800 shrink-0">
          <div className="flex items-center gap-2">
            <button
              onClick={() => navigate('/')}
              className="p-2 rounded-lg hover:bg-gray-200 dark:hover:bg-gray-700 transition-colors"
              aria-label="Back to home"
            >
              <svg className="w-5 h-5 text-gray-600 dark:text-gray-300" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
                <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 19.5 3 12m0 0 7.5-7.5M3 12h18" />
              </svg>
            </button>
            <div className={`w-2.5 h-2.5 rounded-full shrink-0 ${status === 'connected' ? 'bg-green-500' :
              status === 'searching' ? 'bg-yellow-500 animate-pulse' :
                status === 'disconnected' ? 'bg-red-500' : 'bg-gray-400'
              }`} />
            <span className="text-base sm:text-lg font-semibold text-gray-900 dark:text-white truncate">Live Chat</span>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-xs sm:text-sm font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wide shrink-0">
              {status}
            </span>
            <ThemeToggle />
          </div>
        </div>

        {/* Chat Area */}
        <div className="flex-1 min-h-0 overflow-y-auto p-3 sm:p-4 space-y-3 sm:space-y-4 flex flex-col" id="messages">
          {status === 'idle' && (
            <div className="flex-1 flex flex-col items-center justify-center text-center px-4 animate-in fade-in duration-500">
              <h2 className="text-3xl sm:text-4xl font-extrabold tracking-tight bg-gradient-to-r from-indigo-600 to-purple-600 bg-clip-text text-transparent mb-3">
                Pairline
              </h2>
              <p className="text-sm sm:text-base text-gray-500 dark:text-gray-400 max-w-xs mx-auto leading-relaxed">
                Enter your interests below or press Start to chat with someone new.
              </p>
            </div>
          )}

          {status === 'searching' && (
            <div className="flex-1 flex flex-col items-center justify-center text-center space-y-4 sm:space-y-6 px-4">
              <div className="relative">
                <div className="w-16 h-16 sm:w-20 sm:h-20 border-4 border-indigo-500 border-t-transparent rounded-full animate-spin"></div>
                <div className="absolute inset-0 flex items-center justify-center">
                  <div className="w-8 h-8 sm:w-10 sm:h-10 bg-indigo-500/10 rounded-full animate-pulse"></div>
                </div>
              </div>
              <div className="space-y-2">
                <p className="text-xl sm:text-2xl font-bold bg-gradient-to-r from-indigo-600 to-purple-600 bg-clip-text text-transparent">Finding your match...</p>
                {interestTags.length > 0 && (
                  <div className="flex flex-wrap justify-center gap-2 mt-4">
                    {interestTags.map((tag, i) => (
                      <span key={i} className="px-3 py-1 bg-indigo-100 dark:bg-indigo-900/40 text-indigo-600 dark:text-indigo-300 text-xs font-semibold rounded-full border border-indigo-200 dark:border-indigo-800 animate-in fade-in zoom-in duration-300">
                        #{tag}
                      </span>
                    ))}
                  </div>
                )}
              </div>
            </div>
          )}

          {status === 'connected' && messages.length === 0 && (
            <div className="flex justify-center">
              <div className="px-3 py-2 sm:px-4 sm:py-2 bg-gray-100 dark:bg-gray-800 rounded-full text-sm sm:text-base text-gray-700 dark:text-gray-300">
                You're now chatting with a stranger. Say hi! 👋
              </div>
            </div>
          )}

          {messages.map((msg) => (
            <div
              key={msg.id}
              className={`flex ${msg.sender === 'system' ? 'justify-center' :
                msg.sender === 'me' ? 'justify-end' : 'justify-start'
                }`}
            >
              {msg.sender === 'system' ? (
                <div className={`px-3 py-2 sm:px-4 sm:py-2 rounded-full text-xs sm:text-sm font-medium transition-all ${systemMessageClass(msg)}`}>
                  {msg.text}
                </div>
              ) : (
                <div
                  className={`max-w-[85%] sm:max-w-[80%] px-3 py-2 sm:px-4 sm:py-2 rounded-2xl ${msg.sender === 'me'
                    ? myMessageClass(msg.deliveryStatus)
                    : 'bg-gray-200 dark:bg-gray-700 text-gray-900 dark:text-white'
                    }`}
                >
                  <p className="text-sm sm:text-base whitespace-pre-wrap break-words">{DOMPurify.sanitize(msg.text)}</p>
                </div>
              )}
            </div>
          ))}

          <div className={`flex justify-start transition-opacity duration-200 ${peerTyping ? 'opacity-100' : 'opacity-0'}`}>
            <div className="px-3 py-2 sm:px-4 sm:py-2 bg-gray-200 dark:bg-gray-700 rounded-2xl min-h-[35px] flex items-center">
              <div className="flex gap-1">
                <div className="w-2 h-2 bg-gray-500 dark:bg-gray-400 rounded-full animate-bounce" style={{ animationDelay: '0ms' }}></div>
                <div className="w-2 h-2 bg-gray-500 dark:bg-gray-400 rounded-full animate-bounce" style={{ animationDelay: '150ms' }}></div>
                <div className="w-2 h-2 bg-gray-500 dark:bg-gray-400 rounded-full animate-bounce" style={{ animationDelay: '300ms' }}></div>
              </div>
            </div>
          </div>

          <div ref={messagesEndRef} />
        </div>

        {/* Input Area */}
        <div className="p-3 sm:p-4 border-t border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800 shrink-0">
          {(status === 'idle' || status === 'disconnected') && (
            <div className="flex flex-col gap-4">
              <div className="space-y-2">
                <label className="block text-xs font-bold text-gray-500 dark:text-gray-400 uppercase tracking-widest ml-1">
                  Interest Tags
                </label>
                <div className="flex flex-wrap items-center gap-2 p-2 bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-xl min-h-[50px] focus-within:ring-2 focus-within:ring-indigo-500 transition-all shadow-sm">
                  {interestTags.map((tag, index) => (
                    <span
                      key={index}
                      className="flex items-center gap-1.5 px-2.5 py-1 bg-indigo-50 dark:bg-indigo-900/30 text-indigo-700 dark:text-indigo-300 text-sm font-medium rounded-lg animate-in zoom-in-95 duration-200"
                    >
                      {tag}
                      <button
                        onClick={() => removeTag(index)}
                        className="hover:text-indigo-900 dark:hover:text-white transition-colors"
                      >
                        <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2.5} d="M6 18L18 6M6 6l12 12" />
                        </svg>
                      </button>
                    </span>
                  ))}
                  <input
                    type="text"
                    value={tagInput}
                    onChange={(e) => setTagInput(e.target.value)}
                    onKeyDown={handleTagInputKeyDown}
                    onBlur={addTag}
                    maxLength={30}
                    autoComplete="off"
                    autoCorrect="off"
                    spellCheck={false}
                    placeholder={interestTags.length === 0 ? "e.g. coding, music, movies..." : ""}
                    className="flex-1 min-w-[120px] bg-transparent border-none outline-none text-[16px] font-medium dark:text-white placeholder:text-gray-400 dark:placeholder:text-gray-500"
                  />
                </div>
                <p className="text-[10px] text-gray-400 dark:text-gray-500 ml-1">
                  Press Enter or Comma to add. Max 10 tags.
                </p>
              </div>

              {status === 'disconnected' && canReportLastChat && (
                <button
                  onClick={() => setShowReport(true)}
                  className="w-full py-2.5 sm:py-3 bg-orange-100 dark:bg-orange-900/30 text-orange-600 dark:text-orange-400 font-semibold rounded-xl hover:bg-orange-200 dark:hover:bg-orange-900/50 transition-colors text-sm sm:text-base border border-orange-200 dark:border-orange-800/50"
                >
                  Report Last Chat
                </button>
              )}
              <button
                onClick={() => handleStartSearchClick(interestTags.join(','))}
                disabled={!connected}
                className="w-full py-3 sm:py-4 bg-indigo-600 hover:bg-indigo-700 disabled:bg-gray-300 dark:disabled:bg-gray-600 disabled:text-gray-500 text-white font-bold rounded-xl transition-all text-sm sm:text-lg shadow-lg shadow-indigo-500/25 active:scale-[0.98]"
              >
                {connected ? (status === 'disconnected' ? 'Search Again' : 'Find a Stranger') : 'Connecting...'}
              </button>
            </div>
          )}

          {status === 'searching' && (
            <button
              onClick={stopSearch}
              className="w-full py-2.5 sm:py-3 bg-red-100 dark:bg-red-900/30 text-red-600 dark:text-red-400 font-semibold rounded-xl hover:bg-red-200 dark:hover:bg-red-900/50 transition-colors text-sm sm:text-base"
            >
              Cancel Search
            </button>
          )}

          {status === 'connected' && (
            <ChatInput
              onSend={handleSend}
              onTyping={sendTyping}
              onDisconnect={handleDisconnect}
              onReport={() => setShowReport(true)}
              confirmStop={confirmStop}
            />
          )}
        </div>
      </div>

      {showReport && reportPeerId && (
        <ReportDialog
          peerId={reportPeerId}
          reporterSessionId={sessionId}
          reporterToken={sessionToken}
          messages={messages}
          onClose={() => setShowReport(false)}
        />
      )}

      {showEntryModal && (
        <EntryModal
          onClose={() => setShowEntryModal(false)}
          onConfirm={handleModalConfirm}
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// TextChat — the connected wrapper that plugs in the real WebSocket hook.
// ---------------------------------------------------------------------------
export function TextChat({ wsUrl }: { wsUrl: string }) {
  const state = useTextChat(wsUrl);
  return <TextChatView state={state} />;
}
