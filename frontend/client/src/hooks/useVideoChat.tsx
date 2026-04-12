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

import { useState, useEffect, useRef } from 'react';
import { WebSocketClient } from '../services/websocket';
import { useNetworkHealth } from './useNetworkHealth';
import { BANNED_PHRASE_REASON, BLOCKED_PHRASE_NOTICE } from '../chatModeration';
import type { ChatMessage, Message } from '../types';

const isCallerSession = (sessionId: string, peerId: string) => sessionId > peerId;
const turnEnabled = import.meta.env.VITE_ENABLE_TURN !== 'false';
const webrtcDebugEnabled = import.meta.env.VITE_WEBRTC_DEBUG !== 'false';
const forceRelay = import.meta.env.VITE_FORCE_RELAY === 'true';
const hasTurnServer = (iceServers: RTCIceServer[]) =>
  iceServers.some(server => {
    const urls = Array.isArray(server.urls) ? server.urls : [server.urls];
    return urls.some(url => typeof url === 'string' && (url.startsWith('turn:') || url.startsWith('turns:')));
  });
const getSdpMediaSections = (sdp?: string) =>
  (sdp ?? '')
    .split('\n')
    .map(line => line.trim())
    .filter(line => line.startsWith('m='));
const parseIceCandidate = (candidate?: string) => {
  if (!candidate) return {};
  const parts = candidate.trim().split(/\s+/);
  const typeIndex = parts.indexOf('typ');
  const protocol = parts[2];
  const address = parts[4];
  const port = parts[5];
  const candidateType = typeIndex >= 0 ? parts[typeIndex + 1] : undefined;
  return { protocol, address, port, candidateType, raw: candidate };
};

