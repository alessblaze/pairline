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
import { useVideoChat } from '../hooks/useVideoChat';
import { ReportDialog } from './ReportDialog';
import { ThemeToggle } from './ThemeToggle';
import { EntryModal } from './EntryModal';
import { BANNED_PHRASE_REASON } from '../chatModeration';
import DOMPurify from 'dompurify';
import type { ChatMessage } from '../types';

// ---------------------------------------------------------------------------
// Shared type for the hook return value, used by both the component and stories.
// ---------------------------------------------------------------------------
export interface VideoChatState {
  connected: boolean;
  status: 'idle' | 'searching' | 'connected' | 'disconnected';
  messages: ChatMessage[];
  peerTyping: boolean;
  startSearch: (interests?: string, turnstileToken?: string) => void;
  stopSearch: () => void;
  skip: () => void;
  disconnect: () => void;
  sendMessage: (text: string) => void;
  sendTyping: (isTyping: boolean) => void;
  reportPeerId: string | null;
  sessionId: string | null;
  sessionToken: string | null;
  isVideoConnecting: boolean;
  cameraError: string | null;
  localVideoRef: React.RefObject<HTMLVideoElement | null>;
  remoteVideoRef: React.RefObject<HTMLVideoElement | null>;
}

const LOCAL_PREVIEW_EDGE_MARGIN = 12;
const STACKED_VIDEO_RATIOS = [
  { key: '1:1', value: 1 },
  { key: '4:3', value: 4 / 3 },
  { key: '16:9', value: 16 / 9 },
] as const;
const PIP_SIZES = [
  { key: 'S', widthClass: 'w-[24%] min-w-[72px] max-w-[128px]' },
  { key: 'M', widthClass: 'w-[28%] min-w-[80px] max-w-[150px]' },
  { key: 'L', widthClass: 'w-[34%] min-w-[96px] max-w-[190px]' },
] as const;

