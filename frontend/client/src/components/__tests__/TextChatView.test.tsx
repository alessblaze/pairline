import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { TextChatView, type TextChatState } from '../TextChat';

const entryModalCalls: { onConfirm?: (token: string) => void; onClose?: () => void }[] = [];

vi.mock('../ThemeToggle', () => ({
  ThemeToggle: () => <button type="button">Theme</button>,
}));

vi.mock('../ReportDialog', () => ({
  ReportDialog: ({ peerId, onClose }: { peerId: string; onClose: () => void }) => (
    <div>
      <span>Report dialog for {peerId}</span>
      <button type="button" onClick={onClose}>Close report</button>
    </div>
  ),
}));

vi.mock('../EntryModal', () => ({
  EntryModal: ({ onConfirm, onClose }: { onConfirm: (token: string) => void; onClose: () => void }) => {
    entryModalCalls.push({ onConfirm, onClose });
    return <div>Entry modal</div>;
  },
}));

function renderTextChat(state: TextChatState) {
  return render(
    <MemoryRouter>
      <TextChatView state={state} />
    </MemoryRouter>
  );
}

function createState(overrides: Partial<TextChatState> = {}): TextChatState {
  return {
    connected: true,
    status: 'idle',
    messages: [],
    peerTyping: false,
    startSearch: vi.fn(),
    stopSearch: vi.fn(),
    disconnect: vi.fn(),
    sendMessage: vi.fn(),
    sendTyping: vi.fn(),
    reportPeerId: null,
    sessionId: 'session-1',
    sessionToken: 'token-1',
    ...overrides,
  };
}

describe('TextChatView interactions', () => {
  beforeEach(() => {
    entryModalCalls.length = 0;
    Element.prototype.scrollIntoView = vi.fn();
  });

  it('requires entry confirmation before starting a search without a turnstile token', () => {
    const state = createState({ status: 'idle' });
    renderTextChat(state);

    fireEvent.click(screen.getByRole('button', { name: /find a stranger/i }));
    expect(screen.getByText(/entry modal/i)).toBeInTheDocument();

    entryModalCalls[0].onConfirm?.('token-from-modal');
    expect(state.startSearch).toHaveBeenCalledWith('', 'token-from-modal');
  });

  it('opens the report dialog for the last chat in disconnected state', () => {
    const state = createState({
      connected: false,
      status: 'disconnected',
      reportPeerId: 'peer-42',
    });

    renderTextChat(state);
    fireEvent.click(screen.getByRole('button', { name: /report last chat/i }));

    expect(screen.getByText(/report dialog for peer-42/i)).toBeInTheDocument();
  });

  it('requires a second stop click before disconnecting', () => {
    const state = createState({
      status: 'connected',
      messages: [{ id: '1', sender: 'peer', text: 'hello', timestamp: 1 }],
      reportPeerId: 'peer-42',
    });

    renderTextChat(state);
    const stopButton = screen.getByRole('button', { name: /stop chat/i });

    fireEvent.click(stopButton);
    expect(state.disconnect).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: /tap again to stop/i })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /tap again to stop/i }));
    expect(state.disconnect).toHaveBeenCalledTimes(1);
  });
});
