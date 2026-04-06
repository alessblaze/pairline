import { useState } from 'react';
import type { ChatMessage } from '../types';

interface ReportDialogProps {
  peerId: string;
  messages: ChatMessage[];
  reporterSessionId?: string | null;
  reporterToken?: string | null;
  onClose: () => void;
}

export function ReportDialog({ peerId, messages, reporterSessionId, reporterToken, onClose }: ReportDialogProps) {
  const [reason, setReason] = useState('');
  const [description, setDescription] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const chatMessages = messages.filter(m => m.sender !== 'system');

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
        alert('Report submitted successfully');
        onClose();
      } else {
        alert('Failed to submit report');
      }
    } catch (error) {
      console.error('Failed to submit report:', error);
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
        </div>
      </div>
    </div>
  );
}