type StackedVideoRatio = (typeof STACKED_VIDEO_RATIOS)[number]['key'];
type PipSize = (typeof PIP_SIZES)[number]['key'];

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
          className={`py-2.5 font-semibold rounded-xl text-xs sm:text-sm transition-colors ${confirmSkip ? 'bg-blue-600 text-white shadow-md' : 'bg-blue-50 dark:bg-blue-900/30 text-blue-600 dark:text-blue-400 hover:bg-blue-100 dark:hover:bg-blue-900/50'
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
          className={`py-2.5 font-semibold rounded-xl text-xs sm:text-sm transition-colors ${confirmStop ? 'bg-red-600 text-white shadow-md' : 'bg-red-50 dark:bg-red-900/30 text-red-600 dark:text-red-400 hover:bg-red-100 dark:hover:bg-red-900/50'
            }`}
        >
          {confirmStop ? 'Confirm' : 'Stop'}
        </button>
      </div>
    </div>
  );
});

// ---------------------------------------------------------------------------
// VideoChatView — the presentational component.
// Exported so Storybook stories can render it directly with mock state,
// without triggering the real WebSocket/WebRTC hooks.
// ---------------------------------------------------------------------------
export function VideoChatView({ state }: { state: VideoChatState }) {
  const navigate = useNavigate();
  const location = useLocation();
  const turnstileToken = location.state?.turnstileToken as string | undefined;

  const {
    connected, reportPeerId, sessionId, sessionToken, status, messages, peerTyping,
    isVideoConnecting,
    localVideoRef, remoteVideoRef,
    startSearch, stopSearch, skip, disconnect,
    sendMessage, sendTyping: rawSendTyping, cameraError
  } = state;

  const [videoLayout, setVideoLayout] = useState<'pip' | 'stacked'>(() => {
    return (localStorage.getItem('pairline-video-layout') as 'pip' | 'stacked') || 'pip';
  });
  const [isDesktopViewport, setIsDesktopViewport] = useState(() => (
    typeof window === 'undefined' ? true : window.innerWidth >= 768
  ));
  const [stackedVideoRatio, setStackedVideoRatio] = useState<StackedVideoRatio>(() => {
    const savedRatio = localStorage.getItem('pairline-stacked-video-ratio') as StackedVideoRatio | null;
    return STACKED_VIDEO_RATIOS.some(ratio => ratio.key === savedRatio) ? (savedRatio as StackedVideoRatio) : '4:3';
  });
  const [pipSize, setPipSize] = useState<PipSize>(() => {
    const savedSize = localStorage.getItem('pairline-pip-size') as PipSize | null;
    return PIP_SIZES.some(size => size.key === savedSize) ? (savedSize as PipSize) : 'M';
  });
  const contentLayoutRef = useRef<HTMLDivElement>(null);
  const [stackedVideoPanelWidth, setStackedVideoPanelWidth] = useState<number | null>(null);
  const effectiveVideoLayout = isDesktopViewport ? videoLayout : 'pip';

  const toggleVideoLayout = () => {
    setVideoLayout(prev => {
      const next = prev === 'pip' ? 'stacked' : 'pip';
      localStorage.setItem('pairline-video-layout', next);
      return next;
    });
  };

  const cycleStackedVideoRatio = () => {
    setStackedVideoRatio(prev => {
      const currentIndex = STACKED_VIDEO_RATIOS.findIndex(ratio => ratio.key === prev);
      const next = STACKED_VIDEO_RATIOS[(currentIndex + 1) % STACKED_VIDEO_RATIOS.length].key;
      localStorage.setItem('pairline-stacked-video-ratio', next);
      return next;
    });
  };

  const cyclePipSize = () => {
    setPipSize(prev => {
      const currentIndex = PIP_SIZES.findIndex(size => size.key === prev);
      const next = PIP_SIZES[(currentIndex + 1) % PIP_SIZES.length].key;
      localStorage.setItem('pairline-pip-size', next);
      return next;
    });
  };

  const [pipSwapped, setPipSwapped] = useState(false);
  const [isPipSwapTransitionDisabled, setIsPipSwapTransitionDisabled] = useState(false);
  const pipDragHasMovedRef = useRef(false);
  const pipSwapTransitionResetRef = useRef<number | null>(null);

  useEffect(() => {
    return () => {
      if (pipSwapTransitionResetRef.current !== null) {
        window.clearTimeout(pipSwapTransitionResetRef.current);
      }
    };
  }, []);

  useEffect(() => {
    const updateViewportMode = () => {
      setIsDesktopViewport(window.innerWidth >= 768);
    };

    updateViewportMode();
    window.addEventListener('resize', updateViewportMode);

    return () => {
      window.removeEventListener('resize', updateViewportMode);
    };
  }, []);

  const togglePip = () => {
    if (effectiveVideoLayout === 'pip') {
      setIsPipSwapTransitionDisabled(true);
      setPipSwapped(prev => !prev);
      if (pipSwapTransitionResetRef.current !== null) {
        window.clearTimeout(pipSwapTransitionResetRef.current);
      }
      pipSwapTransitionResetRef.current = window.setTimeout(() => {
        setIsPipSwapTransitionDisabled(false);
        pipSwapTransitionResetRef.current = null;
      }, 50);
    }
  };

  const [showVideoConnecting, setShowVideoConnecting] = useState(false);
  const showVideoConnectingSinceRef = useRef<number | null>(null);
  const showVideoConnectingTimersRef = useRef<{ show?: number; hide?: number }>({});
  const remoteVideoRenderKey = `${status}:${reportPeerId ?? 'none'}`;
  const [remoteVideoRenderedKey, setRemoteVideoRenderedKey] = useState<string | null>(null);
  const remoteVideoHasRendered = remoteVideoRenderedKey === remoteVideoRenderKey;

  useEffect(() => {
    const layout = contentLayoutRef.current;
    if (!layout) return;

    const updateStackedPanelWidth = () => {
      if (!isDesktopViewport || effectiveVideoLayout !== 'stacked') {
        setStackedVideoPanelWidth(null);
        return;
      }

      const layoutRect = layout.getBoundingClientRect();
      const ratioValue = STACKED_VIDEO_RATIOS.find(ratio => ratio.key === stackedVideoRatio)?.value ?? 4 / 3;
      const minimumChatWidth = 320;
      const maxWidth = Math.max(layoutRect.width - minimumChatWidth, layoutRect.width * 0.38);
      const idealWidth = (layoutRect.height / 2) * ratioValue;
      const clampedWidth = Math.max(280, Math.min(idealWidth, maxWidth));
      setStackedVideoPanelWidth(clampedWidth);
    };

    updateStackedPanelWidth();

    const resizeObserver = typeof ResizeObserver !== 'undefined'
      ? new ResizeObserver(updateStackedPanelWidth)
      : null;
    resizeObserver?.observe(layout);
    window.addEventListener('resize', updateStackedPanelWidth);

    return () => {
      resizeObserver?.disconnect();
      window.removeEventListener('resize', updateStackedPanelWidth);
    };
  }, [stackedVideoRatio, effectiveVideoLayout, isDesktopViewport]);

  useEffect(() => {
    const video = remoteVideoRef.current;
    if (!video) return;
    const renderKey = remoteVideoRenderKey;

    const markRendered = () => {
      setRemoteVideoRenderedKey(prev => (prev === renderKey ? prev : renderKey));
    };

    video.addEventListener('loadeddata', markRendered);
    video.addEventListener('canplay', markRendered);
    video.addEventListener('playing', markRendered);

    return () => {
      video.removeEventListener('loadeddata', markRendered);
      video.removeEventListener('canplay', markRendered);
      video.removeEventListener('playing', markRendered);
    };
  }, [remoteVideoRef, remoteVideoRenderKey]);

  useEffect(() => {
    const activeTimers = showVideoConnectingTimersRef.current;
    if (activeTimers.show) window.clearTimeout(activeTimers.show);
    if (activeTimers.hide) window.clearTimeout(activeTimers.hide);
    activeTimers.show = undefined;
    activeTimers.hide = undefined;

    const now = Date.now();

    if (status === 'connected' && isVideoConnecting) {
      activeTimers.show = window.setTimeout(() => {
        showVideoConnectingSinceRef.current = Date.now();
        setShowVideoConnecting(true);
      }, 150);
      return;
    }

    const since = showVideoConnectingSinceRef.current;
    const visibleForMs = since ? now - since : 0;
    const minVisibleMs = 300;
    const hideDelay = Math.max(0, minVisibleMs - visibleForMs);
    activeTimers.hide = window.setTimeout(() => {
      showVideoConnectingSinceRef.current = null;
      setShowVideoConnecting(false);
    }, hideDelay);

    return () => {
      if (activeTimers.show) window.clearTimeout(activeTimers.show);
      if (activeTimers.hide) window.clearTimeout(activeTimers.hide);
      activeTimers.show = undefined;
      activeTimers.hide = undefined;
    };
  }, [status, isVideoConnecting]);

  // Stabilise sendTyping identity so the memoised VideoChatInput doesn't re-render on every parent state change
  const sendTyping = useCallback((isTyping: boolean) => rawSendTyping(isTyping), [rawSendTyping]);

  // Mirrors the backend's captcha_verified socket flag.
  // Once verified, subsequent searches on the same WS connection skip the modal.
  // Resets when the WebSocket reconnects (new socket = new captcha_verified state).
  const captchaVerifiedRef = useRef(false);

  // Reset captchaVerified when WebSocket reconnects (new socket = fresh captcha state)
  useEffect(() => {
    if (!connected && captchaVerifiedRef.current) {
      captchaVerifiedRef.current = false;
    }
  }, [connected]);

  // Reset captchaVerified if the backend rejected our token
  useEffect(() => {
    if (!captchaVerifiedRef.current) return;
    const lastMsg = messages[messages.length - 1];
    if (lastMsg?.sender === 'system' && lastMsg?.text?.includes('CAPTCHA')) {
      captchaVerifiedRef.current = false;
    }
  }, [messages]);

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
  const [panelDimensions, setPanelDimensions] = useState<{ width: number; height: number } | null>(null);
  // typingTimeoutRef moved to VideoChatInput
  const videoPanelRef = useRef<HTMLDivElement>(null);
  const localPreviewRef = useRef<HTMLDivElement | null>(null);
  const dragOffsetRef = useRef<{ x: number; y: number; startX: number; startY: number } | null>(null);

  const clampLocalPreviewPosition = useCallback(() => {
    if (!videoPanelRef.current || !localPreviewRef.current) return;

    const panelRect = videoPanelRef.current.getBoundingClientRect();
    const previewRect = localPreviewRef.current.getBoundingClientRect();
    const maxX = Math.max(LOCAL_PREVIEW_EDGE_MARGIN, panelRect.width - previewRect.width - LOCAL_PREVIEW_EDGE_MARGIN);
    const maxY = Math.max(LOCAL_PREVIEW_EDGE_MARGIN, panelRect.height - previewRect.height - LOCAL_PREVIEW_EDGE_MARGIN);

    setLocalPreviewPosition(prev => {
      const currentX = prev?.x ?? panelRect.width - previewRect.width - LOCAL_PREVIEW_EDGE_MARGIN;
      const currentY = prev?.y ?? panelRect.height - previewRect.height - LOCAL_PREVIEW_EDGE_MARGIN;
      const snapToRight = currentX + previewRect.width / 2 >= panelRect.width / 2;
      const snapToBottom = currentY + previewRect.height / 2 >= panelRect.height / 2;
      const nextX = Math.min(Math.max(currentX, LOCAL_PREVIEW_EDGE_MARGIN), maxX);
      const nextY = Math.min(Math.max(currentY, LOCAL_PREVIEW_EDGE_MARGIN), maxY);

      return {
        x: snapToRight ? maxX : nextX,
        y: snapToBottom ? maxY : nextY,
      };
    });
  }, []);

  useEffect(() => {
    if (effectiveVideoLayout !== 'pip') return;

    const frame = window.requestAnimationFrame(() => {
      clampLocalPreviewPosition();
    });

    return () => window.cancelAnimationFrame(frame);
  }, [pipSize, pipSwapped, effectiveVideoLayout, clampLocalPreviewPosition]);

  // Track video panel dimensions reactively for watermark overlap detection.
  // Using ResizeObserver instead of reading clientHeight during render avoids
  // stale values and ensures the watermark reacts to viewport resizes.
  useEffect(() => {
    const panel = videoPanelRef.current;
    if (!panel) return;

    const update = () => setPanelDimensions({ width: panel.clientWidth, height: panel.clientHeight });
    update();

    const resizeObserver = new ResizeObserver(update);
    resizeObserver.observe(panel);

    return () => resizeObserver.disconnect();
  }, []);

  const systemMessageClass = (message: ChatMessage) => (
    message.systemReason === BANNED_PHRASE_REASON
      ? 'bg-red-100 text-red-700 shadow-[0_0_18px_rgba(239,68,68,0.28)] ring-1 ring-red-300/80 dark:bg-red-950/70 dark:text-red-200 dark:ring-red-500/40 dark:shadow-[0_0_22px_rgba(248,113,113,0.28)]'
      : 'bg-gray-100 dark:bg-gray-800 text-gray-500'
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

  const scrollToBottom = () => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'instant' });
  };

  useEffect(() => { scrollToBottom(); }, [messages]);


  useEffect(() => {
    const handlePointerMove = (event: PointerEvent) => {
      if (effectiveVideoLayout === 'stacked') return;
      if (!dragOffsetRef.current || !videoPanelRef.current || !localPreviewRef.current) return;

      const deltaX = Math.abs(event.clientX - dragOffsetRef.current.startX);
      const deltaY = Math.abs(event.clientY - dragOffsetRef.current.startY);
      if (deltaX > 4 || deltaY > 4) {
        pipDragHasMovedRef.current = true;
      }

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
      if (effectiveVideoLayout === 'stacked') {
        dragOffsetRef.current = null;
        setIsDraggingLocalPreview(false);
        return;
      }
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
  }, [isDraggingLocalPreview, effectiveVideoLayout]);

  // handleSend and handleInputChange now live inside VideoChatInput
  const handleSend = useCallback((text: string) => {
    sendMessage(text);
  }, [sendMessage]);

  const handleDisconnect = () => {
    if (!effectiveConfirmStop) {
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
    if (captchaVerifiedRef.current) {
      startSearch(interestsStr);
    } else if (turnstileToken) {
      startSearch(interestsStr, turnstileToken);
      captchaVerifiedRef.current = true;
      navigate(location.pathname, { replace: true, state: {} });
    } else {
      setPendingInterests(interestsStr);
      setShowEntryModal(true);
    }
  }, [
    turnstileToken,
    startSearch,
    navigate,
    location.pathname,
  ]);

  const handleModalConfirm = useCallback((token: string) => {
    setShowEntryModal(false);
    startSearch(pendingInterests, token);
    captchaVerifiedRef.current = true;
  }, [startSearch, pendingInterests]);

  const handleNext = () => {
    if (!effectiveConfirmSkip) {
      setConfirmSkip(true);
      setTimeout(() => setConfirmSkip(false), 3000);
    } else {
      skip();
      setConfirmSkip(false);
    }
  };

  const handleLocalPreviewPointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
    if (effectiveVideoLayout === 'stacked') return;
    if (!videoPanelRef.current || !localPreviewRef.current) return;
    pipDragHasMovedRef.current = false;

    const panelRect = videoPanelRef.current.getBoundingClientRect();
    const previewRect = localPreviewRef.current.getBoundingClientRect();

    dragOffsetRef.current = {
      x: event.clientX - previewRect.left,
      y: event.clientY - previewRect.top,
      startX: event.clientX,
      startY: event.clientY,
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
  const canReportLastChat = !!reportPeerId;
  const effectiveConfirmStop = status === 'connected' ? confirmStop : false;
  const effectiveConfirmSkip = status === 'connected' ? confirmSkip : false;

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
          <button
            onClick={toggleVideoLayout}
            aria-label="Toggle video layout"
            className="hidden md:flex p-1.5 rounded-lg hover:bg-gray-200 dark:hover:bg-gray-700 transition-colors text-gray-600 dark:text-gray-300"
            title="Toggle Video Layout"
          >
            {videoLayout === 'stacked' ? (
              <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <rect x="3" y="2" width="18" height="9" rx="2" ry="2" />
                <rect x="3" y="13" width="18" height="9" rx="2" ry="2" />
              </svg>
            ) : (
              <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <rect x="3" y="3" width="18" height="18" rx="2" ry="2" />
                <rect x="13" y="13" width="8" height="8" rx="1" ry="1" fill="currentColor" />
              </svg>
            )}
          </button>
          {effectiveVideoLayout === 'pip' && (
            <button
              onClick={cyclePipSize}
              aria-label="Toggle PiP size"
              className="flex items-center justify-center min-w-11 px-2 py-1.5 rounded-lg bg-gray-100 text-gray-700 hover:bg-gray-200 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700 transition-colors text-xs font-semibold tracking-wide"
              title="Toggle PiP Size"
            >
              {pipSize}
            </button>
          )}
          {effectiveVideoLayout === 'stacked' && (
            <button
              onClick={cycleStackedVideoRatio}
              aria-label="Toggle stacked video ratio"
              className="hidden md:flex items-center justify-center min-w-14 px-2.5 py-1.5 rounded-lg bg-gray-100 text-gray-700 hover:bg-gray-200 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700 transition-colors text-xs font-semibold tracking-wide"
              title="Toggle Stacked Video Ratio"
            >
              {stackedVideoRatio}
            </button>
          )}
          <ThemeToggle />
        </div>
      </header>

      <div className="flex-1 flex flex-col md:flex-row min-h-0" ref={contentLayoutRef}>
        <div
          className={`relative flex-1 flex flex-col min-h-0 bg-black overflow-hidden border-b md:border-b-0 md:border-r border-gray-200 dark:border-gray-700 ${effectiveVideoLayout === 'stacked' ? 'md:flex-none' : 'md:w-[55%]'}`}
          style={effectiveVideoLayout === 'stacked' && stackedVideoPanelWidth ? { width: `${stackedVideoPanelWidth}px` } : undefined}
          ref={videoPanelRef}
        >
          {(() => {
            const isLocalPip = !(effectiveVideoLayout === 'pip' && pipSwapped);
            const isRemoteMainVideo = effectiveVideoLayout === 'stacked' || isLocalPip;
            const shouldShowWatermark = status === 'connected' && remoteVideoHasRendered;
            const overlayContainerClasses = effectiveVideoLayout === 'stacked'
              ? 'pointer-events-none absolute inset-0 z-30 md:h-1/2 md:inset-auto md:w-full md:top-0 md:left-0'
              : 'pointer-events-none absolute inset-0 z-10';
            const mainVideoTransitionClasses = isPipSwapTransitionDisabled ? '' : 'transition-[width,height] duration-300';
            const pipVideoTransitionClasses = isDraggingLocalPreview || isPipSwapTransitionDisabled ? '' : 'transition-[left,top,width,height,transform] duration-300';
            const pipSizeClasses = PIP_SIZES.find(size => size.key === pipSize)?.widthClass ?? PIP_SIZES[1].widthClass;
            // Watermark overlap: proportional thresholds that adapt to any panel size.
            const isPipOverlappingWatermark = effectiveVideoLayout === 'pip'
              && localPreviewPosition != null
              && panelDimensions != null
              && panelDimensions.height > 0
              && localPreviewPosition.x < panelDimensions.width * 0.35
              && localPreviewPosition.y > panelDimensions.height * 0.65;
            // Position via inline `top`+`left` so CSS transitions animate smoothly
            // (switching between `top`/`bottom` classes can't transition from `auto`).
            // No `z-index` on the wrapper — it promotes the element to its own compositor
            // layer, which breaks `mix-blend-difference` on the text against the video.
            const WATERMARK_INSET = 12;
            const WATERMARK_EST_HEIGHT = 52;
            const watermarkPositionClasses = 'pointer-events-none absolute transition-[top,left] duration-500 ease-out';
            const watermarkStyle: React.CSSProperties = isPipOverlappingWatermark
              ? { top: 20, left: 16 }
              : panelDimensions
                ? { top: panelDimensions.height - WATERMARK_EST_HEIGHT - WATERMARK_INSET, left: WATERMARK_INSET }
                : { bottom: WATERMARK_INSET, left: WATERMARK_INSET };
            const mainVideoClasses = `absolute inset-0 z-0 isolate bg-black ${effectiveVideoLayout === 'stacked' ? 'md:relative md:flex-1 md:min-h-0 md:z-10' : ''} ${mainVideoTransitionClasses} overflow-hidden`;
            const pipVideoClasses = `absolute z-20 isolate ${pipSizeClasses} aspect-video bg-gray-900 rounded-lg overflow-hidden shadow-2xl border-2 border-white/20 touch-none cursor-grab active:cursor-grabbing ${pipVideoTransitionClasses} ${localPreviewPosition ? '' : 'bottom-3 right-3'} ${effectiveVideoLayout === 'stacked' ? 'md:static md:w-full md:max-w-none md:flex-1 md:aspect-auto md:border-none md:rounded-none md:border-t md:border-gray-200 dark:md:border-gray-700 md:shadow-none md:touch-auto md:cursor-auto md:z-10' : ''}`;
            const pipStyle = effectiveVideoLayout === 'pip' ? (localPreviewPosition ? { left: localPreviewPosition.x, top: localPreviewPosition.y } : undefined) : undefined;
            const watermark = (
              <span
                className="text-4xl sm:text-5xl tracking-wide text-white mix-blend-difference"
                style={{ fontFamily: "'Cedarville Cursive', cursive" }}
                aria-hidden="true"
              >
                Pairline
              </span>
            );
            const overlayContent = cameraError ? (
              <div className="absolute inset-0 flex flex-col items-center justify-center px-4 bg-red-50 dark:bg-black/90">
                <h2 className="text-lg sm:text-2xl font-bold mb-2 text-center text-gray-900 dark:text-white">Camera Error</h2>
                <p className="max-w-xs text-center text-sm text-red-600 dark:text-gray-300">{cameraError}</p>
              </div>
            ) : status === 'searching' ? (
              <div className="absolute inset-0 flex flex-col items-center justify-center px-4 bg-white/95 dark:bg-gray-900/90 animate-in fade-in duration-300">
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
              <div className="absolute inset-0 flex flex-col items-center justify-center px-4 bg-black/55 backdrop-blur-[2px] animate-in fade-in duration-300">
                <div className="relative mb-5">
                  <div className="w-14 h-14 sm:w-16 sm:h-16 border-4 border-white/20 border-t-white rounded-full animate-spin"></div>
                </div>
                <p className="text-lg sm:text-xl font-semibold text-white text-center">Connecting video...</p>
              </div>
            ) : status !== 'connected' ? (
              <div className="absolute inset-0 flex flex-col items-center justify-center px-4 bg-gray-100/95 dark:bg-black/80">
                <div className="w-14 h-14 sm:w-20 sm:h-20 mb-4 rounded-full bg-indigo-100 dark:bg-indigo-500/20 flex items-center justify-center">
                  <svg className="w-7 h-7 sm:w-9 sm:h-9 text-indigo-500 dark:text-indigo-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M15 10l4.553-2.276A1 1 0 0121 8.618v6.764a1 1 0 01-1.447.894L15 14M5 18h8a2 2 0 002-2V8a2 2 0 00-2-2H5a2 2 0 00-2 2v8a2 2 0 002 2z" />
                  </svg>
                </div>
                <h2 className="text-xl sm:text-3xl font-bold tracking-tight mb-2 text-center text-gray-900 dark:text-white">
                  {status === 'idle' ? 'Ready to connect' : 'Disconnected'}
                </h2>
              </div>
            ) : null;

            const handlePipClick = () => {
              if (!pipDragHasMovedRef.current) togglePip();
            };

            return (
              <>
                <div
                  ref={!isLocalPip ? (el) => { localPreviewRef.current = el; } : undefined}
                  onPointerDown={!isLocalPip ? handleLocalPreviewPointerDown : undefined}
                  onClick={!isLocalPip ? handlePipClick : undefined}
                  className={isLocalPip ? mainVideoClasses : pipVideoClasses}
                  style={!isLocalPip ? pipStyle : undefined}
                  data-testid="remote-video-container"
                >
                  <video
                    ref={remoteVideoRef}
                    autoPlay
                    playsInline
                    className="w-full h-full object-cover"
                  />
                  {shouldShowWatermark && isRemoteMainVideo && (
                    <div className={watermarkPositionClasses} style={watermarkStyle}>
                      {watermark}
                    </div>
                  )}
                </div>

                <div
                  ref={isLocalPip ? (el) => { localPreviewRef.current = el; } : undefined}
                  onPointerDown={isLocalPip ? handleLocalPreviewPointerDown : undefined}
                  onClick={isLocalPip ? handlePipClick : undefined}
                  className={isLocalPip ? pipVideoClasses : mainVideoClasses}
                  style={isLocalPip ? pipStyle : undefined}
                  data-testid="local-video-container"
                >
                  <video ref={localVideoRef} autoPlay playsInline muted className="w-full h-full object-cover shadow-[inherit]" />
                  {shouldShowWatermark && !isRemoteMainVideo && (
                    <div className={watermarkPositionClasses} style={watermarkStyle}>
                      {watermark}
                    </div>
                  )}
                </div>

                {overlayContent && (
                  <div className={overlayContainerClasses}>
                    {overlayContent}
                  </div>
                )}

              </>
            );
          })()}
        </div>

        <div className="flex-1 flex flex-col min-h-0 md:w-[45%] bg-white dark:bg-gray-900 border-l border-gray-200 dark:border-gray-700">
          <div className="flex-1 min-h-0 overflow-y-auto p-3 sm:p-4 space-y-3 sm:space-y-4 flex flex-col" id="messages">
            {messages.map((msg) => (
              <div key={msg.id} className={`flex ${msg.sender === 'system' ? 'justify-center' : msg.sender === 'me' ? 'justify-end' : 'justify-start'}`}>
                <div className={`max-w-[85%] px-4 py-2 rounded-2xl text-sm transition-all ${msg.sender === 'system' ? systemMessageClass(msg) : msg.sender === 'me' ? myMessageClass(msg.deliveryStatus) : 'bg-gray-100 dark:bg-gray-800 text-gray-900 dark:text-white'}`}>
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
                confirmSkip={effectiveConfirmSkip}
                confirmStop={effectiveConfirmStop}
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

// ---------------------------------------------------------------------------
// VideoChat — the connected wrapper that plugs in the real WebSocket hook.
// ---------------------------------------------------------------------------
export function VideoChat({ wsUrl }: { wsUrl: string }) {
  const state = useVideoChat(wsUrl);
  return <VideoChatView state={state} />;
}
