import { AdminPanelRuntime } from './AdminPanelRuntime'
import type { AdminPanelProps } from './admin-panel/types'

export function AdminPanel({ loginRoute = '/' }: AdminPanelProps) {
  return <AdminPanelRuntime loginRoute={loginRoute} />
}
