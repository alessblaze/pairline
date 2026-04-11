import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { EntryModal } from '../EntryModal';

const turnstileState = {
  token: 'turnstile-token',
};

vi.mock('../../hooks/useTheme', () => ({
  useTheme: () => ({
    theme: 'light',
  }),
}));

vi.mock('@marsidev/react-turnstile', () => ({
  Turnstile: ({ onSuccess, onWidgetLoad }: { onSuccess?: (token: string) => void; onWidgetLoad?: () => void }) => (
    <button
      type="button"
      data-testid="turnstile-mock"
      onClick={() => {
        onWidgetLoad?.();
        onSuccess?.(turnstileState.token);
      }}
    >
      Solve captcha
    </button>
  ),
}));

describe('EntryModal', () => {
  const onClose = vi.fn();
  const onConfirm = vi.fn();

  beforeEach(() => {
    onClose.mockReset();
    onConfirm.mockReset();
  });

  it('keeps confirm disabled until both checkboxes and captcha are completed', () => {
    render(<EntryModal onClose={onClose} onConfirm={onConfirm} />);

    const confirmButton = screen.getByRole('button', { name: /confirm & continue/i });
    expect(confirmButton).toBeDisabled();

    fireEvent.click(screen.getByLabelText(/terms of service/i));
    expect(confirmButton).toBeDisabled();

    fireEvent.click(screen.getByLabelText(/not/i));
    expect(confirmButton).toBeDisabled();

    fireEvent.click(screen.getByTestId('turnstile-mock'));
    expect(confirmButton).toBeEnabled();
  });

  it('submits the solved captcha token once requirements are satisfied', () => {
    render(<EntryModal onClose={onClose} onConfirm={onConfirm} />);

    fireEvent.click(screen.getByLabelText(/terms of service/i));
    fireEvent.click(screen.getByLabelText(/not/i));
    fireEvent.click(screen.getByTestId('turnstile-mock'));
    fireEvent.click(screen.getByRole('button', { name: /confirm & continue/i }));

    expect(onConfirm).toHaveBeenCalledWith('turnstile-token');
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });
});
