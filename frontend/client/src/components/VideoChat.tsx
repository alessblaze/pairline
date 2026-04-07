import { useState, useRef, useEffect, useCallback, memo } from 'react';
import { useNavigate, useLocation } from 'react-router-dom';
import { useVideoChat } from '../hooks/useVideoChat';
import { ReportDialog } from './ReportDialog';
import { ThemeToggle } from './ThemeToggle';
import { EntryModal } from './EntryModal';
import DOMPurify from 'dompurify';

interface VideoChatProps {
  wsUrl: string;
}

const LOCAL_PREVIEW_EDGE_MARGIN = 12;

// ---------------------------------------------------------------------------
// VideoChatInput — isolated so keystrokes ONLY re-render this small component.
// ---------------------------------------------------------------------------
interface VideoChatInputProps {
  onSend: (text: string) => void;
  onTyping: (isTyping: boolean) => void;
  onNext: () => void;
  onReport: () => void;
  onDisconnect: () => void;
  confirmSkip: boolean;
  confirmStop: boolean;
}

const VideoChatInput = memo(function VideoChatInput({
  onSend, onTyping, onNext, onReport, onDisconnect, confirmSkip, confirmStop,
}: VideoChatInputProps) {
  const [value, setValue] = useState('');
  const typingTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => () => {
    if (typingTimeoutRef.current) clearTimeout(typingTimeoutRef.current);
  }, []);

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const v = e.target.value;
    setValue(v);
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
    <div className="flex flex-col gap-2">
      <form onSubmit={handleSubmit} className="flex gap-2 items-center">
        <input
          id="video-chat-message-input"
          type="text"
          value={value}
          onChange={handleChange}
          placeholder="Type a message…"
          autoComplete="off"
          autoCorrect="off"
          spellCheck={false}
          className="flex-1 px-4 py-2.5 bg-white dark:bg-gray-700 border border-gray-300 dark:border-gray-600 rounded-full text-base focus:outline-none focus:ring-2 focus:ring-indigo-500"
        />
        <button type="submit" disabled={!value.trim()} className="p-2.5 bg-indigo-600 text-white rounded-full">
          <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path d="M12 19l9 2-9-18-9 18 9-2zm0 0v-8" /></svg>
        </button>
      </form>
      <div className="grid grid-cols-3 gap-2">
        <button
          onClick={onNext}
          className={`py-2.5 font-semibold rounded-xl text-xs sm:text-sm transition-colors ${
            confirmSkip ? 'bg-blue-600 text-white shadow-md' : 'bg-blue-50 dark:bg-blue-900/30 text-blue-600 dark:text-blue-400 hover:bg-blue-100 dark:hover:bg-blue-900/50'
          }`}
        >
          {confirmSkip ? 'Sure?' : 'Skip'}
        </button>
        <button
          onClick={onReport}
          className="py-2.5 font-semibold rounded-xl text-xs sm:text-sm transition-colors bg-orange-50 dark:bg-orange-900/30 text-orange-600 dark:text-orange-400 hover:bg-orange-100 dark:hover:bg-orange-900/50"
        >
          Report
        </button>
        <button
          onClick={onDisconnect}
          className={`py-2.5 font-semibold rounded-xl text-xs sm:text-sm transition-colors ${
            confirmStop ? 'bg-red-600 text-white shadow-md' : 'bg-red-50 dark:bg-red-900/30 text-red-600 dark:text-red-400 hover:bg-red-100 dark:hover:bg-red-900/50'
          }`}
        >
          {confirmStop ? 'Confirm' : 'Stop'}
        </button>
      </div>
    </div>
  );
});

