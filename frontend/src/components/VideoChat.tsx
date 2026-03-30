import { useState, useRef, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { useVideoChat } from '../hooks/useVideoChat';
import { ReportDialog } from './ReportDialog';
import { ThemeToggle } from './ThemeToggle';
import DOMPurify from 'dompurify';

interface VideoChatProps {
  wsUrl: string;
}

const LOCAL_PREVIEW_EDGE_MARGIN = 12;

export function VideoChat({ wsUrl }: VideoChatProps) {
  const navigate = useNavigate();
  const {
    connected, reportPeerId, sessionId, sessionToken, status, messages, peerTyping,
    localVideoRef, remoteVideoRef,
    startSearch, stopSearch, skip, disconnect,
    sendMessage, sendTyping, cameraError
  } = useVideoChat(wsUrl);

  const [input, setInput] = useState('');
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const [confirmStop, setConfirmStop] = useState(false);
  const [confirmSkip, setConfirmSkip] = useState(false);
  const [showReport, setShowReport] = useState(false);
  const [localPreviewPosition, setLocalPreviewPosition] = useState<{ x: number; y: number } | null>(null);
  const [isDraggingLocalPreview, setIsDraggingLocalPreview] = useState(false);
  const typingTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const videoPanelRef = useRef<HTMLDivElement>(null);
  const localPreviewRef = useRef<HTMLDivElement>(null);
  const dragOffsetRef = useRef<{ x: number; y: number } | null>(null);

  const scrollToBottom = () => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  };

  useEffect(() => { scrollToBottom(); }, [messages]);

  useEffect(() => {
    if (status !== 'connected') {
      setConfirmStop(false);
      setConfirmSkip(false);
    }
  }, [status]);

  useEffect(() => {
    return () => {
      if (typingTimeoutRef.current) clearTimeout(typingTimeoutRef.current);
    };
  }, []);

  useEffect(() => {
    const handlePointerMove = (event: PointerEvent) => {
      if (!dragOffsetRef.current || !videoPanelRef.current || !localPreviewRef.current) return;

      const panelRect = videoPanelRef.current.getBoundingClientRect();
      const previewRect = localPreviewRef.current.getBoundingClientRect();
      const nextX = event.clientX - panelRect.left - dragOffsetRef.current.x;
      const nextY = event.clientY - panelRect.top - dragOffsetRef.current.y;

      setLocalPreviewPosition({
        x: Math.min(
          Math.max(LOCAL_PREVIEW_EDGE_MARGIN, nextX),
          panelRect.width - previewRect.width - LOCAL_PREVIEW_EDGE_MARGIN
        ),
        y: Math.min(
          Math.max(LOCAL_PREVIEW_EDGE_MARGIN, nextY),
          panelRect.height - previewRect.height - LOCAL_PREVIEW_EDGE_MARGIN
        ),
      });
    };

    const handlePointerUp = () => {
      if (dragOffsetRef.current && videoPanelRef.current && localPreviewRef.current) {
        const panelRect = videoPanelRef.current.getBoundingClientRect();
        const previewRect = localPreviewRef.current.getBoundingClientRect();
        const currentX = previewRect.left - panelRect.left;
        const currentY = previewRect.top - panelRect.top;
        const maxX = panelRect.width - previewRect.width - LOCAL_PREVIEW_EDGE_MARGIN;
        const maxY = panelRect.height - previewRect.height - LOCAL_PREVIEW_EDGE_MARGIN;

        const snapX = currentX + previewRect.width / 2 < panelRect.width / 2
          ? LOCAL_PREVIEW_EDGE_MARGIN
          : maxX;
        const snapY = currentY + previewRect.height / 2 < panelRect.height / 2
          ? LOCAL_PREVIEW_EDGE_MARGIN
          : maxY;

        setLocalPreviewPosition({
          x: snapX,
          y: snapY,
        });
      }
      dragOffsetRef.current = null;
      setIsDraggingLocalPreview(false);
    };

    window.addEventListener('pointermove', handlePointerMove);
    window.addEventListener('pointerup', handlePointerUp);

    return () => {
      window.removeEventListener('pointermove', handlePointerMove);
      window.removeEventListener('pointerup', handlePointerUp);
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
      if (typingTimeoutRef.current) clearTimeout(typingTimeoutRef.current);
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

  const handleNext = () => {
    if (!confirmSkip) {
      setConfirmSkip(true);
      setTimeout(() => setConfirmSkip(false), 3000);
    } else {
      skip();
      setConfirmSkip(false);
    }
  };

  const handleLocalPreviewPointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
    if (!videoPanelRef.current || !localPreviewRef.current) return;

    const panelRect = videoPanelRef.current.getBoundingClientRect();
    const previewRect = localPreviewRef.current.getBoundingClientRect();

    dragOffsetRef.current = {
      x: event.clientX - previewRect.left,
      y: event.clientY - previewRect.top,
    };
    setIsDraggingLocalPreview(true);

    setLocalPreviewPosition(prev => prev ?? {
      x: previewRect.left - panelRect.left,
      y: previewRect.top - panelRect.top,
    });
  };

  const statusDot = (
    <div className={`w-2.5 h-2.5 rounded-full shrink-0 ${status === 'connected' ? 'bg-green-500' :
        status === 'searching' ? 'bg-yellow-500 animate-pulse' :
          status === 'disconnected' ? 'bg-red-500' : 'bg-gray-400'
      }`} />
  );
  const canReportLastChat = !!reportPeerId && messages.some((message) => message.sender !== 'system');

  return (
    /* Full-viewport shell */
    <div className="fixed inset-0 flex flex-col bg-gray-100 dark:bg-gray-900 overflow-hidden">

      {/* ── Header ────────────────────────────────────────────────────── */}
      <header className="flex items-center justify-between px-3 sm:px-5 py-2 sm:py-3
                         bg-white dark:bg-gray-900
                         border-b border-gray-200 dark:border-gray-700
                         shrink-0 z-20">
        <div className="flex items-center gap-2 min-w-0">
          <button
            onClick={() => navigate('/')}
            className="p-1.5 sm:p-2 rounded-lg hover:bg-gray-200 dark:hover:bg-gray-700 transition-colors shrink-0"
            aria-label="Back to home"
          >
            <svg className="w-5 h-5 text-gray-600 dark:text-gray-300" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 19.5 3 12m0 0 7.5-7.5M3 12h18" />
            </svg>
          </button>
          {statusDot}
          <span className="text-sm sm:text-lg font-semibold text-gray-900 dark:text-white truncate">
            Live Video &amp; Chat
          </span>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <span className="text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wide hidden xs:inline">
            {status}
          </span>
          <ThemeToggle />
        </div>
      </header>

      {/* ── Main body ─────────────────────────────────────────────────── */}
      {/*
          Layout strategy:
          - Mobile  (< md):  column — video on top (flexible height), chat below fills rest
          - Desktop (≥ md):  row    — video left 55%, chat right 45%
      */}
      <div className="flex-1 flex flex-col md:flex-row min-h-0">

        {/* ── Video panel ─────────────────────────────────────────────── */}
        {/*
            Mobile:  Takes ~45% of what's left after header+controls (dvh-aware).
                     We use a fixed aspect-ratio container so it never collapses.
            Desktop: Takes 55% of the row, any height.
        */}
        <div className="relative flex flex-col
          h-[40dvh] shrink-0
          md:h-auto md:flex-1 md:shrink
          md:w-[55%] md:max-w-[55%]
          bg-black overflow-hidden
          border-b md:border-b-0 md:border-r border-gray-200 dark:border-gray-700
        ">
        <div
          ref={videoPanelRef}
          className="relative flex flex-col h-full"
        >
          {/* Remote video — simple block fill, no absolute needed */}
          <video
            ref={remoteVideoRef}
            autoPlay
            playsInline
            className="w-full h-full object-cover flex-1"
          />

          {/* Overlay when not connected */}
          {cameraError ? (
            <div className="absolute inset-0 flex flex-col items-center justify-center px-4 z-10
                            bg-red-50 dark:bg-black/90 dark:backdrop-blur-md">
              <div className="w-14 h-14 sm:w-20 sm:h-20 mb-4 rounded-full
                              bg-red-100 dark:bg-red-500/20
                              flex items-center justify-center">
                <svg className="w-7 h-7 sm:w-9 sm:h-9 text-red-500 dark:text-red-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
                </svg>
              </div>
              <h2 className="text-lg sm:text-2xl font-bold mb-2 text-center text-gray-900 dark:text-white">Camera Access Required</h2>
              <p className="max-w-xs text-center text-sm sm:text-base leading-relaxed text-red-600 dark:text-gray-300">{cameraError}</p>
            </div>
          ) : status !== 'connected' && (
            <div className="absolute inset-0 flex flex-col items-center justify-center px-4 z-10
                            bg-gray-100/95 dark:bg-black/70 dark:backdrop-blur-sm">
              <div className="w-14 h-14 sm:w-20 sm:h-20 mb-4 rounded-full
                              bg-indigo-100 dark:bg-indigo-500/20
                              flex items-center justify-center">
                {status === 'searching' ? (
                  <div className="w-9 h-9 border-4 border-indigo-500 dark:border-indigo-400 border-t-transparent rounded-full animate-spin" />
                ) : (
                  <svg className="w-7 h-7 sm:w-9 sm:h-9 text-indigo-500 dark:text-indigo-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M15 10l4.553-2.276A1 1 0 0121 8.618v6.764a1 1 0 01-1.447.894L15 14M5 18h8a2 2 0 002-2V8a2 2 0 00-2-2H5a2 2 0 00-2 2v8a2 2 0 002 2z" />
                  </svg>
                )}
              </div>
              <h2 className="text-xl sm:text-3xl font-bold tracking-tight mb-1 text-center text-gray-900 dark:text-white">
                {status === 'idle' ? 'Ready to connect' :
                  status === 'searching' ? 'Finding a partner…' :
                    status === 'disconnected' ? 'Disconnected' : ''}
              </h2>
              <p className="max-w-xs text-center text-sm sm:text-base text-gray-500 dark:text-gray-400">
                {status === 'searching'
                  ? 'Matching you with someone from around the globe.'
                  : 'Grant camera permission and press Search.'}
              </p>
            </div>
          )}

          {/* PiP local feed — scales with panel size */}
          <div
            ref={localPreviewRef}
            onPointerDown={handleLocalPreviewPointerDown}
            className={`absolute
                          w-[28%] min-w-[80px] max-w-[150px]
                          sm:min-w-[100px] sm:max-w-[180px]
                          aspect-video
                          bg-gradient-to-br from-white to-gray-200 dark:from-gray-900 dark:to-gray-800 rounded-lg overflow-hidden
                          shadow-2xl border-2 border-gray-300/80 dark:border-white/20 z-20
                          group touch-none cursor-grab active:cursor-grabbing
                          ${isDraggingLocalPreview ? '' : 'transition-[left,top,transform] duration-200 ease-out hover:scale-105'}
                          ${localPreviewPosition ? '' : 'bottom-3 right-3'}`}
            style={localPreviewPosition ? { left: localPreviewPosition.x, top: localPreviewPosition.y } : undefined}
          >
            <video
              ref={localVideoRef}
              autoPlay playsInline muted
              className="w-full h-full object-cover"
            />
            <div className="absolute inset-0 pointer-events-none bg-[radial-gradient(circle_at_top,rgba(255,255,255,0.25),transparent_60%)] dark:bg-[radial-gradient(circle_at_top,rgba(255,255,255,0.08),transparent_60%)]" />
            <div className="absolute bottom-1 right-1.5 opacity-0 group-hover:opacity-100 transition-opacity">
              <span className="text-[9px] sm:text-[10px] bg-black/50 text-white px-1.5 py-0.5 rounded backdrop-blur">You</span>
            </div>
          </div>

          {/* Mobile-only status badge (desktop shows it in the chat header area) */}
          <div className="absolute top-2 left-2 md:hidden z-20">
            <span className="text-[10px] font-semibold uppercase tracking-wide bg-black/50 text-white px-2 py-1 rounded-full backdrop-blur">
              {status}
            </span>
          </div>
        </div>
        </div>

        {/* ── Chat panel ──────────────────────────────────────────────── */}
        {/*
            Mobile:  flex-1 so it takes all remaining vertical space after video.
            Desktop: fixed at 45% width, full height.
        */}
        <div className="flex-1 flex flex-col min-h-0
                        md:w-[45%] md:max-w-[45%]
                        bg-white dark:bg-gray-900">

          {/* Messages scrollable area */}
          <div className="flex-1 overflow-y-auto p-3 sm:p-4 space-y-3" id="messages">
            {status === 'idle' && (
              <div className="h-full flex flex-col items-center justify-center text-center opacity-40 space-y-2">
                <svg className="w-9 h-9 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1} d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" />
                </svg>
                <p className="text-sm font-medium text-gray-500 dark:text-gray-400">Text chat will appear here</p>
              </div>
            )}

            {status === 'searching' && (
              <div className="flex justify-center mt-4">
                <div className="px-4 py-2 bg-indigo-50 border border-indigo-100 dark:bg-indigo-900/30 dark:border-indigo-800/50 rounded-full text-sm text-indigo-700 dark:text-indigo-300 font-medium shadow-sm flex items-center gap-2">
                  <div className="w-4 h-4 border-2 border-indigo-500 dark:border-indigo-400 border-t-transparent rounded-full animate-spin"></div>
                  Searching for a stranger...
                </div>
              </div>
            )}

            {messages.length === 0 && status === 'connected' && (
              <div className="flex justify-center mt-4">
                <div className="px-3 py-2 bg-indigo-50 border border-indigo-100 dark:bg-indigo-900/30 dark:border-indigo-800/50 rounded-full text-sm text-indigo-700 dark:text-indigo-300 font-medium shadow-sm">
                  You're connected! Say hi. 👋
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
                  <div className="px-3 py-1.5 bg-gray-100 dark:bg-gray-800 rounded-full text-xs text-gray-500 dark:text-gray-400 font-medium">
                    {msg.text}
                  </div>
                ) : (
                  <div className={`max-w-[85%] sm:max-w-[88%] px-4 py-2 rounded-2xl shadow-sm text-sm sm:text-base ${msg.sender === 'me'
                      ? 'bg-indigo-600 text-white rounded-tr-sm'
                      : 'bg-gray-100 dark:bg-gray-800 text-gray-900 dark:text-white border border-gray-200 dark:border-gray-700 rounded-tl-sm'
                    }`}>
                    <p className="whitespace-pre-wrap break-words">{DOMPurify.sanitize(msg.text)}</p>
                  </div>
                )}
              </div>
            ))}

            {/* Typing indicator */}
            <div className={`flex justify-start transition-opacity duration-200 ${peerTyping ? 'opacity-100' : 'opacity-0'}`}>
              <div className="px-4 py-3 bg-gray-100 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-2xl rounded-tl-sm flex items-center gap-1 shadow-sm">
                {[0, 150, 300].map(delay => (
                  <div key={delay} className="w-1.5 h-1.5 bg-gray-500 dark:bg-gray-400 rounded-full animate-bounce" style={{ animationDelay: `${delay}ms` }} />
                ))}
              </div>
            </div>

            <div ref={messagesEndRef} />
          </div>

          {/* ── Action bar (always visible, never scrolls away) ─────── */}
          <div className="p-3 sm:p-4 border-t border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800 shrink-0">

            {(status === 'idle' || status === 'disconnected') && (
              <div className="flex flex-col gap-2">
                {status === 'disconnected' && canReportLastChat && (
                  <button
                    onClick={() => setShowReport(true)}
                    className="w-full py-3 sm:py-3.5 bg-orange-100 hover:bg-orange-200 dark:bg-orange-900/30 dark:hover:bg-orange-900/50 text-orange-600 dark:text-orange-400 font-semibold rounded-xl text-sm sm:text-base transition-colors"
                  >
                    Report Last Chat
                  </button>
                )}
                <button
                  onClick={startSearch}
                  disabled={!connected}
                  className="w-full py-3 sm:py-3.5 bg-indigo-600 hover:bg-indigo-700
                             disabled:bg-gray-300 dark:disabled:bg-gray-600
                             text-white font-semibold rounded-xl text-sm sm:text-base
                             transition-colors shadow-lg shadow-indigo-600/20"
                >
                  {connected
                    ? (status === 'disconnected' ? 'Search Again' : 'Find a Stranger')
                    : 'Connecting to server…'}
                </button>
              </div>
            )}

            {status === 'searching' && (
              <button
                onClick={stopSearch}
                className="w-full py-3 sm:py-3.5 bg-red-100 dark:bg-red-900/30
                           text-red-600 dark:text-red-400 font-semibold rounded-xl
                           hover:bg-red-200 dark:hover:bg-red-900/50 transition-colors"
              >
                Cancel Search
              </button>
            )}

            {status === 'connected' && (
              <div className="flex flex-col gap-2">
                {/* Message input */}
                <form onSubmit={handleSend} className="flex gap-2 items-center">
                  <input
                    type="text"
                    value={input}
                    onChange={handleInputChange}
                    placeholder="Type a message…"
                    maxLength={2000}
                    className="flex-1 px-4 py-2.5 bg-white dark:bg-gray-700/50
                      border border-gray-300 dark:border-gray-600
                      rounded-full text-base
                      text-gray-900 dark:text-white placeholder-gray-500
                      focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-indigo-500
                      transition-all shadow-sm min-w-0"
                    autoFocus
                  />
                  <button
                    type="submit"
                    disabled={!input.trim()}
                    className="shrink-0 px-4 py-2.5 bg-indigo-600 hover:bg-indigo-700
                      disabled:opacity-50 disabled:bg-gray-400
                      text-white font-semibold rounded-full
                      transition-all shadow-sm flex items-center justify-center h-full"
                  >
                    <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 19l9 2-9-18-9 18 9-2zm0 0v-8" />
                    </svg>
                  </button>
                </form>
                {input.length > 0 && (
                  <div className={`text-center text-xs font-medium transition-all duration-200 ${
                    input.length >= 1800 ? 'text-orange-500' : 'text-gray-400 dark:text-gray-500'
                  }`}>
                    {input.length} / 2000 characters
                  </div>
                )}

                {/* Control buttons */}
                <div className="grid grid-cols-3 gap-2">
                  <button
                    onClick={handleNext}
                    className={`py-2 sm:py-2.5 font-semibold rounded-xl text-xs sm:text-sm transition-colors ${confirmSkip
                        ? 'bg-blue-600 hover:bg-blue-700 text-white shadow-lg'
                        : 'bg-blue-100 hover:bg-blue-200 dark:bg-blue-900/30 dark:hover:bg-blue-900/50 text-blue-600 dark:text-blue-400'
                      }`}
                  >
                    {confirmSkip ? 'Sure?' : 'Skip'}
                  </button>
                  <button
                    onClick={() => setShowReport(true)}
                    className="py-2 sm:py-2.5 bg-orange-100 hover:bg-orange-200
                               dark:bg-orange-900/30 dark:hover:bg-orange-900/50
                               text-orange-600 dark:text-orange-400
                               font-semibold rounded-xl transition-colors text-xs sm:text-sm"
                  >
                    Report
                  </button>
                  <button
                    onClick={handleDisconnect}
                    className={`py-2 sm:py-2.5 font-semibold rounded-xl text-xs sm:text-sm transition-colors ${confirmStop
                        ? 'bg-red-600 hover:bg-red-700 text-white shadow-lg'
                        : 'bg-red-100 hover:bg-red-200 dark:bg-red-900/30 dark:hover:bg-red-900/50 text-red-600 dark:text-red-400'
                      }`}
                  >
                    {confirmStop ? 'Confirm' : 'Stop'}
                  </button>
                </div>
              </div>
            )}
          </div>
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
