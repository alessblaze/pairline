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

import type { Message } from '../types';

import { Socket, type Channel } from 'phoenix';

const roomPingIntervalMs = 30_000;

export class WebSocketClient {
  private socket: Socket | null = null;
  private channel: Channel | null = null;
  private connectPromise: Promise<void> | null = null;
  private url: string;
  private topic: string;
  private messageHandlers: ((msg: Message) => void)[] = [];
  private closeHandlers: (() => void)[] = [];
  private openHandlers: (() => void)[] = [];
  private maxRetriesHandlers: (() => void)[] = [];
  private reconnectingHandlers: (() => void)[] = [];
  private reconnectAttempts = 0;
  private maxReconnectAttempts = 10;
  private reconnectDelay = 1000;
  private reconnectTimeout: ReturnType<typeof setTimeout> | null = null;
  private roomPingInterval: ReturnType<typeof setInterval> | null = null;
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
            this.startRoomHeartbeat();
            this.openHandlers.forEach(handler => handler());
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
      console.log('You are connected to chat server.');
    });

    this.socket.onError((error: unknown) => {
      console.error('Phoenix socket error:', error);
    });

    this.socket.onClose((event: unknown) => {
      if (import.meta.env.VITE_WEBSOCKET_DEBUG === 'true') {
        console.log('Phoenix socket disconnected', event);
      }
      this.isConnecting = false;
      this.connectPromise = null;
      this.stopRoomHeartbeat();
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
      this.reconnectingHandlers.forEach(handler => handler());
      const delay = Math.min(this.reconnectDelay * Math.pow(2, this.reconnectAttempts), 30000);
      console.warn(`Reconnecting in ${delay}ms (attempt ${this.reconnectAttempts + 1}/${this.maxReconnectAttempts})`);

      this.reconnectTimeout = setTimeout(() => {
        this.reconnectAttempts++;
        this.connect().catch((error) => {
          console.error('Reconnection failed:', error);
        });
      }, delay);
    } else if (this.reconnectAttempts >= this.maxReconnectAttempts) {
      console.error('Max reconnection attempts reached');
      this.shouldReconnect = false;
      this.maxRetriesHandlers.forEach(handler => handler());
    }
  }

  private startRoomHeartbeat() {
    this.stopRoomHeartbeat();

    this.roomPingInterval = setInterval(() => {
      if (!this.isConnected() || !this.channel) {
        return;
      }

      this.channel.push('ping', {})
        .receive('error', (payload: unknown) => {
          if (import.meta.env.VITE_WEBSOCKET_DEBUG === 'true') {
            console.warn('Room heartbeat failed:', payload);
          }
        });
    }, roomPingIntervalMs);
  }

  private stopRoomHeartbeat() {
    if (this.roomPingInterval) {
      clearInterval(this.roomPingInterval);
      this.roomPingInterval = null;
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

  sendWithResponse(type: string, data: any, timeoutMs = 8000): Promise<any> {
    try {
      if (this.isConnected() && this.channel) {
        return new Promise((resolve, reject) => {
          const timeoutId = setTimeout(() => {
            reject(new Error(`Timed out waiting for "${type}" response`));
          }, timeoutMs);

          this.channel?.push(type, { data })
            .receive('ok', (payload: Message | { status?: string }) => {
              clearTimeout(timeoutId);
              resolve(payload);
            })
            .receive('error', (payload: Message | { reason?: string; error?: string; type?: string }) => {
              clearTimeout(timeoutId);
              if (payload && typeof payload === 'object' && 'type' in payload && payload.type) {
                resolve(payload as Message);
                return;
              }

              const errorPayload: Message = {
                type: 'error',
                data: payload
              };

              resolve(errorPayload);
            });
        });
      }

      console.error('WebSocket not connected');
      throw new Error('WebSocket not connected');
    } catch (error) {
      console.error('Failed to send message with response:', error);
      return Promise.reject(error);
    }
  }

  onMessage(handler: (msg: Message) => void) {
    this.messageHandlers.push(handler);
  }

  onClose(handler: () => void) {
    this.closeHandlers.push(handler);
  }

  onOpen(handler: () => void) {
    this.openHandlers.push(handler);
  }

  onMaxRetries(handler: () => void) {
    this.maxRetriesHandlers.push(handler);
  }

  onReconnecting(handler: () => void) {
    this.reconnectingHandlers.push(handler);
  }

  disconnect() {
    this.shouldReconnect = false;
    this.isConnecting = false;
    this.connectPromise = null;
    this.stopRoomHeartbeat();

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
    this.openHandlers = [];
    this.maxRetriesHandlers = [];
    this.reconnectingHandlers = [];
  }

  isConnected(): boolean {
    return this.channel !== null && this.channel.state === 'joined';
  }

  private dispatchMessage(message: Message) {
    if (import.meta.env.VITE_WEBSOCKET_DEBUG === 'true') {
      console.log('WebSocket received:', message);
    }

    this.messageHandlers.forEach(handler => handler(message));
  }
}
