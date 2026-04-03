import type { Message } from '../types';

import { Socket, type Channel } from '../vendor/phoenix.mjs';

export class WebSocketClient {
  private socket: Socket | null = null;
  private channel: Channel | null = null;
  private connectPromise: Promise<void> | null = null;
  private url: string;
  private topic: string;
  private messageHandlers: ((msg: Message) => void)[] = [];
  private closeHandlers: (() => void)[] = [];
  private reconnectAttempts = 0;
  private maxReconnectAttempts = 10;
  private reconnectDelay = 1000;
  private reconnectTimeout: ReturnType<typeof setTimeout> | null = null;
  private shouldReconnect = true;
  private isConnecting = false;

  constructor(url: string, topic = 'room:lobby') {
    this.url = url;
    this.topic = topic;
  }

  connect(): Promise<void> {
    if (this.isConnected()) {
      return Promise.resolve();
    }

    if (this.connectPromise) {
      return this.connectPromise;
    }

    if (this.channel && this.channel.state === 'joining') {
      return Promise.resolve();
    }

    this.isConnecting = true;

    this.connectPromise = new Promise((resolve, reject) => {
      try {
        this.ensureSocket();
        this.ensureChannel();

        this.socket?.connect();
        const channel = this.channel;

        if (!channel) {
          throw new Error('Channel is not initialized');
        }

        channel.join()
          .receive('ok', () => {
            this.isConnecting = false;
            this.connectPromise = null;
            this.reconnectAttempts = 0;
            this.shouldReconnect = true;
            resolve();
          })
          .receive('error', (error: unknown) => {
            this.isConnecting = false;
            this.connectPromise = null;
            reject(error);
          });
      } catch (error) {
        this.isConnecting = false;
        this.connectPromise = null;
        reject(error);
      }
    });

    return this.connectPromise;
  }

  private ensureSocket() {
    if (this.socket) {
      return;
    }

    this.socket = new Socket(this.url);

    this.socket.onOpen(() => {
      console.log('Phoenix socket connected');
    });

    this.socket.onError((error: unknown) => {
      console.error('Phoenix socket error:', error);
    });

    this.socket.onClose((event: unknown) => {
      console.log('Phoenix socket disconnected', event);
      this.isConnecting = false;
      this.connectPromise = null;
      this.channel = null;
      this.socket = null;
      this.closeHandlers.forEach(handler => handler());

      if (this.shouldReconnect) {
        this.handleReconnect();
      }
    });
  }

  private ensureChannel() {
    if (!this.socket) {
      throw new Error('Socket is not initialized');
    }

    if (this.channel && this.channel.state !== 'closed') {
      return;
    }

    this.channel = this.socket.channel(this.topic, {});

    this.channel.on('match', (payload: Omit<Message, 'type'>) => {
      this.dispatchMessage({ type: 'match', ...payload });
    });

    this.channel.on('message', (payload: Omit<Message, 'type'>) => {
      this.dispatchMessage({ type: 'message', ...payload });
    });

    this.channel.on('typing', (payload: Omit<Message, 'type'>) => {
      this.dispatchMessage({ type: 'typing', ...payload });
    });

    this.channel.on('webrtc_start', (payload: Omit<Message, 'type'>) => {
      this.dispatchMessage({ type: 'webrtc_start', ...payload });
    });

    this.channel.on('offer', (payload: Omit<Message, 'type'>) => {
      this.dispatchMessage({ type: 'offer', ...payload });
    });

    this.channel.on('answer', (payload: Omit<Message, 'type'>) => {
      this.dispatchMessage({ type: 'answer', ...payload });
    });

    this.channel.on('ice', (payload: Omit<Message, 'type'>) => {
      this.dispatchMessage({ type: 'ice', ...payload });
    });

    this.channel.on('disconnected', (payload: Omit<Message, 'type'>) => {
      this.dispatchMessage({ type: 'disconnected', ...payload });
    });

    this.channel.on('timeout', (payload: Omit<Message, 'type'>) => {
      this.dispatchMessage({ type: 'timeout', ...payload });
    });

    this.channel.on('banned', (payload: Omit<Message, 'type'>) => {
      this.dispatchMessage({ type: 'banned', ...payload });
    });
  }

  private handleReconnect() {
    if (this.reconnectAttempts < this.maxReconnectAttempts && this.shouldReconnect && !this.isConnecting) {
      const delay = Math.min(this.reconnectDelay * Math.pow(2, this.reconnectAttempts), 30000);
      console.log(`Reconnecting in ${delay}ms (attempt ${this.reconnectAttempts + 1}/${this.maxReconnectAttempts})`);
      
      this.reconnectTimeout = setTimeout(() => {
        this.reconnectAttempts++;
        this.connect().catch((error) => {
          console.error('Reconnection failed:', error);
        });
      }, delay);
    } else if (this.reconnectAttempts >= this.maxReconnectAttempts) {
      console.error('Max reconnection attempts reached');
      this.shouldReconnect = false;
    }
  }

  send(type: string, data: any) {
    try {
      if (this.isConnected() && this.channel) {
        this.channel.push(type, { data })
          .receive('ok', (payload: Message) => {
            if (payload?.type) {
              this.dispatchMessage(payload);
            }
          })
          .receive('error', (payload: Message | { reason?: string; error?: string; type?: string }) => {
            if (payload && typeof payload === 'object' && 'type' in payload && payload.type) {
              this.dispatchMessage(payload as Message);
              return;
            }

            this.dispatchMessage({
              type: 'error',
              data: payload
            });
          });
      } else {
        console.error('WebSocket not connected');
        throw new Error('WebSocket not connected');
      }
    } catch (error) {
      console.error('Failed to send message:', error);
      throw error;
    }
  }

  onMessage(handler: (msg: Message) => void) {
    this.messageHandlers.push(handler);
  }

  onClose(handler: () => void) {
    this.closeHandlers.push(handler);
  }

  disconnect() {
    this.shouldReconnect = false;
    this.isConnecting = false;
    this.connectPromise = null;
    
    if (this.reconnectTimeout) {
      clearTimeout(this.reconnectTimeout);
      this.reconnectTimeout = null;
    }

    if (this.channel && this.channel.state !== 'closed') {
      this.channel.leave();
    }
    this.channel = null;
    this.socket?.disconnect();
    this.socket = null;
    
    this.messageHandlers = [];
    this.closeHandlers = [];
  }

  isConnected(): boolean {
    return this.channel !== null && this.channel.state === 'joined';
  }

  private dispatchMessage(message: Message) {
    console.log('WebSocket received:', message);

    this.messageHandlers.forEach(handler => handler(message));
  }
}
