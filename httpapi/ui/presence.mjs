export function relativeSeen(value, now = Date.now()) {
  const seen = Date.parse(value);
  if (!Number.isFinite(seen)) return 'Unknown time';
  const seconds = Math.max(0, Math.floor((now - seen) / 1000));
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}
