export class Socket {
  constructor(endPoint: string, opts?: Record<string, unknown>);
  connect(): void;
  disconnect(callback?: () => void, code?: number, reason?: string): void;
  channel(topic: string, params?: Record<string, unknown>): Channel;
  onOpen(callback: () => void): void;
  onClose(callback: (event: unknown) => void): void;
  onError(callback: (error: unknown) => void): void;
}

export class Channel {
  state: string;
  join(timeout?: number): Push;
  leave(timeout?: number): Push;
  push(event: string, payload: any, timeout?: number): Push;
  on(event: string, callback: (payload: any, ref?: string, joinRef?: string) => void): void;
}

export class Push {
  receive(status: string, callback: (payload: any) => void): Push;
}
