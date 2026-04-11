import type { Meta, StoryObj } from '@storybook/react';
import { EntryModal } from './EntryModal';
import { fn } from 'storybook/test';

const meta = {
  title: 'Components/Modals/EntryModal',
  component: EntryModal,
  parameters: {
    layout: 'fullscreen',
  },
} satisfies Meta<typeof EntryModal>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  args: {
    onClose: fn(),
    onConfirm: fn(),
  },
};
