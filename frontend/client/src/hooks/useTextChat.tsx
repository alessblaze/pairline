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

export function useTextChat(wsUrl: string) {
  const { setChannelStatus, removeChannel } = useNetworkHealth();
  const [wsClient] = useState(() => new WebSocketClient(wsUrl, 'room:text'));
  const [connected, setConnected] = useState(false);
  const [peerId, setPeerId] = useState<string | null>(null);
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [sessionToken, setSessionToken] = useState<string | null>(null);
  const [reportPeerId, setReportPeerId] = useState<string | null>(null);
  const [status, setStatus] = useState<'idle' | 'searching' | 'connected' | 'disconnected'>('idle');
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [, setShowReconnectMessage] = useState(false);
  const [peerTyping, setPeerTyping] = useState(false);
  const peerTypingTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const showReconnectMessageRef = useRef(false);
  const chatEpochRef = useRef(0);

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

  const cleanup = () => {
    beginNewChatEpoch();
    setPeerId(null);
    setSessionId(null);
    setSessionToken(null);
    setReportPeerId(null);
    setMessages([]);
    setReconnectMessageVisible(false);
    setPeerTyping(false);
    if (peerTypingTimeoutRef.current) {
      clearTimeout(peerTypingTimeoutRef.current);
      peerTypingTimeoutRef.current = null;
    }
  };

  const handleMessage = (message: Message) => {
    if (import.meta.env.VITE_WEBSOCKET_DEBUG === 'true') {
      console.log('Received message:', message);
    }

    if (message.session_id) {
      setSessionId(message.session_id);
    }

    if (message.session_token) {
      setSessionToken(message.session_token);
    }

    switch (message.type) {
      case 'match':
        beginNewChatEpoch();
        const peerIdMatch = message.peer_id;
        const common = (message as any).common_interests || [];
        const isBotMatch = message.partner_session_kind === 'bot';
        setPeerId(peerIdMatch || '');
        setReportPeerId(!isBotMatch && message.reportable !== false ? (peerIdMatch || null) : null);
        setStatus('connected');
        setReconnectMessageVisible(false);
        setPeerTyping(false);

        if (peerTypingTimeoutRef.current) {
          clearTimeout(peerTypingTimeoutRef.current);
          peerTypingTimeoutRef.current = null;
        }

        const systemMessages: ChatMessage[] = [];
        if (common.length > 0) {
          systemMessages.push({
            id: crypto.randomUUID(),
            text: `You both like: ${common.join(', ')}`,
            sender: 'system',
            timestamp: Date.now()
          });
        }
        systemMessages.push({
          id: crypto.randomUUID(),
          text: `You are talking to a random stranger.`,
          sender: 'system',
          timestamp: Date.now()
        });
        setMessages(systemMessages);
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

      case 'disconnected':
        if (import.meta.env.VITE_WEBSOCKET_DEBUG === 'true') {
          console.log('Peer disconnected');
        }
        setStatus('disconnected');
        setPeerId(null);
        setPeerTyping(false);
        beginNewChatEpoch();
        pushSystemMessage('Stranger has disconnected.');
        break;

      case 'banned':
        const textBanReason = message.data?.reason || (message as any).reason || 'Banned by an administrator';
        console.error('You have been banned:', textBanReason);
        setStatus('disconnected');
        setPeerTyping(false);
        beginNewChatEpoch();
        pushSystemMessage(`You have been banned: ${textBanReason}`);
        break;

      case 'error':
        const errorMessage = message.data?.message || message.data?.error || message.data?.reason || JSON.stringify(message.data) || 'An error occurred';
        console.error('Server error:', message);
        // If CAPTCHA verification failed, revert to idle so user can retry
        if (typeof errorMessage === 'string' && errorMessage.includes('CAPTCHA')) {
          setStatus('idle');
        }
        pushSystemMessage(`Error: ${errorMessage}`);
        break;

      case 'timeout':
        if (import.meta.env.VITE_WEBSOCKET_DEBUG === 'true') {
          console.log('Matchmaking timeout');
        }
        setStatus('disconnected');
        setPeerId(null);
        setPeerTyping(false);
        beginNewChatEpoch();
        pushSystemMessage('Matchmaking timeout: No strangers are available right now.');
        break;

      case 'stopped':
        beginNewChatEpoch();
        setStatus('idle');
        setPeerId(null);
        setSessionId(null);
        setSessionToken(null);
        setReportPeerId(null);
        setMessages([]);
        setPeerTyping(false);
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

      default:
        if (import.meta.env.VITE_WEBSOCKET_DEBUG === 'true') {
          console.log('Unknown message type:', message.type, message);
        }
    }
  };

  useEffect(() => {
    let mounted = true;

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
          if (mounted) setChannelStatus('phoenix:text', 'degraded');
        });

        wsClient.onMaxRetries(() => {
          if (mounted) setChannelStatus('phoenix:text', 'offline');
        });

        wsClient.onOpen(() => {
          if (mounted) {
            setChannelStatus('phoenix:text', 'ok');
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
            setReportPeerId(null);
            setPeerTyping(false);
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
      removeChannel('phoenix:text');
      cleanup();
      wsClient.disconnect();
    };
  }, [wsClient]);

  const startSearch = async (interests: string = '', turnstileToken?: string) => {
    try {
      if (!wsClient.isConnected()) {
        await wsClient.connect();
        setConnected(true);
      }

      setMessages([]);
      beginNewChatEpoch();
      setReconnectMessageVisible(false);
      setReportPeerId(null);
      setStatus('searching');

      wsClient.send('start', {
        token: turnstileToken,
        preferences: {
          mode: 'text',
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
      setReportPeerId(null);
      wsClient.send('stop', {});
    } catch (error) {
      console.error('Failed to stop search:', error);
    }
  };

  const disconnect = () => {
    try {
      wsClient.send('disconnect', {});
      beginNewChatEpoch();
      setStatus('disconnected');
      setPeerId(null);
      setPeerTyping(false);
      pushSystemMessage('You have disconnected.');
    } catch (error) {
      console.error('Failed to disconnect:', error);
    }
  };

  const skip = () => {
    try {
      if (status === 'connected') {
        wsClient.send('skip', {});
        beginNewChatEpoch();
        setPeerId(null);
        setReportPeerId(null);
        setMessages([]);
        setPeerTyping(false);
        setStatus('searching');
      }
    } catch (error) {
      console.error('Failed to skip:', error);
    }
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

  return {
    connected,
    peerId,
    sessionId,
    sessionToken,
    reportPeerId,
    status,
    messages,
    peerTyping,
    startSearch,
    stopSearch,
    disconnect,
    sendMessage,
    sendTyping,
    skip
  };
}
