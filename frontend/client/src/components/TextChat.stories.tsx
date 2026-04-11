import type { Meta, StoryObj } from '@storybook/react-vite';
import { MemoryRouter } from 'react-router-dom';
import { TextChatView, type TextChatState } from './TextChat';

import { NetworkHealthProvider } from '../hooks/useNetworkHealth';
import { fn } from 'storybook/test';

const meta = {
  title: 'Pages/TextChat',
  component: TextChatView,
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
} satisfies Meta<typeof TextChatView>;

export default meta;
type Story = StoryObj<typeof meta>;

const defaultMockState: TextChatState = {
  connected: true,
  status: 'idle',
  messages: [],
  peerTyping: false,
  startSearch: fn(),
  stopSearch: fn(),
  disconnect: fn(),
  sendMessage: fn(),
  sendTyping: fn(),
  reportPeerId: null,
  sessionId: 'sess-123',
  sessionToken: 'tok-xyz',
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
        { id: '2', sender: 'me', text: 'Hi! How are you doing?', timestamp: Date.now() },
        { id: '3', sender: 'peer', text: 'I am doing great, just browsing around.', timestamp: Date.now() },
        { id: '4', sender: 'me', text: 'Nice, where are you from?', timestamp: Date.now() },
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
        { id: '2', sender: 'me', text: 'Hello?', timestamp: Date.now() },
        { id: '3', sender: 'system', text: 'Stranger has disconnected.', timestamp: Date.now() },
      ],
      reportPeerId: 'peer-123',
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
        { id: '2', sender: 'peer', text: 'Hey! Nice, what language do you code in?', timestamp: Date.now() },
        { id: '3', sender: 'me', text: 'TypeScript mostly, you?', timestamp: Date.now() },
      ],
      reportPeerId: 'peer-456',
    },
  },
};

export const PartialDisconnect: Story = {
  args: {
    state: {
      ...defaultMockState,
      connected: false,
      status: 'disconnected',
      messages: [
        { id: '1', sender: 'system', text: 'You are now chatting with a random stranger!', timestamp: Date.now() },
        { id: '2', sender: 'me', text: 'Hey, how are you?', timestamp: Date.now() },
        { id: '3', sender: 'peer', text: 'Good! What do you do?', timestamp: Date.now() },
        { id: '4', sender: 'system', text: 'Connection to server lost. Reconnecting...', timestamp: Date.now() },
      ],
      reportPeerId: 'peer-789',
    },
  },
};
