import type { Meta, StoryObj } from '@storybook/react-vite';
import { MemoryRouter } from 'react-router-dom';
import { LandingPage } from './LandingPage';
import loadingGif from '../assets/loading.gif';

const meta = {
  title: 'Pages/LandingPage',
  component: LandingPage,
  tags: ['autodocs'],
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
} satisfies Meta<typeof LandingPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {};

export const LoadingSkeleton: Story = {
  render: () => (
    <div className="fixed inset-0 z-[200] flex flex-col items-center justify-center bg-gradient-to-br from-slate-100 via-pink-50 to-indigo-50 dark:from-slate-950 dark:via-slate-900 dark:to-slate-950 transition-colors">
      <div className="flex flex-col items-center gap-4">
        <img
          src={loadingGif}
          alt="Loading"
          className="w-24 h-24 sm:w-32 sm:h-32 object-contain drop-shadow-[0_10px_20px_rgba(255,154,158,0.3)]"
        />
        <p className="text-sm font-semibold text-slate-400 dark:text-slate-500 tracking-widest uppercase animate-pulse">待って――それ、お風呂のお湯だ…………</p>
      </div>
    </div>
  ),
};
