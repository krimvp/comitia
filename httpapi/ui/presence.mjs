export function relativeSeen(value, now = Date.now()) {
  const seen = Date.parse(value);
  if (!Number.isFinite(seen)) return 'Unknown time';
  return relativeAge(Math.max(0, Math.floor((now - seen) / 1000)));
}

function relativeAge(seconds) {
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

export function instanceSeen(instance) {
  const age = instance.seen_seconds_ago;
  const seen = Number.isFinite(age) && age >= 0 ? relativeAge(Math.floor(age)) : relativeSeen(instance.last_seen);
  const recency = instance.recent === true ? ' · Recent activity' : instance.recent === false ? ' · No recent activity' : '';
  return `Last seen ${seen}${recency}`;
}
