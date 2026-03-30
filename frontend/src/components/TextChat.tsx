import { useState, useRef, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTextChat } from '../hooks/useTextChat';
import { ThemeToggle } from './ThemeToggle';
import { ReportDialog } from './ReportDialog';
import DOMPurify from 'dompurify';

export function TextChat({ wsUrl }: { wsUrl: string }) {
  const navigate = useNavigate();
  const { connected, status, messages, startSearch, stopSearch, disconnect, sendMessage, reportPeerId, sessionId, sessionToken, peerTyping, sendTyping } = useTextChat(wsUrl);

  const [input, setInput] = useState('');
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const [confirmStop, setConfirmStop] = useState(false);
  const [showReport, setShowReport] = useState(false);
  const typingTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const canReportLastChat = !!reportPeerId && messages.some((message) => message.sender !== 'system');

  const scrollToBottom = () => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  };

  useEffect(() => {
    scrollToBottom();
  }, [messages]);

  useEffect(() => {
    if (status !== 'connected') {
      setConfirmStop(false);
    }
  }, [status]);

  useEffect(() => {
    return () => {
      if (typingTimeoutRef.current) {
        clearTimeout(typingTimeoutRef.current);
      }
    };
  }, []);

  const handleSend = (e: React.FormEvent) => {
    e.preventDefault();
    if (input.trim()) {
      sendMessage(input);
      setInput('');
      sendTyping(false);
      if (typingTimeoutRef.current) {
        clearTimeout(typingTimeoutRef.current);
        typingTimeoutRef.current = null;
      }
      setConfirmStop(false);
    }
  };

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const value = e.target.value;
    setInput(value);

    if (value.trim()) {
      sendTyping(true);
      if (typingTimeoutRef.current) {
        clearTimeout(typingTimeoutRef.current);
      }
      typingTimeoutRef.current = setTimeout(() => {
        sendTyping(false);
        typingTimeoutRef.current = null;
      }, 2000);
    } else {
      sendTyping(false);
      if (typingTimeoutRef.current) {
        clearTimeout(typingTimeoutRef.current);
        typingTimeoutRef.current = null;
      }
    }
  };

  const handleDisconnect = () => {
    if (!confirmStop) {
      setConfirmStop(true);
      setTimeout(() => setConfirmStop(false), 3000);
    } else {
      disconnect();
      setConfirmStop(false);
    }
  };

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
        <div className="flex-1 min-h-0 overflow-y-auto p-3 sm:p-4 space-y-3 sm:space-y-4" id="messages">
          {status === 'idle' && (
            <div className="h-full flex flex-col items-center justify-center text-center space-y-3 sm:space-y-4">
              <div className="w-14 h-14 sm:w-16 sm:h-16 rounded-full bg-gradient-to-tr from-indigo-500 to-purple-500 flex items-center justify-center shrink-0">
                <svg className="w-7 h-7 sm:w-8 sm:h-8 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M17 8h2a2 2 0 012 2v6a2 2 0 01-2 2h-2v4l-4-4H9a1.994 1.994 0 01-1.414-.586m0 0L11 14h4a2 2 0 002-2V6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2v4l.586-.586z" />
                </svg>
              </div>
              <h2 className="text-xl sm:text-2xl font-bold text-gray-900 dark:text-white">Start a Conversation</h2>
              <p className="text-sm sm:text-base text-gray-600 dark:text-gray-400 max-w-md px-4">Connect with random strangers around the world in an instant.</p>
            </div>
          )}

          {status === 'searching' && (
            <div className="h-full flex flex-col items-center justify-center text-center space-y-3 sm:space-y-4">
              <div className="w-10 h-10 sm:w-12 sm:h-12 border-4 border-indigo-500 border-t-transparent rounded-full animate-spin shrink-0"></div>
              <p className="text-base sm:text-lg font-medium text-gray-700 dark:text-gray-300">Finding your match...</p>
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
                <div className="px-3 py-2 sm:px-4 sm:py-2 bg-gray-100 dark:bg-gray-800 rounded-full text-xs sm:text-sm text-gray-500 dark:text-gray-400 font-medium">
                  {msg.text}
                </div>
              ) : (
                <div
                  className={`max-w-[85%] sm:max-w-[80%] px-3 py-2 sm:px-4 sm:py-2 rounded-2xl ${msg.sender === 'me'
                      ? 'bg-indigo-600 text-white'
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
            <div className="flex flex-col gap-2">
              {status === 'disconnected' && canReportLastChat && (
                <button
                  onClick={() => setShowReport(true)}
                  className="w-full py-2.5 sm:py-3 bg-orange-100 dark:bg-orange-900/30 text-orange-600 dark:text-orange-400 font-semibold rounded-xl hover:bg-orange-200 dark:hover:bg-orange-900/50 transition-colors text-sm sm:text-base"
                >
                  Report Last Chat
                </button>
              )}
              <button
                onClick={startSearch}
                disabled={!connected}
                className="w-full py-2.5 sm:py-3 bg-indigo-600 hover:bg-indigo-700 disabled:bg-gray-300 dark:disabled:bg-gray-600 disabled:text-gray-500 text-white font-semibold rounded-xl transition-colors text-sm sm:text-base"
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
        <div className="flex flex-col gap-2 sm:gap-3">
          <form onSubmit={handleSend} className="flex gap-2 items-center">
            <input
              type="text"
              value={input}
              onChange={handleInputChange}
              placeholder="Type a message..."
              maxLength={2000}
              className="flex-1 px-3 py-2 sm:px-4 sm:py-3 bg-white dark:bg-gray-700 border border-gray-300 dark:border-gray-600 rounded-xl text-base text-gray-900 dark:text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-indigo-500"
              autoFocus
            />
            <button
              type="submit"
              disabled={!input.trim()}
              className="shrink-0 px-4 py-2 sm:px-6 sm:py-3 bg-indigo-600 hover:bg-indigo-700 disabled:bg-gray-300 dark:disabled:bg-gray-600 disabled:text-gray-500 text-white font-semibold rounded-xl transition-colors text-sm sm:text-base h-full"
            >
              Send
            </button>
          </form>
          {input.length > 0 && (
            <div className={`text-center text-xs font-medium transition-all duration-200 ${
              input.length >= 1800 ? 'text-orange-500' : 'text-gray-400 dark:text-gray-500'
            }`}>
              {input.length} / 2000 characters
            </div>
          )}
          <div className="flex gap-2">
            <button
              onClick={() => setShowReport(true)}
              className="flex-1 py-2.5 sm:py-3 bg-orange-100 dark:bg-orange-900/30 text-orange-600 dark:text-orange-400 font-semibold rounded-xl hover:bg-orange-200 dark:hover:bg-orange-900/50 transition-colors text-sm sm:text-base"
            >
              Report
            </button>
            <button
              onClick={handleDisconnect}
              className={`flex-1 py-2.5 sm:py-3 font-semibold rounded-xl transition-colors text-sm sm:text-base ${confirmStop
                  ? 'bg-red-600 hover:bg-red-700 text-white'
                  : 'bg-red-100 dark:bg-red-900/30 text-red-600 dark:text-red-400 hover:bg-red-200 dark:hover:bg-red-900/50'
                }`}
            >
              {confirmStop ? 'Tap again to stop' : 'Stop Chat'}
            </button>
          </div>
        </div>
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
    </div>
  );
}
