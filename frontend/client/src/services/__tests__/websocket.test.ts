import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

type ReceiveStatus = 'ok' | 'error';
const mockState = vi.hoisted(() => ({
  latestSocket: null as {
    channelInstance: {
      pushes: Array<{ event: string; payload: unknown }>;
    };
  } | null
}));

vi.mock('phoenix', () => {
  class FakePush {
    receive(status: ReceiveStatus, callback: (payload?: unknown) => void) {
      if (status === 'ok') {
        callback({});
      }
      return this;
    }
  }

  class FakeChannel {
    state = 'closed';
    pushes: Array<{ event: string; payload: unknown }> = [];

    join() {
      const response = {
        receive: (status: ReceiveStatus, callback: () => void) => {
          if (status === 'ok') {
            this.state = 'joined';
            callback();
          }
          return response;
        }
      };
      return response;
    }

    on() {
      return this;
    }

    push(event: string, payload: unknown) {
      this.pushes.push({ event, payload });
      return new FakePush();
    }

    leave() {
      this.state = 'closed';
      return this;
    }
  }

  return {
    Socket: class MockSocket {
      channelInstance = new FakeChannel();
      private openHandler: (() => void) | null = null;
      private closeHandler: ((event: unknown) => void) | null = null;

      constructor(url: string) {
        void url;
        mockState.latestSocket = this;
      }

      onOpen(handler: () => void) {
        this.openHandler = handler;
      }

      onClose(handler: (event: unknown) => void) {
        this.closeHandler = handler;
      }

      onError() {}

      connect() {
        this.openHandler?.();
      }

      disconnect() {
        this.closeHandler?.({});
      }

      channel() {
        return this.channelInstance;
      }
    }
  };
});

import { WebSocketClient } from '../websocket';

describe('WebSocketClient room heartbeat', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    mockState.latestSocket = null;
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  it('sends periodic room ping messages after joining', async () => {
    const client = new WebSocketClient('ws://example.test/ws', 'room:video');

    await client.connect();

    expect(mockState.latestSocket).not.toBeNull();
    expect(mockState.latestSocket?.channelInstance.pushes).toEqual([]);

    vi.advanceTimersByTime(30_000);
    expect(mockState.latestSocket?.channelInstance.pushes).toEqual([{ event: 'ping', payload: {} }]);

    vi.advanceTimersByTime(30_000);
    expect(mockState.latestSocket?.channelInstance.pushes).toHaveLength(2);
  });

  it('stops sending room pings after disconnect', async () => {
    const client = new WebSocketClient('ws://example.test/ws', 'room:video');

    await client.connect();
    vi.advanceTimersByTime(30_000);
    expect(mockState.latestSocket?.channelInstance.pushes).toHaveLength(1);

    client.disconnect();
    vi.advanceTimersByTime(90_000);

    expect(mockState.latestSocket?.channelInstance.pushes).toHaveLength(1);
  });
});