// ---------------------------------------------------------------------------
export function VideoChat({ wsUrl }: VideoChatProps) {
  const navigate = useNavigate();
  const location = useLocation();
  const turnstileToken = location.state?.turnstileToken as string | undefined;
  const {
    connected, reportPeerId, sessionId, sessionToken, status, messages, peerTyping,
    isVideoConnecting,
    localVideoRef, remoteVideoRef,
    startSearch, stopSearch, skip, disconnect,
    sendMessage, sendTyping: rawSendTyping, cameraError
  } = useVideoChat(wsUrl);

  const [showVideoConnecting, setShowVideoConnecting] = useState(false);
  const showVideoConnectingSinceRef = useRef<number | null>(null);
  const showVideoConnectingTimersRef = useRef<{ show?: number; hide?: number }>({});

  const [remoteVideoHasRendered, setRemoteVideoHasRendered] = useState(false);
  const remoteVideoRenderedRef = useRef(false);

  useEffect(() => {
    if (status !== 'connected') {
      remoteVideoRenderedRef.current = false;
      setRemoteVideoHasRendered(false);
      return;
    }

    remoteVideoRenderedRef.current = false;
    setRemoteVideoHasRendered(false);
  }, [status, reportPeerId]);

  useEffect(() => {
    const video = remoteVideoRef.current;
    if (!video) return;

    const markRendered = () => {
      if (remoteVideoRenderedRef.current) return;
      remoteVideoRenderedRef.current = true;
      setRemoteVideoHasRendered(true);
    };

    video.addEventListener('loadeddata', markRendered);
    video.addEventListener('canplay', markRendered);
    video.addEventListener('playing', markRendered);

    return () => {
      video.removeEventListener('loadeddata', markRendered);
      video.removeEventListener('canplay', markRendered);
      video.removeEventListener('playing', markRendered);
    };
  }, [remoteVideoRef, status, reportPeerId]);

  useEffect(() => {
    const timers = showVideoConnectingTimersRef.current;
    if (timers.show) window.clearTimeout(timers.show);
    if (timers.hide) window.clearTimeout(timers.hide);
    timers.show = undefined;
    timers.hide = undefined;

    const now = Date.now();

    if (status === 'connected' && isVideoConnecting) {
      timers.show = window.setTimeout(() => {
        showVideoConnectingSinceRef.current = Date.now();
        setShowVideoConnecting(true);
      }, 150);
      return;
    }

    const since = showVideoConnectingSinceRef.current;
    const visibleForMs = since ? now - since : 0;
    const minVisibleMs = 300;
    const hideDelay = Math.max(0, minVisibleMs - visibleForMs);
    timers.hide = window.setTimeout(() => {
      showVideoConnectingSinceRef.current = null;
      setShowVideoConnecting(false);
    }, hideDelay);

    return () => {
      const timers = showVideoConnectingTimersRef.current;
      if (timers.show) window.clearTimeout(timers.show);
      if (timers.hide) window.clearTimeout(timers.hide);
      timers.show = undefined;
      timers.hide = undefined;
    };
  }, [status, isVideoConnecting]);

  // Stabilise sendTyping identity so the memoised VideoChatInput doesn't re-render on every parent state change
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

  // input state now lives in <VideoChatInput> — removed from parent to isolate keystroke re-renders
  const [interestTags, setInterestTags] = useState<string[]>([]);
  const [tagInput, setTagInput] = useState('');
  const [showEntryModal, setShowEntryModal] = useState(false);
  const [pendingInterests, setPendingInterests] = useState('');
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const [confirmStop, setConfirmStop] = useState(false);
  const [confirmSkip, setConfirmSkip] = useState(false);
  const [showReport, setShowReport] = useState(false);
  const [localPreviewPosition, setLocalPreviewPosition] = useState<{ x: number; y: number } | null>(null);
  const [isDraggingLocalPreview, setIsDraggingLocalPreview] = useState(false);
  // typingTimeoutRef moved to VideoChatInput
  const videoPanelRef = useRef<HTMLDivElement>(null);
  const localPreviewRef = useRef<HTMLDivElement>(null);
  const dragOffsetRef = useRef<{ x: number; y: number } | null>(null);

  const scrollToBottom = () => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'instant' });
  };

  useEffect(() => { scrollToBottom(); }, [messages]);

  useEffect(() => {
    if (status !== 'connected') {
      setConfirmStop(false);
      setConfirmSkip(false);
    }
  }, [status]);


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

    if (isDraggingLocalPreview) {
      window.addEventListener('pointermove', handlePointerMove);
      window.addEventListener('pointerup', handlePointerUp);
    }
    return () => {
      window.removeEventListener('pointermove', handlePointerMove);
      window.removeEventListener('pointerup', handlePointerUp);
    };
  }, [isDraggingLocalPreview]);

  // handleSend and handleInputChange now live inside VideoChatInput
  const handleSend = useCallback((text: string) => {
    sendMessage(text);
  }, [sendMessage]);

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
    <div className="fixed inset-0 flex flex-col bg-gray-100 dark:bg-gray-900 overflow-hidden">
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

      <div className="flex-1 flex flex-col md:flex-row min-h-0">
        <div className="relative flex-1 flex flex-col min-h-0 md:w-[55%] bg-black overflow-hidden border-b md:border-b-0 md:border-r border-gray-200 dark:border-gray-700" ref={videoPanelRef}>
          <video
            ref={remoteVideoRef}
            autoPlay
            playsInline
            className="w-full h-full object-cover"
          />

          {cameraError ? (
            <div className="absolute inset-0 flex flex-col items-center justify-center px-4 z-10 bg-red-50 dark:bg-black/90">
              <h2 className="text-lg sm:text-2xl font-bold mb-2 text-center text-gray-900 dark:text-white">Camera Error</h2>
              <p className="max-w-xs text-center text-sm text-red-600 dark:text-gray-300">{cameraError}</p>
            </div>
          ) : status === 'searching' ? (
            <div className="absolute inset-0 z-20 flex flex-col items-center justify-center px-4 bg-white/95 dark:bg-gray-900/90 animate-in fade-in duration-300">
              <div className="relative mb-6">
                <div className="w-16 h-16 sm:w-20 sm:h-20 border-4 border-indigo-200 border-t-indigo-600 dark:border-indigo-400/30 dark:border-t-indigo-400 rounded-full animate-spin"></div>
              </div>
              <p className="text-xl sm:text-2xl font-bold text-gray-900 dark:text-white mb-4">Finding your match...</p>
              {interestTags.length > 0 && (
                <div className="flex flex-wrap justify-center gap-2 max-w-[80%]">
                  {interestTags.map((tag, i) => (
                    <span key={i} className="px-3 py-1 bg-indigo-50 text-indigo-700 border border-indigo-200 dark:bg-white/10 dark:text-white dark:border-white/20 text-xs font-semibold rounded-full">
                      #{tag}
                    </span>
                  ))}
                </div>
              )}
            </div>
          ) : status === 'connected' && showVideoConnecting && !remoteVideoHasRendered ? (
            <div className="absolute inset-0 z-20 flex flex-col items-center justify-center px-4 bg-black/55 backdrop-blur-[2px] animate-in fade-in duration-300">
              <div className="relative mb-5">
                <div className="w-14 h-14 sm:w-16 sm:h-16 border-4 border-white/20 border-t-white rounded-full animate-spin"></div>
              </div>
              <p className="text-lg sm:text-xl font-semibold text-white text-center">Connecting video...</p>
            </div>
          ) : status !== 'connected' && (
            <div className="absolute inset-0 flex flex-col items-center justify-center px-4 z-10 bg-gray-100/95 dark:bg-black/80">
              <div className="w-14 h-14 sm:w-20 sm:h-20 mb-4 rounded-full bg-indigo-100 dark:bg-indigo-500/20 flex items-center justify-center">
                <svg className="w-7 h-7 sm:w-9 sm:h-9 text-indigo-500 dark:text-indigo-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M15 10l4.553-2.276A1 1 0 0121 8.618v6.764a1 1 0 01-1.447.894L15 14M5 18h8a2 2 0 002-2V8a2 2 0 00-2-2H5a2 2 0 00-2 2v8a2 2 0 002 2z" />
                </svg>
              </div>
              <h2 className="text-xl sm:text-3xl font-bold tracking-tight mb-2 text-center text-gray-900 dark:text-white">
                {status === 'idle' ? 'Ready to connect' : 'Disconnected'}
              </h2>
            </div>
          )}

          <div
            ref={localPreviewRef}
            onPointerDown={handleLocalPreviewPointerDown}
            className={`absolute w-[28%] min-w-[80px] max-w-[150px] aspect-video bg-gray-900 rounded-lg overflow-hidden shadow-2xl border-2 border-white/20 z-20 touch-none cursor-grab active:cursor-grabbing ${isDraggingLocalPreview ? '' : 'transition-[left,top,transform] duration-200'} ${localPreviewPosition ? '' : 'bottom-3 right-3'}`}
            style={localPreviewPosition ? { left: localPreviewPosition.x, top: localPreviewPosition.y } : undefined}
          >
            <video ref={localVideoRef} autoPlay playsInline muted className="w-full h-full object-cover" />
          </div>
        </div>

        <div className="flex-1 flex flex-col min-h-0 md:w-[45%] bg-white dark:bg-gray-900 border-l border-gray-200 dark:border-gray-700">
          <div className="flex-1 min-h-0 overflow-y-auto p-3 sm:p-4 space-y-3 sm:space-y-4 flex flex-col" id="messages">
            {messages.map((msg) => (
              <div key={msg.id} className={`flex ${msg.sender === 'system' ? 'justify-center' : msg.sender === 'me' ? 'justify-end' : 'justify-start'}`}>
                <div className={`max-w-[85%] px-4 py-2 rounded-2xl text-sm ${msg.sender === 'system' ? 'bg-gray-100 dark:bg-gray-800 text-gray-500' : msg.sender === 'me' ? 'bg-indigo-600 text-white' : 'bg-gray-100 dark:bg-gray-800 text-gray-900 dark:text-white'}`}>
                  <p className="whitespace-pre-wrap break-words">{DOMPurify.sanitize(msg.text)}</p>
                </div>
              </div>
            ))}
            {status === 'idle' && (
              <div className="flex-1 flex flex-col items-center justify-center text-center px-4 animate-in fade-in duration-500">
                <h3 className="text-2xl sm:text-3xl font-extrabold tracking-tight bg-gradient-to-r from-indigo-600 to-purple-600 bg-clip-text text-transparent mb-3">
                  Pairline
                </h3>
                <p className="text-sm text-gray-500 dark:text-gray-400 max-w-[220px] mx-auto leading-relaxed">
                  Start searching to connect instantly via text and video.
                </p>
              </div>
            )}
            <div className={`flex justify-start transition-opacity ${peerTyping ? 'opacity-100' : 'opacity-0'}`}>
              <div className="px-4 py-3 bg-gray-100 dark:bg-gray-800 rounded-2xl flex items-center gap-1">
                <div className="w-1.5 h-1.5 bg-gray-500 rounded-full animate-bounce" />
                <div className="w-1.5 h-1.5 bg-gray-500 rounded-full animate-bounce [animation-delay:150ms]" />
                <div className="w-1.5 h-1.5 bg-gray-500 rounded-full animate-bounce [animation-delay:300ms]" />
              </div>
            </div>
            <div ref={messagesEndRef} />
          </div>

          <div className="p-3 sm:p-4 border-t border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800 shrink-0">
            {(status === 'idle' || status === 'disconnected') && (
              <div className="flex flex-col gap-4 animate-in slide-in-from-bottom-4 duration-500">
                <div className="space-y-2">
                  <label className="block text-xs font-bold text-gray-500 dark:text-gray-400 uppercase tracking-widest ml-1">Interest Tags</label>
                  <div className="flex flex-wrap items-center gap-2 p-2 bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-xl min-h-[50px] focus-within:ring-2 focus-within:ring-indigo-500 shadow-sm transition-all">
                    {interestTags.map((tag, index) => (
                      <span key={index} className="flex items-center gap-1.5 px-2.5 py-1 bg-indigo-50 dark:bg-indigo-900/30 text-indigo-700 dark:text-indigo-300 text-sm font-medium rounded-lg">
                        {tag}
                        <button onClick={() => removeTag(index)} className="hover:text-indigo-900 transition-colors">
                          <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2.5} d="M6 18L18 6M6 6l12 12" /></svg>
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
                      placeholder={interestTags.length === 0 ? "e.g. coding, music..." : ""}
                      className="flex-1 min-w-[120px] bg-transparent border-none outline-none text-[16px] font-medium dark:text-white placeholder:text-gray-400 dark:placeholder:text-gray-500"
                    />
                  </div>
                  <p className="text-[10px] text-gray-400 dark:text-gray-500 ml-1">
                    Press Enter or Comma to add. Max 10 tags.
                  </p>
                </div>

                <div className="flex flex-col gap-2">
                  {status === 'disconnected' && canReportLastChat && (
                    <button
                      onClick={() => setShowReport(true)}
                      className="w-full py-2.5 sm:py-3 bg-orange-100 hover:bg-orange-200 dark:bg-orange-900/30 dark:hover:bg-orange-900/50 text-orange-600 dark:text-orange-400 font-semibold rounded-xl text-sm sm:text-base border border-orange-200 dark:border-orange-800/50 shadow-sm transition-all"
                    >
                      Report Last Chat
                    </button>
                  )}
                  <button
                    onClick={() => handleStartSearchClick(interestTags.join(','))}
                    disabled={!connected}
                    className="w-full py-3 bg-indigo-600 hover:bg-indigo-700 disabled:bg-gray-400 text-white font-bold rounded-xl shadow-lg shadow-indigo-600/20 active:scale-[0.98] transition-all"
                  >
                    {connected ? (status === 'disconnected' ? 'Search Again' : 'Find Partner') : 'Connecting...'}
                  </button>
                </div>
              </div>
            )}

            {status === 'searching' && (
              <button onClick={stopSearch} className="w-full py-3 bg-red-100 dark:bg-red-900/30 text-red-600 dark:text-red-400 font-semibold rounded-xl">Cancel Search</button>
            )}

            {status === 'connected' && (
              <VideoChatInput
                onSend={handleSend}
                onTyping={sendTyping}
                onNext={handleNext}
                onReport={() => setShowReport(true)}
                onDisconnect={handleDisconnect}
                confirmSkip={confirmSkip}
                confirmStop={confirmStop}
              />
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

      {showEntryModal && (
        <EntryModal
          onClose={() => setShowEntryModal(false)}
          onConfirm={handleModalConfirm}
        />
      )}
    </div>
  );
}
