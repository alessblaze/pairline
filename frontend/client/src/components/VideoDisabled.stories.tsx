import type { Meta, StoryObj } from '@storybook/react';
import { MemoryRouter } from 'react-router-dom';
import { VideoDisabled } from './VideoDisabled';

const meta = {
  title: 'Pages/VideoDisabled',
  component: VideoDisabled,
  decorators: [
    (Story) => (
      <MemoryRouter>
        <Story />
      </MemoryRouter>
    ),
  ],
  parameters: {
    layout: 'fullscreen',
  },
} satisfies Meta<typeof VideoDisabled>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {};
