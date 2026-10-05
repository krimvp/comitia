export const TASK_EVENT = 'comitia.task-event.v1';

export function taskMarker(status) {
  return {pending:'□', leased:'◷', completed:'✓', failed:'✕', cancelled:'⊘'}[status] || '?';
}

export function taskEvent(message) {
  if (message.envelope?.schema !== TASK_EVENT) return null;
  const payload = message.envelope.payload || {};
  const status = ['pending', 'leased', 'completed', 'failed', 'cancelled'].includes(payload.status) ? payload.status : 'unknown';
  const reason = typeof payload.reason === 'string' ? payload.reason : '';
  return `Task ${status}${reason ? `: ${reason}` : ''}`;
}