export function useVideoChat(wsUrl: string) {
  const { setChannelStatus, removeChannel, setApiStatus } = useNetworkHealth();
  const [wsClient] = useState(() => new WebSocketClient(wsUrl, 'room:video'));
  const [connected, setConnected] = useState(false);
  const [peerId, setPeerId] = useState<string | null>(null);
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [sessionToken, setSessionToken] = useState<string | null>(null);
  const [reportPeerId, setReportPeerId] = useState<string | null>(null);
  const [status, setStatus] = useState<'idle' | 'searching' | 'connected' | 'disconnected'>('idle');
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [peerTyping, setPeerTyping] = useState(false);
  const peerTypingTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [, setShowReconnectMessage] = useState(false);
  const chatEpochRef = useRef(0);
  const showReconnectMessageRef = useRef(false);
  const [cameraError, setCameraError] = useState<string | null>(null);
  const [isVideoConnecting, setIsVideoConnecting] = useState(false);
  const localVideoRef = useRef<HTMLVideoElement>(null);
  const remoteVideoRef = useRef<HTMLVideoElement>(null);
  const localStreamRef = useRef<MediaStream | null>(null);
  const peerConnectionRef = useRef<RTCPeerConnection | null>(null);
  const peerIdRef = useRef<string | null>(null);
  const sessionIdRef = useRef<string | null>(null);
  const sessionTokenRef = useRef<string | null>(null);
  const webrtcWsRef = useRef<WebSocket | null>(null);
  const pendingIceCandidatesRef = useRef<any[]>([]);
  const webrtcWsQueueRef = useRef<any[]>([]);
  const mockVideoTimerRef = useRef<number | null>(null);
  const iceRestartPendingRef = useRef(false);
  const webrtcSocketOpenRef = useRef(false);
  const signalingReadySentRef = useRef(false);
  const negotiationStartedRef = useRef(false);
  const pendingWebrtcStartRef = useRef<string | null>(null);
  const goWsConnectTimeoutRef = useRef<number | null>(null);
  const goWsReconnectTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const suppressedGoWsEventsRef = useRef<WeakSet<WebSocket>>(new WeakSet());
  const mountedRef = useRef(true);
  const transceiversRef = useRef<{ audio: RTCRtpTransceiver | null; video: RTCRtpTransceiver | null }>({
    audio: null,
    video: null,
  });
  const defaultStunServers = import.meta.env.VITE_STUN_SERVERS
    ? import.meta.env.VITE_STUN_SERVERS.split(',').map((url: string) => ({ urls: url.trim() }))
    : [
      { urls: 'stun:stun.cloudflare.com:3478' },
      { urls: 'stun:stun.l.google.com:19302' }
    ];

  const turnServersRef = useRef<RTCIceServer[]>(defaultStunServers);
  const turnFetchedRef = useRef(false);
  const turnFetchPromiseRef = useRef<Promise<RTCIceServer[] | null> | null>(null);

  const apiRetryCountRef = useRef(0);
  const apiSuccessTimeoutRef = useRef<number | null>(null);
  const serverRejectionCountRef = useRef(0);
  const goWsReconnectExhaustedRef = useRef(false);

  const reportApiError = (reason: string) => {
    if (apiSuccessTimeoutRef.current !== null) {
      window.clearTimeout(apiSuccessTimeoutRef.current);
      apiSuccessTimeoutRef.current = null;
    }

    apiRetryCountRef.current += 1;
    console.warn(`[NetworkHealth] API Error: ${reason}. (Strike ${apiRetryCountRef.current}/5)`);
    if (apiRetryCountRef.current === 1) {
      setApiStatus('degraded');
    } else if (apiRetryCountRef.current >= 5) {
      console.error('[NetworkHealth] API offline state triggered due to continuous failures.');
      setApiStatus('offline');
    }
  };

  const reportApiSuccess = (isStillHealthy?: () => boolean) => {
    if (apiSuccessTimeoutRef.current !== null) {
      window.clearTimeout(apiSuccessTimeoutRef.current);
    }

    apiSuccessTimeoutRef.current = window.setTimeout(() => {
      if (isStillHealthy && !isStillHealthy()) {
        return;
      }

      if (apiRetryCountRef.current > 0 || serverRejectionCountRef.current > 0) {
        console.info('[NetworkHealth] API connection recovered successfully.');
        apiRetryCountRef.current = 0;
        serverRejectionCountRef.current = 0;
        setApiStatus('ok');
      }
    }, 3000);
  };

  const logWebRTC = (label: string, details?: unknown) => {
    if (!webrtcDebugEnabled) return;
    if (import.meta.env.VITE_WEBSOCKET_DEBUG === 'true') {
      console.log(`[WebRTC] ${label}`, details ?? '');
    }
  };

  const setReconnectMessageVisible = (visible: boolean) => {
    showReconnectMessageRef.current = visible;
    setShowReconnectMessage(visible);
  };

  const beginNewChatEpoch = () => {
    chatEpochRef.current += 1;
  };

  const pushSystemMessage = (text: string, systemReason?: string) => {
    setMessages(msgs => [...msgs, {
      id: crypto.randomUUID(),
      text,
      sender: 'system',
      timestamp: Date.now(),
      systemReason
    }]);
  };

  const updateDeliveryStatus = (
    messageId: string,
    deliveryStatus: ChatMessage['deliveryStatus'],
    replacementText?: string
  ) => {
    setMessages(prev => prev.map((message) => (
      message.id === messageId
        ? {
            ...message,
            text: replacementText ?? message.text,
            deliveryStatus
          }
        : message
    )));
  };

  const showConnectionStatusMessage = (text: string) => {
    if (showReconnectMessageRef.current) return;

    pushSystemMessage(text);
    setReconnectMessageVisible(true);
  };

  const showVideoSignalingUnavailable = (text: string) => {
    pendingWebrtcStartRef.current = null;
    setIsVideoConnecting(false);
    setMessages(msgs => {
      if (msgs.some(msg => msg.sender === 'system' && msg.text === text)) {
        return msgs;
      }

      return [...msgs, {
        id: crypto.randomUUID(),
        text,
        sender: 'system',
        timestamp: Date.now()
      }];
    });
  };

  const canOpenGoWebSocket = () => !goWsReconnectExhaustedRef.current;

  const clearGoWsConnectTimeout = () => {
    if (goWsConnectTimeoutRef.current !== null) {
      window.clearTimeout(goWsConnectTimeoutRef.current);
      goWsConnectTimeoutRef.current = null;
    }
  };

  const closeGoWebSocket = (reason: string) => {
    const ws = webrtcWsRef.current;
    if (!ws) return;

    suppressedGoWsEventsRef.current.add(ws);
    logWebRTC('Closing Go WebRTC WS', { reason, readyState: ws.readyState });
    webrtcWsRef.current = null;
    webrtcSocketOpenRef.current = false;
    signalingReadySentRef.current = false;
    clearGoWsConnectTimeout();

    try {
      ws.close();
    } catch (error) {
      console.warn('Failed closing Go WebRTC WS cleanly:', error);
    }
  };

  const resetWebRTCTransport = (reason: string, options?: { keepLocalMedia?: boolean }) => {
    const keepLocalMedia = options?.keepLocalMedia === true;

    iceRestartPendingRef.current = false;
    pendingWebrtcStartRef.current = null;
    signalingReadySentRef.current = false;
    negotiationStartedRef.current = false;
    turnFetchedRef.current = false;
    turnFetchPromiseRef.current = null;
    closeGoWebSocket(reason);

    if (peerConnectionRef.current) {
      peerConnectionRef.current.close();
      peerConnectionRef.current = null;
    }

    transceiversRef.current = { audio: null, video: null };
    pendingIceCandidatesRef.current = [];
    webrtcWsQueueRef.current = [];
    setIsVideoConnecting(false);

    if (remoteVideoRef.current) {
      remoteVideoRef.current.srcObject = null;
    }

    if (!keepLocalMedia && localStreamRef.current) {
      localStreamRef.current.getTracks().forEach(track => track.stop());
      localStreamRef.current = null;
    }

    if (!keepLocalMedia && localVideoRef.current) {
      localVideoRef.current.srcObject = null;
    }
  };

  const logSelectedCandidatePair = async (pc: RTCPeerConnection) => {
    if (!webrtcDebugEnabled) return;
    try {
      const stats = await pc.getStats();
      let selectedPair: any = null;
      let localCandidate: any = null;
      let remoteCandidate: any = null;

      stats.forEach(report => {
        if (report.type === 'transport' && report.selectedCandidatePairId) {
          selectedPair = stats.get(report.selectedCandidatePairId);
        }
      });

      if (!selectedPair) {
        stats.forEach(report => {
          if (report.type === 'candidate-pair' && report.state === 'succeeded' && report.nominated) {
            selectedPair = report;
          }
        });
      }

      if (selectedPair) {
        localCandidate = stats.get(selectedPair.localCandidateId);
        remoteCandidate = stats.get(selectedPair.remoteCandidateId);
      }

      logWebRTC('Selected candidate pair', {
        pair: selectedPair
          ? {
            state: selectedPair.state,
            nominated: selectedPair.nominated,
            currentRoundTripTime: selectedPair.currentRoundTripTime,
            availableOutgoingBitrate: selectedPair.availableOutgoingBitrate
          }
          : null,
        local: localCandidate
          ? {
            candidateType: localCandidate.candidateType,
            protocol: localCandidate.protocol,
            address: localCandidate.address,
            port: localCandidate.port,
            url: localCandidate.url
          }
          : null,
        remote: remoteCandidate
          ? {
            candidateType: remoteCandidate.candidateType,
            protocol: remoteCandidate.protocol,
            address: remoteCandidate.address,
            port: remoteCandidate.port,
            url: remoteCandidate.url
          }
          : null
      });
    } catch (error) {
      console.error('Failed to inspect selected ICE candidate pair:', error);
    }
  };

  useEffect(() => {
    sessionIdRef.current = sessionId;
  }, [sessionId]);

  useEffect(() => {
    peerIdRef.current = peerId;
  }, [peerId]);

  useEffect(() => {
    sessionTokenRef.current = sessionToken;
  }, [sessionToken]);

  useEffect(() => {
    let mounted = true;
    mountedRef.current = true;

    const setup = async () => {
      try {
        await wsClient.connect();
        if (mounted) {
          setConnected(true);
          setReconnectMessageVisible(false);
        }

        wsClient.onMessage((message: Message) => {
          if (mounted) handleMessage(message);
        });

        wsClient.onReconnecting(() => {
          if (mounted) setChannelStatus('phoenix:video', 'degraded');
        });

        wsClient.onMaxRetries(() => {
          if (mounted) setChannelStatus('phoenix:video', 'offline');
        });

        wsClient.onOpen(() => {
          if (mounted) {
            setChannelStatus('phoenix:video', 'ok');
            setConnected(true);
            setReconnectMessageVisible(false);
            setStatus(prev => prev === 'disconnected' ? 'idle' : prev);
          }
        });

        wsClient.onClose(() => {
          if (mounted) {
            setConnected(false);
            setStatus(prev => {
              if (prev === 'connected' || prev === 'searching') {
                showConnectionStatusMessage('Connection to server lost. Reconnecting...');
                return 'disconnected';
              }
              return prev;
            });
            setPeerId(null);
            setSessionId(null);
            setSessionToken(null);
            sessionTokenRef.current = null;
            setReportPeerId(null);
            setPeerTyping(false);
            peerIdRef.current = null;
            sessionIdRef.current = null;
            resetWebRTCTransport('phoenix_disconnect', { keepLocalMedia: true });
          }
        });
      } catch (error) {
        console.error('Failed to connect:', error);
        if (mounted) {
          setConnected(false);
          setStatus('disconnected');
          showConnectionStatusMessage('Failed to connect to server. Please refresh the page.');
        }
      }
    };

    setup();

    return () => {
      mounted = false;
      mountedRef.current = false;
      removeChannel('phoenix:video');
      setApiStatus('ok');
      cleanup();
      wsClient.disconnect();
    };
  }, [wsClient]);

  const getLocalStream = async () => {
    if (localStreamRef.current) return localStreamRef.current;

    try {
      setCameraError(null);

      const stream = await navigator.mediaDevices.getUserMedia({
        video: true,
        audio: true
      });
      localStreamRef.current = stream;
      if (localVideoRef.current) {
        localVideoRef.current.srcObject = stream;
      }
      return stream;
    } catch (error: any) {
      if (error.name === 'NotAllowedError' || error.name === 'SecurityError') {
        setCameraError("Camera access is blocked. Please click the lock icon in your URL bar to unblock it, if in mobile, check app settings, then refresh.");
        console.warn("User or Browser explicitly denied camera permissions.");
        return null;
      }

      console.warn('Physical camera not found, falling back to mock stream:', error);

      try {
        const canvas = document.createElement('canvas');
        canvas.width = 640;
        canvas.height = 480;
        const ctx = canvas.getContext('2d');

        if (ctx) {
          let t = 0;
          mockVideoTimerRef.current = window.setInterval(() => {
            ctx.fillStyle = '#111';
            ctx.fillRect(0, 0, canvas.width, canvas.height);

            ctx.fillStyle = `hsl(${t % 360}, 80%, 50%)`;
            t += 2;
            ctx.beginPath();
            ctx.arc(canvas.width / 2 + Math.sin(t / 20) * 100, canvas.height / 2 + Math.cos(t / 15) * 50 + 20, 30, 0, Math.PI * 2);
            ctx.fill();

            ctx.font = '30px sans-serif';
            ctx.fillStyle = 'white';
            ctx.textAlign = 'center';
            ctx.fillText('Virtual Webcam', canvas.width / 2, canvas.height / 2 - 30);
          }, 1000 / 30);
        }
        const mockStream = canvas.captureStream(30);

        localStreamRef.current = mockStream;
        if (localVideoRef.current) {
          localVideoRef.current.srcObject = mockStream;
        }
        return mockStream;
      } catch (fallbackError) {
        console.error('Failed to create mock stream:', fallbackError);
        throw error;
      }
    }
  };

  const sendWebRTC = async (eventType: 'offer' | 'answer' | 'ice', payload: any, currentPeerId: string | null = peerId) => {
    const activeSessionId = sessionIdRef.current;
    if (!activeSessionId || !currentPeerId || !webrtcWsRef.current) {
      console.warn("Cannot send WebRTC signal: WS disconnected or session absent");
      return;
    }

    const messagePayload = {
      type: eventType,
      from_session_id: activeSessionId,
      to_session_id: currentPeerId,
      data: payload
    };

    try {
      if (webrtcWsRef.current.readyState === WebSocket.OPEN) {
        webrtcWsRef.current.send(JSON.stringify(messagePayload));
      } else if (webrtcWsRef.current.readyState === WebSocket.CONNECTING) {
        webrtcWsQueueRef.current.push(messagePayload);
      } else {
        console.warn("Cannot send WebRTC signal: WS in CLOSING/CLOSED state");
      }
    } catch (e) {
      console.error('Failed to send WebRTC signal to Go WS:', e);
    }
  };

  const maybeSendWebRTCReady = (targetPeerId: string | null = peerIdRef.current) => {
    if (!targetPeerId || !sessionIdRef.current || !webrtcWsRef.current || !webrtcSocketOpenRef.current || signalingReadySentRef.current) {
      return;
    }

    signalingReadySentRef.current = true;

    try {
      wsClient.send('webrtc_ready', {});
    } catch (err) {
      signalingReadySentRef.current = false;
      console.error('Failed to send WebRTC ready signal:', err);
    }
  };

  const startNegotiationIfNeeded = async (targetPeerId: string) => {
    const activeSessionId = sessionIdRef.current;
    if (!activeSessionId || negotiationStartedRef.current) return;

    const isCaller = isCallerSession(activeSessionId, targetPeerId);
    if (!isCaller) {
      logWebRTC('Callee waiting for offer', {
        targetPeerId,
        sessionId: activeSessionId
      });
      return;
    }

    negotiationStartedRef.current = true;

    let pc = peerConnectionRef.current;
    if (!pc) {
      pc = createPeerConnection(targetPeerId);

      const transceiverInit: RTCRtpTransceiverInit = {
        direction: 'sendrecv'
      };
      transceiversRef.current.audio = pc.addTransceiver('audio', transceiverInit);
      transceiversRef.current.video = pc.addTransceiver('video', transceiverInit);

      if (localStreamRef.current) {
        localStreamRef.current.getTracks().forEach(track => {
          const transceiver = transceiversRef.current[track.kind as 'audio' | 'video'];
          if (transceiver) {
            transceiver.sender.replaceTrack(track);
          }
        });
      }
    }

    try {
      await attachLocalTracks();
      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      logWebRTC('Created initial offer', {
        targetPeerId,
        mLines: getSdpMediaSections(offer.sdp)
      });
      sendWebRTC('offer', { sdp: offer }, targetPeerId);
    } catch (error) {
      negotiationStartedRef.current = false;
      console.error('Failed to create offer:', error);
    }
  };

  const attachLocalTracks = async () => {
    const audioTrack = localStreamRef.current?.getAudioTracks()[0] ?? null;
    const videoTrack = localStreamRef.current?.getVideoTracks()[0] ?? null;
    const audioSender = transceiversRef.current.audio?.sender ?? null;
    const videoSender = transceiversRef.current.video?.sender ?? null;

    // Replace tracks on the transceivers
    if (audioSender) {
      await audioSender.replaceTrack(audioTrack);
    }
    if (videoSender) {
      await videoSender.replaceTrack(videoTrack);
    }
    logWebRTC('Attached local tracks', {
      audio: audioTrack ? { id: audioTrack.id, label: audioTrack.label, readyState: audioTrack.readyState, enabled: audioTrack.enabled } : null,
      video: videoTrack ? { id: videoTrack.id, label: videoTrack.label, readyState: videoTrack.readyState, enabled: videoTrack.enabled } : null
    });
  };

  const createPeerConnection = (targetPeerId: string) => {
    const config: RTCConfiguration = {
      iceServers: turnServersRef.current,
      iceTransportPolicy: forceRelay ? 'relay' : 'all'
    };
    logWebRTC('Creating peer connection', {
      targetPeerId,
      turnEnabled,
      forceRelay,
      iceServers: config.iceServers?.map(server => ({
        urls: Array.isArray(server.urls) ? server.urls : [server.urls]
      }))
    });

    const pc = new RTCPeerConnection(config);
    peerConnectionRef.current = pc;

    pc.onicecandidate = (event) => {
      if (event.candidate) {
        logWebRTC('Sending ICE candidate', {
          targetPeerId,
          sdpMid: event.candidate.sdpMid,
          ...parseIceCandidate(event.candidate.candidate)
        });
        sendWebRTC('ice', { candidate: event.candidate }, targetPeerId);
      }
    };

    pc.oniceconnectionstatechange = () => {
      if (import.meta.env.VITE_WEBSOCKET_DEBUG === 'true') {
        console.log('WebRTC ICE State:', pc.iceConnectionState);
      }
      logWebRTC('ICE state changed', {
        iceConnectionState: pc.iceConnectionState,
        connectionState: pc.connectionState,
        signalingState: pc.signalingState
      });
      if (pc.iceConnectionState === 'connected' || pc.iceConnectionState === 'completed') {
        void logSelectedCandidatePair(pc);
      }
      if (turnEnabled && (pc.iceConnectionState === 'failed' || pc.iceConnectionState === 'disconnected')) {
        console.warn(`ICE ${pc.iceConnectionState} — attempting TURN upgrade...`);
        fetchAndApplyTURN(pc, targetPeerId);
      }
    };

    pc.onconnectionstatechange = () => {
      logWebRTC('Peer connection state changed', {
        connectionState: pc.connectionState,
        iceConnectionState: pc.iceConnectionState,
        signalingState: pc.signalingState
      });
      if (pc.connectionState === 'connected') {
        setIsVideoConnecting(false);
      } else if (pc.connectionState === 'connecting') {
        setIsVideoConnecting(true);
      }
    };

    pc.onsignalingstatechange = () => {
      logWebRTC('Signaling state changed', {
        signalingState: pc.signalingState,
        localDescriptionType: pc.localDescription?.type,
        remoteDescriptionType: pc.remoteDescription?.type
      });
    };

    // Required for ICE restart: when restartIce() is called after a TURN upgrade,
    pc.onnegotiationneeded = async () => {
      if (!iceRestartPendingRef.current) return;

      const activeSessionId = sessionIdRef.current;
      const peerIdSnapshot = targetPeerId;
      if (!activeSessionId || !peerIdSnapshot) return;
      const isCaller = isCallerSession(activeSessionId, peerIdSnapshot);
      if (!isCaller) return;
      if (pc.signalingState !== 'stable') return;

      try {
        const offer = await pc.createOffer({ iceRestart: true });
        await pc.setLocalDescription(offer);
        logWebRTC('Created ICE restart offer', {
          targetPeerId: peerIdSnapshot,
          mLines: getSdpMediaSections(offer.sdp)
        });
        iceRestartPendingRef.current = false;
        sendWebRTC('offer', { sdp: offer }, peerIdSnapshot);
      } catch (err) {
        iceRestartPendingRef.current = false;
        console.error('ICE restart offer failed:', err);
      }
    };

    pc.ontrack = (event) => {
      if (import.meta.env.VITE_WEBSOCKET_DEBUG === 'true') {
        console.log('Received track from peer:', event.track.kind);
      }
      logWebRTC('Received remote track', {
        kind: event.track.kind,
        id: event.track.id,
        streams: event.streams.map(stream => ({
          id: stream.id,
          tracks: stream.getTracks().map(track => ({ kind: track.kind, id: track.id, readyState: track.readyState }))
        }))
      });
      if (remoteVideoRef.current) {
        const videoElement = remoteVideoRef.current;
        let streamToAssign: MediaStream;

        if (event.streams && event.streams[0]) {
          streamToAssign = event.streams[0];
        } else {
          streamToAssign = (videoElement.srcObject as MediaStream) || new MediaStream();
          streamToAssign.addTrack(event.track);
        }

        // Force the browser to recognize new sub-tracks in the same stream by
        // bouncing the srcObject reference. Without this, WebKit often ignores dynamically added tracks.
        if (videoElement.srcObject === streamToAssign) {
          videoElement.srcObject = null;
        }
        videoElement.srcObject = streamToAssign;

        void videoElement.play().catch(err => {
          console.warn('Remote video autoplay was blocked:', err);
        });
      }
      setIsVideoConnecting(false);
    };

    return pc;
  };

  const fetchTurnServers = async () => {
    if (!turnEnabled) {
      logWebRTC('TURN disabled by env flag');
      return null;
    }
    if (turnFetchPromiseRef.current) {
      return turnFetchPromiseRef.current;
    }
    turnFetchPromiseRef.current = (async () => {
      try {
        const goApiUrl = import.meta.env.VITE_API_URL || 'http://localhost:8082';
        const activeSessionId = sessionIdRef.current;
        const activeSessionToken = sessionTokenRef.current;
        if (!activeSessionId || !activeSessionToken) {
          logWebRTC('Skipping TURN fetch because session auth is missing');
          return null;
        }

        const res = await fetch(`${goApiUrl}/api/v1/webrtc/turn`, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
          },
          body: JSON.stringify({
            session_id: activeSessionId,
            session_token: activeSessionToken,
          }),
        });
        if (!res.ok) {
          reportApiError(`TURN fetch failed with HTTP ${res.status}`);
          return null;
        }
        reportApiSuccess(() => !goWsReconnectExhaustedRef.current || webrtcSocketOpenRef.current);
        const data = await res.json();

        if (data.iceServers && Array.isArray(data.iceServers)) {
          logWebRTC('Fetched TURN/STUN servers', {
            urls: data.iceServers.flatMap((server: RTCIceServer) => Array.isArray(server.urls) ? server.urls : [server.urls])
          });
          return data.iceServers as RTCIceServer[];
        }
        return null;
      } catch (err: unknown) {
        console.error('Failed to fetch TURN credentials:', err);
        if (err instanceof TypeError) {
          reportApiError('TURN fetch network TypeError');
        }
        return null;
      } finally {
        turnFetchPromiseRef.current = null;
      }
    })();

    return turnFetchPromiseRef.current;
  };

  const fetchAndApplyTURN = async (pc: RTCPeerConnection, _targetPeerId: string) => {
    if (turnFetchedRef.current) return; // Already upgraded — don't call CF API again
    turnFetchedRef.current = true;

    try {
      const iceServers = await fetchTurnServers();
      if (!iceServers) {
        turnFetchedRef.current = false;
        return;
      }

      turnServersRef.current = iceServers;
      if (!hasTurnServer(iceServers)) {
        console.warn('TURN endpoint returned no relay servers; staying on STUN-only ICE.');
        turnFetchedRef.current = false;
        return;
      }

      pc.setConfiguration({ iceServers });
      iceRestartPendingRef.current = true;
      pc.restartIce();
      if (import.meta.env.VITE_WEBSOCKET_DEBUG === 'true') {
        console.log('TURN upgrade applied, ICE restarting via relay...');
      }
    } catch (err) {
      console.error('Failed to apply TURN credentials for fallback:', err);
      iceRestartPendingRef.current = false;
      turnFetchedRef.current = false;
    }
  };

  const flushIceCandidates = async () => {
    const pc = peerConnectionRef.current;
    if (!pc || !pc.remoteDescription) return;
    while (pendingIceCandidatesRef.current.length > 0) {
      const candidateInit = pendingIceCandidatesRef.current.shift();
      if (candidateInit) {
        try {
          await pc.addIceCandidate(new RTCIceCandidate(candidateInit));
        } catch (err) {
          console.error('Failed to add queued ICE candidate:', err);
        }
      }
    }
  };

  const handleMessage = async (message: Message) => {
    switch (message.type) {
      case 'searching':
      case 'connected':
        if (message.session_id && message.session_id !== sessionIdRef.current) {
          goWsReconnectExhaustedRef.current = false;
          setSessionId(message.session_id);
          sessionIdRef.current = message.session_id;

          if (message.session_token) {
            setSessionToken(message.session_token);
            sessionTokenRef.current = message.session_token;
          }

          // Hydrate raw WebRTC socket to Go exclusively for signaling.
          closeGoWebSocket('session_changed');

          const goApiUrl = import.meta.env.VITE_API_URL || 'http://localhost:8082';
          const wsProtocol = goApiUrl.startsWith('https') ? 'wss:' : 'ws:';
          const host = goApiUrl.replace(/^https?:\/\//, '');

          if (!canOpenGoWebSocket()) {
            logWebRTC('Skipping Go WebRTC WS open because retries are exhausted', {
              sessionId: message.session_id
            });
            break;
          }

          const ws = new WebSocket(
            `${wsProtocol}//${host}/api/v1/webrtc/ws?session_id=${encodeURIComponent(message.session_id)}&session_token=${encodeURIComponent(message.session_token || '')}`
          );
          webrtcWsRef.current = ws;
          webrtcSocketOpenRef.current = false;

          goWsConnectTimeoutRef.current = window.setTimeout(() => {
            if (webrtcWsRef.current === ws && !webrtcSocketOpenRef.current) {
              console.error('Go WebRTC WS connection timed out after 8s');
              showVideoSignalingUnavailable('Video signaling connection timed out. Text chat is still active. Refresh the page before searching again.');
            }
          }, 8000);

          ws.onopen = () => {
            if (webrtcWsRef.current !== ws) {
              suppressedGoWsEventsRef.current.add(ws);
              try {
                ws.close();
              } catch {
                // Ignore redundant close on a stale socket.
              }
              return;
            }

            logWebRTC('Go WebRTC WS opened', { sessionId: message.session_id });
            webrtcSocketOpenRef.current = true;
            clearGoWsConnectTimeout();
            reportApiSuccess(() => webrtcWsRef.current === ws && ws.readyState === WebSocket.OPEN);
            // Flush any pending WS signaling packets (offer/answer/ice) upon tcp connect
            while (webrtcWsQueueRef.current.length > 0) {
              const queuedMessage = webrtcWsQueueRef.current.shift();
              ws.send(JSON.stringify(queuedMessage));
            }
            maybeSendWebRTCReady();

            const pendingPeerId = pendingWebrtcStartRef.current;
            if (pendingPeerId) {
              pendingWebrtcStartRef.current = null;
              logWebRTC('Processing deferred webrtc_start after Go WS opened', { peerId: pendingPeerId });
              startNegotiationIfNeeded(pendingPeerId);
            }
          };
          ws.onclose = (event) => {
            const suppressed = suppressedGoWsEventsRef.current.has(ws);
            const activeSocket = webrtcWsRef.current === ws;

            if (activeSocket) {
              webrtcWsRef.current = null;
              webrtcSocketOpenRef.current = false;
              signalingReadySentRef.current = false;
              clearGoWsConnectTimeout();

              // Server-initiated rejections (e.g. 1008 "already connected") are not
              // network failures — the server explicitly refused a duplicate session.
              // Retrying against these just creates a reconnect storm. Only truly
              // unexpected closes (network drop, service crash) warrant error escalation.
              const isServerRejection = event.code === 1008;

              if (!isServerRejection) {
                reportApiError('Go WebRTC WebSocket closed unexpectedly');

                if (apiRetryCountRef.current < 5) {
                  // Capture values in closure to avoid reading stale refs
                  const reconnectSessionId = message.session_id;
                  const reconnectToken = sessionTokenRef.current || undefined;
                  const reconnectDelay = 2000 * apiRetryCountRef.current;

                  // Cancel any previous reconnect timer before scheduling a new one
                  if (goWsReconnectTimeoutRef.current !== null) {
                    clearTimeout(goWsReconnectTimeoutRef.current);
                  }

                  goWsReconnectTimeoutRef.current = setTimeout(() => {
                    goWsReconnectTimeoutRef.current = null;
                    if (!mountedRef.current) return;
                    if (sessionIdRef.current !== reconnectSessionId) return;
                    if (!canOpenGoWebSocket()) return;

                    console.warn(`[NetworkHealth] Attempting Go WebRTC WS reconnect (Retry ${apiRetryCountRef.current})...`);
                    closeGoWebSocket('reconnect_attempt');
                    // Temporarily clear sessionIdRef so handleMessage 'connected' guard passes
                    sessionIdRef.current = null;
                    handleMessage({
                      type: 'connected',
                      session_id: reconnectSessionId,
                      session_token: reconnectToken
                    });
                  }, reconnectDelay);
                }
              } else {
                // Server thinks we're already connected (stale Redis state).
                // Retry with longer backoff to give the server time to clean up.
                serverRejectionCountRef.current += 1;

                if (serverRejectionCountRef.current <= 3) {
                  const reconnectSessionId = message.session_id;
                  const reconnectToken = sessionTokenRef.current || undefined;
                  const rejectionDelay = 3000 * serverRejectionCountRef.current; // 3s, 6s, 9s

                  logWebRTC('Go WS rejected (already connected), scheduling retry', {
                    attempt: serverRejectionCountRef.current,
                    delay: rejectionDelay,
                    sessionId: message.session_id
                  });

                  if (goWsReconnectTimeoutRef.current !== null) {
                    clearTimeout(goWsReconnectTimeoutRef.current);
                  }

                  goWsReconnectTimeoutRef.current = setTimeout(() => {
                    goWsReconnectTimeoutRef.current = null;
                    if (!mountedRef.current) return;
                    if (sessionIdRef.current !== reconnectSessionId) return;
                    if (!canOpenGoWebSocket()) return;

                    closeGoWebSocket('1008_retry');
                    sessionIdRef.current = null;
                    handleMessage({
                      type: 'connected',
                      session_id: reconnectSessionId,
                      session_token: reconnectToken
                    });
                  }, rejectionDelay);
                } else {
                  goWsReconnectExhaustedRef.current = true;
                  if (goWsReconnectTimeoutRef.current !== null) {
                    clearTimeout(goWsReconnectTimeoutRef.current);
                    goWsReconnectTimeoutRef.current = null;
                  }
                  logWebRTC('Go WS rejected by server too many times, giving up', {
                    sessionId: message.session_id,
                    attempts: serverRejectionCountRef.current
                  });
                  setApiStatus('offline');
                  showVideoSignalingUnavailable('Video signaling is unavailable right now. Text chat is still active. Try refreshing the page.');
                }
              }
            }

            if (suppressed || !activeSocket) {
              logWebRTC('Ignoring stale Go WebRTC WS close', {
                sessionId: message.session_id,
                code: event.code,
                reason: event.reason || undefined
              });
              return;
            }

            console.warn('Go WebRTC WS closed', {
              sessionId: message.session_id,
              code: event.code,
              reason: event.reason || undefined,
              wasClean: event.wasClean,
              readyState: ws.readyState
            });
          };
          ws.onerror = (event) => {
            const suppressed = suppressedGoWsEventsRef.current.has(ws);
            const activeSocket = webrtcWsRef.current === ws;

            if (suppressed || !activeSocket) {
              logWebRTC('Ignoring stale Go WebRTC WS error', {
                sessionId: message.session_id
              });
              return;
            }

            console.warn('Go WebRTC WS error', {
              sessionId: message.session_id,
              readyState: ws.readyState,
              eventType: event.type
            });
          };
          ws.onmessage = (event) => {
            try {
              const signal = JSON.parse(event.data);
              // Forward the native Go signal straight back into our unified message handler seamlessly
              handleMessage(signal as Message);
            } catch (err) {
              console.error("Failed parsing Go WS signal:", err);
            }
          };
          // Proactively fetch TURN credentials as soon as we have a session to avoid delay during match
          if (turnEnabled && !turnFetchedRef.current) {
            void fetchTurnServers().then(iceServers => {
              if (iceServers) {
                turnServersRef.current = iceServers;
                if (hasTurnServer(iceServers)) {
                  turnFetchedRef.current = true;
                }
              }
            });
          }
        }
        break;

      case 'match':
        beginNewChatEpoch();
        const peerIdMatch = message.peer_id;
        const common = (message as any).common_interests || [];
        if (import.meta.env.VITE_WEBSOCKET_DEBUG === 'true') {
          console.log('Matched with peer:', peerIdMatch);
        }
        logWebRTC('Phoenix match received', {
          peerId: peerIdMatch,
          sessionId: sessionIdRef.current
        });

        // Close any stale PeerConnection from a previous match
        if (peerConnectionRef.current) {
          logWebRTC('Closing stale PeerConnection from previous match');
          peerConnectionRef.current.close();
          peerConnectionRef.current = null;
          transceiversRef.current = { audio: null, video: null };
        }
        if (remoteVideoRef.current) {
          remoteVideoRef.current.srcObject = null;
        }
        pendingIceCandidatesRef.current = [];
        pendingWebrtcStartRef.current = null;

        setPeerId(peerIdMatch || '');
        setReportPeerId(peerIdMatch || null);
        setStatus('connected');
        setIsVideoConnecting(true);
        setShowReconnectMessage(false);
        setPeerTyping(false);
        signalingReadySentRef.current = false;
        negotiationStartedRef.current = false;
        peerIdRef.current = peerIdMatch || null;

        if (peerTypingTimeoutRef.current) {
          clearTimeout(peerTypingTimeoutRef.current);
          peerTypingTimeoutRef.current = null;
        }

        if (common.length > 0) {
          setMessages([{
            id: crypto.randomUUID(),
            text: `You both like: ${common.join(', ')}`,
            sender: 'system',
            timestamp: Date.now()
          }]);
        } else {
          setMessages([{
            id: crypto.randomUUID(),
            text: `You are talking to a random stranger.`,
            sender: 'system',
            timestamp: Date.now()
          }]);
        }

        // If the Go WS is already open, signal ready immediately.
        // If the Go WS is dead (e.g. server rejected with 1008, or network drop),
        // re-establish it so video signaling can work for this match.
        if (webrtcSocketOpenRef.current) {
          maybeSendWebRTCReady(peerIdMatch || null);
        } else if (!webrtcWsRef.current && sessionIdRef.current && canOpenGoWebSocket()) {
          logWebRTC('Go WS is dead on match, re-establishing for signaling', {
            sessionId: sessionIdRef.current
          });
          const currentSessionId = sessionIdRef.current;
          const currentToken = sessionTokenRef.current || undefined;
          // Temporarily clear so the 'connected' handler guard passes
          sessionIdRef.current = null;
          handleMessage({
            type: 'connected',
            session_id: currentSessionId,
            session_token: currentToken
          });
        } else if (!canOpenGoWebSocket()) {
          showVideoSignalingUnavailable('Video signaling is unavailable right now. Text chat is still active. Try refreshing the page.');
        }
        break;

      case 'webrtc_ready':
        if (turnEnabled && !turnFetchedRef.current) {
          const iceServers = await fetchTurnServers();
          if (iceServers) {
            turnServersRef.current = iceServers;
            if (hasTurnServer(iceServers)) {
              turnFetchedRef.current = true;
            }
            logWebRTC('Prepared TURN/STUN servers during webrtc_ready', {
              hasRelay: hasTurnServer(iceServers),
              urls: iceServers.flatMap(server => Array.isArray(server.urls) ? server.urls : [server.urls])
            });
            // PROACTIVE FIX: If the PeerConnection was already created (e.g., match raced with webrtc_ready),
            // apply the new config immediately to avoid waiting for ICE failure.
            if (peerConnectionRef.current) {
              logWebRTC('Applying newly fetched TURN credentials to existing PeerConnection');
              peerConnectionRef.current.setConfiguration({ ...peerConnectionRef.current.getConfiguration(), iceServers });
            }
          }
        }
        break;

      case 'webrtc_start':
        if (message.peer_id) {
          logWebRTC('Phoenix webrtc_start received', {
            peerId: message.peer_id,
            goWsOpen: webrtcSocketOpenRef.current
          });
          // Gate negotiation on Go WS being open — this is the primary fix for one-way video.
          // If the Go WS isn't open yet, queue the peer_id and process it when onopen fires.
          if (webrtcSocketOpenRef.current) {
            await startNegotiationIfNeeded(message.peer_id);
          } else {
            logWebRTC('Go WS not open yet, deferring webrtc_start', { peerId: message.peer_id });
            pendingWebrtcStartRef.current = message.peer_id;
          }
        }
        break;

      case 'message':
        if (message.data?.content) {
          setMessages(prev => [...prev, {
            id: crypto.randomUUID(),
            text: message.data.content,
            sender: 'peer',
            timestamp: Date.now()
          }]);
        }
        break;

      case 'system':
        if (message.data?.message) {
          pushSystemMessage(message.data.message, message.data?.reason_code);
        }
        break;

      case 'typing':
        if (message.data?.typing !== undefined) {
          setPeerTyping(message.data.typing);
          // Safety net: auto-clear if typing: false is never received (network loss, rate-limit)
          if (peerTypingTimeoutRef.current) {
            clearTimeout(peerTypingTimeoutRef.current);
            peerTypingTimeoutRef.current = null;
          }
          if (message.data.typing) {
            peerTypingTimeoutRef.current = setTimeout(() => {
              setPeerTyping(false);
              peerTypingTimeoutRef.current = null;
            }, 4000);
          }
        }
        break;

      case 'offer':
        if (message.peer_id) {
          let pc = peerConnectionRef.current;
          if (!pc) {
            pc = createPeerConnection(message.peer_id);
          }
          try {
            logWebRTC('Applying remote offer', {
              peerId: message.peer_id,
              mLines: getSdpMediaSections(message.data?.sdp?.sdp)
            });
            await pc.setRemoteDescription(new RTCSessionDescription(message.data.sdp));
            await flushIceCandidates();

            // Extract the transceivers WebRTC automatically created from the remote SDP's m-lines.
            const transceivers = pc.getTransceivers();
            transceiversRef.current.audio = transceivers.find(t => t.receiver.track.kind === 'audio') ?? null;
            transceiversRef.current.video = transceivers.find(t => t.receiver.track.kind === 'video') ?? null;

            // CRITICAL: Implicit transceivers from setRemoteDescription default to 'recvonly'.
            // replaceTrack() does NOT change direction (unlike addTrack which auto-upgrades).
            // Without this, the answer SDP says 'recvonly' and the Caller never fires ontrack.
            if (transceiversRef.current.audio) transceiversRef.current.audio.direction = 'sendrecv';
            if (transceiversRef.current.video) transceiversRef.current.video.direction = 'sendrecv';

            await attachLocalTracks();

            const answer = await pc.createAnswer();
            await pc.setLocalDescription(answer);
            logWebRTC('Created local answer', {
              peerId: message.peer_id,
              mLines: getSdpMediaSections(answer.sdp)
            });
            sendWebRTC('answer', { sdp: answer }, message.peer_id);
          } catch (error) {
            console.error('Failed to handle offer:', error);
          }
        }
        break;

      case 'answer':
        if (peerConnectionRef.current) {
          try {
            logWebRTC('Applying remote answer', {
              mLines: getSdpMediaSections(message.data?.sdp?.sdp)
            });
            await peerConnectionRef.current.setRemoteDescription(new RTCSessionDescription(message.data.sdp));
            await flushIceCandidates();
          } catch (error) {
            console.error('Failed to handle answer:', error);
          }
        }
        break;

      case 'ice':
        if (peerConnectionRef.current && message.data?.candidate) {
          try {
            logWebRTC('Received ICE candidate', {
              sdpMid: message.data.candidate.sdpMid,
              ...parseIceCandidate(message.data.candidate.candidate)
            });
            if (peerConnectionRef.current.remoteDescription) {
              await peerConnectionRef.current.addIceCandidate(new RTCIceCandidate(message.data.candidate));
            } else {
              pendingIceCandidatesRef.current.push(message.data.candidate);
            }
          } catch (error) {
            console.error('Failed to add ICE candidate:', error);
          }
        }
        break;

      case 'disconnect':
      case 'disconnected':
        if (import.meta.env.VITE_WEBSOCKET_DEBUG === 'true') {
          console.log('Peer disconnected');
        }
        setStatus('disconnected');
        setIsVideoConnecting(false);
        setPeerId(null);
        setPeerTyping(false);
        beginNewChatEpoch();
        pushSystemMessage('Stranger has disconnected.');
        if (peerConnectionRef.current) {
          peerConnectionRef.current.close();
          peerConnectionRef.current = null;
        }
        if (remoteVideoRef.current) {
          remoteVideoRef.current.srcObject = null;
        }
        break;

      case 'banned':
        const videoBanReason = message.data?.reason || (message as any).reason || 'Banned by an administrator';
        console.error('You have been banned:', videoBanReason);
        setStatus('disconnected');
        setIsVideoConnecting(false);
        setPeerTyping(false);
        beginNewChatEpoch();
        pushSystemMessage(`You have been banned: ${videoBanReason}`);
        break;

      case 'error': {
        const errorMessage = message.data?.message || message.data?.error || message.data?.reason || JSON.stringify(message.data) || 'An error occurred';
        console.error('Server error:', message);
        // If CAPTCHA verification failed, revert to idle so user can retry
        if (typeof errorMessage === 'string' && errorMessage.includes('CAPTCHA')) {
          setStatus('idle');
        }
        // Silently drop benign race-condition errors that occur during skip/reconnect flow.
        // 'No partner' fires when webrtc_ready arrives before the new match is fully assigned.
        const benignErrors = ['No partner', 'No partner to skip'];
        if (benignErrors.some(e => errorMessage.includes(e))) break;
        pushSystemMessage(`Error: ${errorMessage}`);
        break;
      }

      case 'timeout':
        if (import.meta.env.VITE_WEBSOCKET_DEBUG === 'true') {
          console.log('Matchmaking timeout');
        }
        setStatus('disconnected');
        setIsVideoConnecting(false);
        setPeerId(null);
        setPeerTyping(false);
        beginNewChatEpoch();
        pushSystemMessage('Matchmaking timeout: No strangers are available right now.');
        break;

      case 'stopped':
        beginNewChatEpoch();
        setStatus('idle');
        setIsVideoConnecting(false);
        setPeerId(null);
        setSessionId(null);
        setSessionToken(null);
        sessionTokenRef.current = null;
        setReportPeerId(null);
        setMessages([]);
        setPeerTyping(false);
        break;
    }
  };

  const startSearch = async (interests: string = '', turnstileToken?: string) => {
    try {
      turnFetchedRef.current = false;
      iceRestartPendingRef.current = false;
      signalingReadySentRef.current = false;
      negotiationStartedRef.current = false;
      turnFetchPromiseRef.current = null;
      goWsReconnectExhaustedRef.current = false;
      setMessages([]);
      beginNewChatEpoch();
      setReconnectMessageVisible(false);
      setReportPeerId(null);
      setIsVideoConnecting(false);
      setStatus('searching');

      // Must prompt for camera synchronously with the raw user click to satisfy strict Safari/Firefox Android Transient Activation rules.
      await getLocalStream();

      logWebRTC('Starting search', {
        turnEnabled,
        forceRelay,
        prefetchedTurn: false,
        interests: interests.trim()
      });

      if (!wsClient.isConnected()) {
        await wsClient.connect();
        setConnected(true);
      }

      wsClient.send('start', {
        token: turnstileToken,
        preferences: {
          mode: 'video',
          interests: interests.trim()
        }
      });
    } catch (error) {
      console.error('Failed to start search:', error);
      setStatus('idle');
    }
  };

  const stopSearch = () => {
    try {
      setStatus('idle');
      setSessionId(null);
      setSessionToken(null);
      sessionTokenRef.current = null;
      setReportPeerId(null);
      wsClient.send('stop', {});
    } catch (error) {
      console.error('Failed to stop search:', error);
    }
  };

  const skip = () => {
    if (status === 'connected') {
      wsClient.send('skip', {});
      beginNewChatEpoch();
      setPeerId(null);
      setReportPeerId(null);
      setMessages([]);
      setPeerTyping(false);
      setIsVideoConnecting(false);
      setStatus('searching');

      // Clean up WebRTC state for the next connection
      if (peerConnectionRef.current) {
        peerConnectionRef.current.close();
        peerConnectionRef.current = null;
      }
      transceiversRef.current = { audio: null, video: null };
      if (remoteVideoRef.current) {
        remoteVideoRef.current.srcObject = null;
      }
      pendingIceCandidatesRef.current = [];
      pendingWebrtcStartRef.current = null;
      signalingReadySentRef.current = false;
      negotiationStartedRef.current = false;
      turnFetchedRef.current = false;
      iceRestartPendingRef.current = false;
      goWsReconnectExhaustedRef.current = false;
    }
  };

  const disconnect = () => {
    wsClient.send('disconnect', {});
    beginNewChatEpoch();
    setStatus('disconnected');
    setIsVideoConnecting(false);
    setPeerId(null);
    setPeerTyping(false);
    pushSystemMessage('You have disconnected.');
    cleanup();
  };

  const sendMessage = (text: string) => {
    try {
      if (text.trim() && status === 'connected') {
        const messageId = crypto.randomUUID();
        const chatEpoch = chatEpochRef.current;

        setMessages(prev => [...prev, {
          id: messageId,
          text,
          sender: 'me',
          timestamp: Date.now(),
          deliveryStatus: 'pending'
        }]);

        void wsClient.sendWithResponse('message', { content: text })
          .then((payload) => {
            if (chatEpochRef.current !== chatEpoch) {
              return;
            }

            if (payload?.type === 'system') {
              if (payload.data?.reason_code === BANNED_PHRASE_REASON || payload.data?.message === BLOCKED_PHRASE_NOTICE) {
                updateDeliveryStatus(messageId, 'blocked');
                pushSystemMessage(
                  payload.data?.message || BLOCKED_PHRASE_NOTICE,
                  payload.data?.reason_code || BANNED_PHRASE_REASON
                );
              }
              return;
            }

            if (payload?.type === 'error') {
              const errorMessage = payload.data?.message || payload.data?.error || payload.data?.reason || 'Message could not be sent. Please try again.';
              updateDeliveryStatus(messageId, 'failed');
              pushSystemMessage(`Message could not be sent: ${errorMessage}`);
              return;
            }

            updateDeliveryStatus(messageId, 'sent');
          })
          .catch((error) => {
            console.error('Failed to send message:', error);
            if (chatEpochRef.current !== chatEpoch) {
              return;
            }
            updateDeliveryStatus(messageId, 'failed');
            pushSystemMessage('Message could not be sent right now. Please try again.');
          });
      }
    } catch (error) {
      console.error('Failed to send message:', error);
    }
  };

  const sendTyping = (isTyping: boolean) => {
    try {
      if (status === 'connected') {
        wsClient.send('typing', { typing: isTyping });
      }
    } catch (error) {
      console.error('Failed to send typing indicator:', error);
    }
  };

  const cleanup = () => {
    beginNewChatEpoch();
    sessionTokenRef.current = null;
    setReconnectMessageVisible(false);
    if (peerTypingTimeoutRef.current) {
      clearTimeout(peerTypingTimeoutRef.current);
      peerTypingTimeoutRef.current = null;
    }
    if (apiSuccessTimeoutRef.current !== null) {
      window.clearTimeout(apiSuccessTimeoutRef.current);
      apiSuccessTimeoutRef.current = null;
    }
    if (goWsReconnectTimeoutRef.current !== null) {
      clearTimeout(goWsReconnectTimeoutRef.current);
      goWsReconnectTimeoutRef.current = null;
    }
    clearGoWsConnectTimeout();
    if (mockVideoTimerRef.current !== null) {
      window.clearInterval(mockVideoTimerRef.current);
      mockVideoTimerRef.current = null;
    }
    resetWebRTCTransport('cleanup');
  };

  return {
    connected,
    peerId,
    sessionId,
    sessionToken,
    reportPeerId,
    status,
    messages,
    peerTyping,
    isVideoConnecting,
    localVideoRef,
    remoteVideoRef,
    startSearch,
    stopSearch,
    skip,
    disconnect,
    sendMessage,
    sendTyping,
    cameraError
  };
}
