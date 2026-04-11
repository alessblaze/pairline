import { AdminPanelRuntime } from './AdminPanelRuntime';
import type { AdminPanelStoryHarnessProps } from './admin-panel/storyTypes';

export function AdminPanelStoryHarness({
  loginRoute = '/',
  mockState,
}: AdminPanelStoryHarnessProps) {
  return <AdminPanelRuntime loginRoute={loginRoute} __mockState={mockState} />;
}
