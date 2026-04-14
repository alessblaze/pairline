import type { Report } from '../../types';

export interface BanModalState {
  open: boolean;
  sessionId: string;
  ip: string;
  sourceReportId?: string;
  target: 'session' | 'ip';
  reason: string;
  mode: 'permanent' | 'temporary';
  durationValue: string;
  durationUnit: 'hours' | 'days';
  clearManualInputsOnSubmit?: boolean;
}

export interface AdminPanelProps {
  loginRoute?: string;
}

export type RawReport = Omit<Report, 'chat_log'> & {
  chat_log: string | Report['chat_log'] | null;
  auto_moderation_categories?: string | Report['auto_moderation_categories'] | null;
};
