import { useState, useEffect, useRef } from 'react';
import { WebSocketClient } from '../services/websocket';
import type { Message } from '../types';

export function useTextChat(wsUrl: string) {
  const [wsClient] = useState(() => new WebSocketClient(wsUrl, 'room:text'));
  const [connected, setConnected] = useState(false);
  const [peerId, setPeerId] = useState<string | null>(null);
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [sessionToken, setSessionToken] = useState<string | null>(null);
  const [reportPeerId, setReportPeerId] = useState<string | null>(null);
  const [status, setStatus] = useState<'idle' | 'searching' | 'connected' | 'disconnected'>('idle');
  const [messages, setMessages] = useState<Array<{ id: string; text: string; sender: 'me' | 'peer' | 'system'; timestamp: number }>>([]);
  const [showReconnectMessage, setShowReconnectMessage] = useState(false);
  const [peerTyping, setPeerTyping] = useState(false);
  const peerTypingTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const cleanup = () => {
    setPeerId(null);
    setSessionId(null);
    setSessionToken(null);
    setReportPeerId(null);
    setMessages([]);
    setShowReconnectMessage(false);
    setPeerTyping(false);
    if (peerTypingTimeoutRef.current) {
      clearTimeout(peerTypingTimeoutRef.current);
      peerTypingTimeoutRef.current = null;
    }
  };

  const handleMessage = (message: Message) => {
    console.log('Received message:', message);

    if (message.session_id) {
      setSessionId(message.session_id);
    }
    
    if (message.session_token) {
      setSessionToken(message.session_token);
    }
    
    switch (message.type) {
      case 'match':
        const peerIdMatch = message.peer_id;
        const common = (message as any).common_interests || [];
        setPeerId(peerIdMatch || '');
        setReportPeerId(peerIdMatch || null);
        setStatus('connected');
        setShowReconnectMessage(false);
        
        if (common.length > 0) {
          setMessages(prev => [...prev, {
            id: crypto.randomUUID(),
            text: `You both like: ${common.join(', ')}`,
            sender: 'system',
            timestamp: Date.now()
          }]);
        } else {
          setMessages(prev => [...prev, {
            id: crypto.randomUUID(),
            text: `You are talking to a random stranger.`,
            sender: 'system',
            timestamp: Date.now()
          }]);
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

      case 'disconnected':
        console.log('Peer disconnected');
        setStatus('disconnected');
        setPeerId(null);
        setPeerTyping(false);
        setMessages(prev => [...prev, {
          id: crypto.randomUUID(),
          text: 'Stranger has disconnected.',
          sender: 'system',
          timestamp: Date.now()
        }]);
        break;

      case 'banned':
        const textBanReason = message.data?.reason || (message as any).reason || 'Banned by an administrator';
        console.error('You have been banned:', textBanReason);
        setStatus('disconnected');
        setPeerTyping(false);
        setMessages(prev => [...prev, {
          id: crypto.randomUUID(),
          text: `You have been banned: ${textBanReason}`,
          sender: 'system',
          timestamp: Date.now()
        }]);
        break;

      case 'error':
        const errorMessage = message.data?.message || message.data?.error || message.data?.reason || JSON.stringify(message.data) || 'An error occurred';
        console.error('Server error:', message);
        setMessages(prev => [...prev, {
          id: crypto.randomUUID(),
          text: `Error: ${errorMessage}`,
          sender: 'system',
          timestamp: Date.now()
        }]);
        break;

      case 'timeout':
        console.log('Matchmaking timeout');
        setStatus('disconnected');
        setPeerId(null);
        setPeerTyping(false);
        setMessages(prev => [...prev, {
          id: crypto.randomUUID(),
          text: 'Matchmaking timeout: No strangers are available right now.',
          sender: 'system',
          timestamp: Date.now()
        }]);
        break;

      case 'stopped':
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
        console.log('Unknown message type:', message.type, message);
    }
  };

  useEffect(() => {
    let mounted = true;

    const setup = async () => {
      try {
        await wsClient.connect();
        if (mounted) {
          setConnected(true);
          setShowReconnectMessage(false);
        }

        wsClient.onMessage((message: Message) => {
          if (mounted) handleMessage(message);
        });

        wsClient.onClose(() => {
          if (mounted) {
            setConnected(false);
            setStatus(prev => {
              if (prev === 'connected' || prev === 'searching') {
                if (!showReconnectMessage) {
                  setMessages(msgs => [...msgs, {
                    id: crypto.randomUUID(),
                    text: 'Connection to server lost. Reconnecting...',
                    sender: 'system',
                    timestamp: Date.now()
                  }]);
                  setShowReconnectMessage(true);
                }
                return 'disconnected';
              }
              return prev;
            });
            setPeerId(null);
          }
        });
      } catch (error) {
        console.error('Failed to connect:', error);
        if (mounted) {
          setConnected(false);
          setStatus('disconnected');
          if (!showReconnectMessage) {
            setMessages(msgs => [...msgs, {
              id: crypto.randomUUID(),
              text: 'Failed to connect to server. Please refresh the page.',
              sender: 'system',
              timestamp: Date.now()
            }]);
            setShowReconnectMessage(true);
          }
        }
      }
    };

    setup();

    return () => {
      mounted = false;
      cleanup();
      wsClient.disconnect();
    };
  }, [wsClient]);

  const startSearch = async (interests: string = '') => {
    try {
      if (!wsClient.isConnected()) {
        await wsClient.connect();
        setConnected(true);
      }

      setMessages([]);
      setShowReconnectMessage(false);
      setReportPeerId(null);
      setStatus('searching');

      wsClient.send('start', { 
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
      setStatus('disconnected');
      setPeerId(null);
      setPeerTyping(false);
      setMessages(prev => [...prev, {
        id: crypto.randomUUID(),
        text: 'You have disconnected.',
        sender: 'system',
        timestamp: Date.now()
      }]);
    } catch (error) {
      console.error('Failed to disconnect:', error);
    }
  };

  const skip = () => {
    try {
      if (status === 'connected') {
        wsClient.send('skip', {});
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
        wsClient.send('message', { content: text });
        setMessages(prev => [...prev, {
          id: crypto.randomUUID(),
          text,
          sender: 'me',
          timestamp: Date.now()
        }]);
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
