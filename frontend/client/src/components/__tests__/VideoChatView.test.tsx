import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { VideoChatView, type VideoChatState } from '../VideoChat';

const entryModalCalls: { onConfirm?: (token: string) => void }[] = [];

vi.mock('../ThemeToggle', () => ({
  ThemeToggle: () => <button type="button">Theme</button>,
}));

vi.mock('../ReportDialog', () => ({
  ReportDialog: ({ peerId }: { peerId: string }) => <div>Report dialog for {peerId}</div>,
}));

vi.mock('../EntryModal', () => ({
  EntryModal: ({ onConfirm }: { onConfirm: (token: string) => void }) => {
    entryModalCalls.push({ onConfirm });
    return <div>Entry modal</div>;
  },
}));

function createVideoState(overrides: Partial<VideoChatState> = {}): VideoChatState {
  return {
    connected: true,
    status: 'idle',
    messages: [],
    peerTyping: false,
    startSearch: vi.fn(),
    stopSearch: vi.fn(),
    skip: vi.fn(),
    disconnect: vi.fn(),
    sendMessage: vi.fn(),
    sendTyping: vi.fn(),
    reportPeerId: null,
    sessionId: 'session-1',
    sessionToken: 'token-1',
    isVideoConnecting: false,
    cameraError: null,
    localVideoRef: { current: null },
    remoteVideoRef: { current: null },
    ...overrides,
  };
}

function renderVideoChat(state: VideoChatState) {
  return render(
    <MemoryRouter>
      <VideoChatView state={state} />
    </MemoryRouter>
  );
}

describe('VideoChatView interactions', () => {
  beforeEach(() => {
    entryModalCalls.length = 0;
    Element.prototype.scrollIntoView = vi.fn();
  });

  it('requires entry confirmation before searching when no turnstile token is present', () => {
    const state = createVideoState({ status: 'idle' });
    renderVideoChat(state);

    fireEvent.click(screen.getByRole('button', { name: /find partner/i }));
    expect(screen.getByText(/entry modal/i)).toBeInTheDocument();

    entryModalCalls[0].onConfirm?.('video-token');
    expect(state.startSearch).toHaveBeenCalledWith('', 'video-token');
  });

  it('requires a second click to skip a connected chat', () => {
    const state = createVideoState({
      status: 'connected',
      messages: [{ id: '1', sender: 'peer', text: 'hello', timestamp: 1 }],
    });

    renderVideoChat(state);
    fireEvent.click(screen.getByRole('button', { name: /^skip$/i }));
    expect(state.skip).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: /sure\?/i })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /sure\?/i }));
    expect(state.skip).toHaveBeenCalledTimes(1);
  });

  it('opens the report dialog for a disconnected chat with a reportable peer', () => {
    const state = createVideoState({
      connected: false,
      status: 'disconnected',
      reportPeerId: 'peer-99',
    });

    renderVideoChat(state);
    fireEvent.click(screen.getByRole('button', { name: /report last chat/i }));

    expect(screen.getByText(/report dialog for peer-99/i)).toBeInTheDocument();
  });

  it('swaps the pip video on click while connected', () => {
    const state = createVideoState({
      status: 'connected',
    });

    renderVideoChat(state);

    const localContainer = screen.getByTestId('local-video-container');
    const remoteContainer = screen.getByTestId('remote-video-container');

    expect(localContainer.className).toContain('z-20');
    expect(remoteContainer.className).toContain('z-0');

    fireEvent.click(localContainer);

    expect(screen.getByTestId('remote-video-container').className).toContain('z-20');
    expect(screen.getByTestId('local-video-container').className).toContain('z-0');
  });
});
