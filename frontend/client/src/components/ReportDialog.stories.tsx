import type { Meta, StoryObj } from '@storybook/react';
import { MemoryRouter } from 'react-router-dom';
import { ReportDialog } from './ReportDialog';
import { NetworkHealthProvider } from '../hooks/useNetworkHealth';
import { fn } from 'storybook/test';

const meta = {
  title: 'Components/Modals/ReportDialog',
  component: ReportDialog,
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
} satisfies Meta<typeof ReportDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  args: {
    peerId: 'mock-peer-id-123',
    reporterSessionId: 'my-session',
    reporterToken: 'my-token',
    messages: [
      { id: '1', sender: 'system', text: 'You are now chatting with a stranger.', timestamp: Date.now() },
      { id: '2', sender: 'peer', text: 'Hey there you idiot', timestamp: Date.now() },
    ],
    onClose: fn(),
  },
};
