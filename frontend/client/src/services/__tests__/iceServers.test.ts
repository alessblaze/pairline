import { describe, expect, it } from 'vitest';

import { normalizeIceServersForBootstrap } from '../iceServers';

describe('normalizeIceServersForBootstrap', () => {
  it('deduplicates duplicate URLs for all modes', () => {
    const iceServers: RTCIceServer[] = [
      {
        urls: [
          'stun:stun.cloudflare.com:3478',
          'stun:stun.cloudflare.com:3478',
          'turn:relay.example.com:3478?transport=udp',
          'turn:relay.example.com:3478?transport=udp',
        ],
        username: 'user',
        credential: 'cred',
      },
    ];

    expect(normalizeIceServersForBootstrap(iceServers, 'cloudflare')).toEqual([
      {
        urls: [
          'stun:stun.cloudflare.com:3478',
          'turn:relay.example.com:3478?transport=udp',
        ],
        username: 'user',
        credential: 'cred',
      },
    ]);
  });

  it('reduces integrated TURN fan-out to one URL per transport class', () => {
    const iceServers: RTCIceServer[] = [
      { urls: 'stun:stun.cloudflare.com:3478' },
      {
        urls: [
          'turn:turn-a.example.com:3478?transport=udp',
          'turn:turn-a.example.com:3478?transport=tcp',
          'turn:turn-b.example.com:3478?transport=udp',
          'turn:turn-b.example.com:3478?transport=tcp',
        ],
        username: 'session|digest',
        credential: 'shared-secret',
      },
    ];

    expect(normalizeIceServersForBootstrap(iceServers, 'integrated')).toEqual([
      { urls: ['stun:stun.cloudflare.com:3478'] },
      {
        urls: [
          'turn:turn-a.example.com:3478?transport=udp',
          'turn:turn-a.example.com:3478?transport=tcp',
        ],
        username: 'session|digest',
        credential: 'shared-secret',
      },
    ]);
  });

  it('prefers TURNS over multiple plain TCP integrated URLs when both are present', () => {
    const iceServers: RTCIceServer[] = [
      {
        urls: [
          'turn:turn-a.example.com:3478?transport=tcp',
          'turns:turn-a.example.com:5349?transport=tcp',
          'turn:turn-b.example.com:3478?transport=tcp',
        ],
        username: 'session|digest',
        credential: 'shared-secret',
      },
    ];

    expect(normalizeIceServersForBootstrap(iceServers, 'integrated')).toEqual([
      {
        urls: [
          'turns:turn-a.example.com:5349?transport=tcp',
          'turn:turn-a.example.com:3478?transport=tcp',
        ],
        username: 'session|digest',
        credential: 'shared-secret',
      },
    ]);
  });
});
