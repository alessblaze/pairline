import type { Meta, StoryObj } from '@storybook/react-vite';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { VideoChatView, type VideoChatState } from './VideoChat';

import { NetworkHealthProvider } from '../hooks/useNetworkHealth';
import { fn } from 'storybook/test';

const meta = {
  title: 'Pages/VideoChat',
  component: VideoChatView,
  decorators: [
    (Story) => (
      <MemoryRouter>
        <NetworkHealthProvider>
          <Story />
        </NetworkHealthProvider>
      </MemoryRouter>
    ),
  ],
  parameters: {
    layout: 'fullscreen',
  },
} satisfies Meta<typeof VideoChatView>;

export default meta;
type Story = StoryObj<typeof meta>;

const createDummyRef = () => React.createRef<HTMLVideoElement>();

const defaultMockState: VideoChatState = {
  connected: true,
  status: 'idle',
  messages: [],
  peerTyping: false,
  startSearch: fn(),
  stopSearch: fn(),
  disconnect: fn(),
  skip: fn(),
  sendMessage: fn(),
  sendTyping: fn(),
  reportPeerId: null,
  sessionId: 'sess-123',
  sessionToken: 'tok-xyz',
  isVideoConnecting: false,
  cameraError: null,
  localVideoRef: createDummyRef(),
  remoteVideoRef: createDummyRef(),
};

export const Idle: Story = {
  args: {
    state: {
      ...defaultMockState,
      status: 'idle',
    },
  },
};

export const Searching: Story = {
  args: {
    state: {
      ...defaultMockState,
      status: 'searching',
    },
  },
};

export const ConnectingVideo: Story = {
  args: {
    state: {
      ...defaultMockState,
      status: 'connected',
      isVideoConnecting: true,
    },
  },
};

export const ConnectedEmpty: Story = {
  args: {
    state: {
      ...defaultMockState,
      status: 'connected',
    },
  },
};

export const ConnectedWithMessages: Story = {
  args: {
    state: {
      ...defaultMockState,
      status: 'connected',
      peerTyping: true,
      messages: [
        { id: '1', sender: 'system', text: 'You are now chatting with a random stranger!', timestamp: Date.now() },
        { id: '2', sender: 'peer', text: 'turn your mic on', timestamp: Date.now() },
      ],
      reportPeerId: 'peer-123',
    },
  },
};

export const Disconnected: Story = {
  args: {
    state: {
      ...defaultMockState,
      connected: false,
      status: 'disconnected',
      messages: [
        { id: '1', sender: 'system', text: 'You are now chatting with a random stranger!', timestamp: Date.now() },
        { id: '3', sender: 'system', text: 'Stranger has disconnected.', timestamp: Date.now() },
      ],
      reportPeerId: 'peer-123',
    },
  },
};

export const CameraError: Story = {
  args: {
    state: {
      ...defaultMockState,
      status: 'idle',
      cameraError: 'NotAllowedError: Permission denied',
    },
  },
};

export const ConnectedWithInterests: Story = {
  args: {
    state: {
      ...defaultMockState,
      status: 'connected',
      messages: [
        { id: '1', sender: 'system', text: 'You both like: coding, music', timestamp: Date.now() },
        { id: '2', sender: 'peer', text: 'Hey! What are you working on?', timestamp: Date.now() },
        { id: '3', sender: 'me', text: 'Building a chat app, you?', timestamp: Date.now() },
      ],
      reportPeerId: 'peer-456',
    },
  },
};

export const PartialDisconnect: Story = {
  decorators: [
    (Story) => (
      <NetworkHealthProvider initialChannelStatuses={{ 'phoenix:video': 'degraded' }}>
        <Story />
      </NetworkHealthProvider>
    ),
  ],
  args: {
    state: {
      ...defaultMockState,
      connected: false,
      status: 'disconnected',
      messages: [
        { id: '1', sender: 'system', text: 'You are now chatting with a random stranger!', timestamp: Date.now() },
        { id: '2', sender: 'peer', text: 'Hey! Can you see me?', timestamp: Date.now() },
        { id: '3', sender: 'me', text: 'Yeah, loud and clear!', timestamp: Date.now() },
        { id: '4', sender: 'system', text: 'Connection to server lost. Reconnecting...', timestamp: Date.now() },
      ],
      reportPeerId: 'peer-789',
    },
  },
};
