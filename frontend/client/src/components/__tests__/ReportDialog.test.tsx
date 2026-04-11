import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { ReportDialog } from '../ReportDialog';

const setApiStatus = vi.fn();

vi.mock('../../hooks/useNetworkHealth', () => ({
  useNetworkHealth: () => ({
    setApiStatus,
  }),
}));

describe('ReportDialog', () => {
  const onClose = vi.fn();
  const alertSpy = vi.spyOn(window, 'alert').mockImplementation(() => {});

  beforeEach(() => {
    onClose.mockReset();
    setApiStatus.mockReset();
    alertSpy.mockClear();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('submits a report and renders the success state', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true });
    vi.stubGlobal('fetch', fetchMock);

    render(
      <ReportDialog
        peerId="peer-123"
        reporterSessionId="session-1"
        reporterToken="token-1"
        messages={[
          { id: '1', sender: 'system', text: 'connected', timestamp: 1 },
          { id: '2', sender: 'peer', text: 'hello', timestamp: 2 },
          { id: '3', sender: 'me', text: 'hi', timestamp: 3 },
        ]}
        onClose={onClose}
      />
    );

    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'harassment' } });
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Repeated abuse' } });
    fireEvent.click(screen.getByRole('button', { name: /submit report/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });

    const [, init] = fetchMock.mock.calls[0];
    expect(init?.method).toBe('POST');
    expect(JSON.parse(String(init?.body))).toMatchObject({
      reporter_session_id: 'session-1',
      reporter_token: 'token-1',
      reported_session_id: 'peer-123',
      reason: 'harassment',
      description: 'Repeated abuse',
      chat_log: [
        { id: '2', sender: 'peer', text: 'hello', timestamp: 2 },
        { id: '3', sender: 'me', text: 'hi', timestamp: 3 },
      ],
    });

    expect(await screen.findByText(/report submitted/i)).toBeInTheDocument();
    expect(setApiStatus).toHaveBeenCalledWith('ok');
  });

  it('alerts when the session metadata is missing before submitting', () => {
    render(
      <ReportDialog
        peerId="peer-123"
        reporterSessionId={null}
        reporterToken={null}
        messages={[]}
        onClose={onClose}
      />
    );

    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'spam' } });
    fireEvent.click(screen.getByRole('button', { name: /submit report/i }));

    expect(alertSpy).toHaveBeenCalledWith('Your chat session is no longer valid. Please start a new chat and try again.');
  });
});
