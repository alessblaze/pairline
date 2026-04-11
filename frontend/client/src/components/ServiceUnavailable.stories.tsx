import type { Meta, StoryObj } from '@storybook/react-vite';
import { MemoryRouter } from 'react-router-dom';
import { ServiceUnavailable } from './ServiceUnavailable';

import { NetworkHealthProvider } from '../hooks/useNetworkHealth';

const meta = {
  title: 'Pages/ServiceUnavailable',
  component: ServiceUnavailable,
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
} satisfies Meta<typeof ServiceUnavailable>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {};
