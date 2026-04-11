import type { Meta, StoryObj } from '@storybook/react-vite';
import { ErrorBoundary } from './ErrorBoundary';

// A dummy component that immediately throws to trigger the Error Boundary
function Boom() {
  throw new Error('This is a simulated crash for the Storybook view!');
  return <div />;
}

const meta = {
  title: 'Components/Utility/ErrorBoundary',
  component: ErrorBoundary,
  parameters: {
    layout: 'fullscreen',
  },
} satisfies Meta<typeof ErrorBoundary>;

export default meta;
type Story = StoryObj<typeof meta>;

export const DefaultTriggered: Story = {
  render: () => (
    <ErrorBoundary>
      <Boom />
    </ErrorBoundary>
  ),
};
