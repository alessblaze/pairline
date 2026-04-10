// Pairline - Open Source Video Chat and Matchmaking
// Copyright (C) 2026 Albert Blasczykowski
// Aless Microsystems
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

import { useEffect, useState } from 'react';
import type { ChatMessage } from '../types';
import { useNetworkHealth } from '../hooks/useNetworkHealth';

interface ReportDialogProps {
  peerId: string;
  messages: ChatMessage[];
  reporterSessionId?: string | null;
  reporterToken?: string | null;
  onClose: () => void;
}

export function ReportDialog({ peerId, messages, reporterSessionId, reporterToken, onClose }: ReportDialogProps) {
  const { setApiStatus } = useNetworkHealth();
  const [reason, setReason] = useState('');
  const [description, setDescription] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [submitted, setSubmitted] = useState(false);

  const chatMessages = messages.filter(m => m.sender !== 'system');

  useEffect(() => {
    return () => {
      setApiStatus('ok');
    };
  }, [setApiStatus]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();

    if (!peerId) {
      alert('Missing reported session. Please start a new chat and try again.');
      return;
    }

    if (!reporterSessionId || !reporterToken) {
      alert('Your chat session is no longer valid. Please start a new chat and try again.');
      return;
    }

    setSubmitting(true);
    const controller = new AbortController();
    const timeoutId = window.setTimeout(() => controller.abort(), 10000);

    try {
      const response = await fetch(`${import.meta.env.VITE_API_URL}/api/v1/moderation/report`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        signal: controller.signal,
        body: JSON.stringify({
          reporter_session_id: reporterSessionId,
          reporter_token: reporterToken,
          reported_session_id: peerId,
          reason,
          description,
          chat_log: chatMessages,
        }),
      });

      if (response.ok) {
        setApiStatus('ok');
        setSubmitted(true);
      } else {
        alert('Failed to submit report');
      }
    } catch (error: unknown) {
      console.error('Failed to submit report:', error);
      if (error instanceof TypeError) {
        setApiStatus('degraded');
      }

      if (error instanceof DOMException && error.name === 'AbortError') {
        alert('Report request timed out. Please try again.');
      } else {
        alert('Failed to submit report');
      }
    } finally {
      window.clearTimeout(timeoutId);
      setSubmitting(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black bg-opacity-60 flex items-center justify-center z-50 p-4">
      <div className="bg-white dark:bg-gray-900 rounded-xl shadow-2xl max-w-lg w-full max-h-[90vh] overflow-y-auto">
        <div className="p-6">
          {submitted ? (
            <div className="flex flex-col items-center text-center">
              <div className="mb-4 flex h-14 w-14 items-center justify-center rounded-full bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300">
                <svg className="h-7 w-7" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
                  <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                </svg>
              </div>
              <h2 className="text-xl font-bold text-gray-900 dark:text-white">Report Submitted</h2>
              <p className="mt-2 text-sm leading-6 text-gray-500 dark:text-gray-400">
                Thanks. Your report has been sent to moderation and will be reviewed shortly.
              </p>
              <button
                type="button"
                onClick={onClose}
                className="mt-6 w-full rounded-lg bg-emerald-600 px-4 py-2.5 font-semibold text-white transition-colors hover:bg-emerald-700 dark:bg-emerald-500 dark:hover:bg-emerald-400"
              >
                Close
              </button>
            </div>
          ) : (
            <>
              <h2 className="text-xl font-bold mb-1 text-gray-900 dark:text-white">Report User</h2>
              <p className="text-sm text-gray-500 dark:text-gray-400 mb-4">
                A copy of this chat ({chatMessages.length} message{chatMessages.length !== 1 ? 's' : ''}) will be included with your report.
              </p>

              <form onSubmit={handleSubmit}>
                <div className="mb-4">
                  <label className="block text-sm font-medium mb-1.5 text-gray-700 dark:text-gray-300">Reason</label>
                  <select
                    value={reason}
                    onChange={(e) => setReason(e.target.value)}
                    required
                    className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-red-500"
                  >
                    <option value="">Select a reason</option>
                    <option value="harassment">Harassment</option>
                    <option value="inappropriate_content">Inappropriate Content</option>
                    <option value="spam">Spam</option>
                    <option value="other">Other</option>
                  </select>
                </div>

                <div className="mb-4">
                  <label className="block text-sm font-medium mb-1.5 text-gray-700 dark:text-gray-300">Additional Details</label>
                  <textarea
                    value={description}
                    maxLength={500}
                    onChange={(e) => setDescription(e.target.value)}
                    className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-red-500"
                    rows={3}
                    placeholder="Describe what happened..."
                  />
                  {description.length > 0 && (
                    <div className={`text-center text-xs mt-1.5 font-medium transition-all duration-200 ${
                      description.length >= 400 ? 'text-red-500' : 'text-gray-400 dark:text-gray-500'
                    }`}>
                      {description.length} / 500 characters
                    </div>
                  )}
                </div>

                {chatMessages.length > 0 && (
                  <div className="mb-4">
                    <label className="block text-sm font-medium mb-1.5 text-gray-700 dark:text-gray-300">
                      Chat Transcript (included automatically)
                    </label>
                    <div className="border border-gray-200 dark:border-gray-700 rounded-lg p-3 max-h-40 overflow-y-auto bg-gray-50 dark:bg-gray-800 space-y-1.5">
                      {chatMessages.map((msg) => (
                        <div key={msg.id} className={`flex ${msg.sender === 'me' ? 'justify-end' : 'justify-start'}`}>
                          <span className={`text-xs px-2.5 py-1 rounded-full max-w-[85%] break-words ${
                            msg.sender === 'me'
                              ? 'bg-indigo-100 dark:bg-indigo-900/50 text-indigo-800 dark:text-indigo-200'
                              : 'bg-gray-200 dark:bg-gray-700 text-gray-800 dark:text-gray-200'
                          }`}>
                            <span className="font-semibold mr-1">{msg.sender === 'me' ? 'You' : 'Stranger'}:</span>
                            {msg.text}
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                <div className="flex gap-3 mt-5">
                  <button
                    type="submit"
                    disabled={submitting}
                    className="flex-1 px-4 py-2.5 bg-red-600 hover:bg-red-700 disabled:bg-gray-400 text-white font-semibold rounded-lg transition-colors"
                  >
                    {submitting ? 'Submitting...' : 'Submit Report'}
                  </button>
                  <button
                    type="button"
                    onClick={onClose}
                    className="flex-1 px-4 py-2.5 bg-gray-100 hover:bg-gray-200 dark:bg-gray-800 dark:hover:bg-gray-700 text-gray-700 dark:text-gray-300 font-semibold rounded-lg transition-colors"
                  >
                    Cancel
                  </button>
                </div>
              </form>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
