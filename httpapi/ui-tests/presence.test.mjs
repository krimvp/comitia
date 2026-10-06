import test from 'node:test';
import assert from 'node:assert/strict';
import {relativeSeen} from '../ui/presence.mjs';

test('instance last seen uses relative time and tolerates clock skew and invalid dates', () => {
  const now = Date.parse('2026-10-04T14:00:00Z');
  for (const [age, expected] of [[0,'0s ago'],[59000,'59s ago'],[60000,'1m ago'],[120000,'2m ago'],[3600000,'1h ago'],[86400000,'1d ago'],[172800000,'2d ago'],[-60000,'0s ago']]) {
    assert.equal(relativeSeen(new Date(now-age).toISOString(),now),expected);
  }
  assert.equal(relativeSeen('invalid',now),'Unknown time');
  assert.equal(relativeSeen(undefined,now),'Unknown time');
});

import {instanceSeen} from '../ui/presence.mjs';
test('instance presence uses server recency despite browser clock skew', () => {
  assert.equal(instanceSeen({last_seen:'2000-01-01T00:00:00Z',seen_seconds_ago:0,recent:true}), 'Last seen 0s ago · Recent activity');
  assert.equal(instanceSeen({last_seen:'2099-01-01T00:00:00Z',seen_seconds_ago:90,recent:false}), 'Last seen 1m ago · No recent activity');
  assert.equal(instanceSeen({seen_seconds_ago:86400,recent:false}), 'Last seen 1d ago · No recent activity');
  assert.equal(instanceSeen({last_seen:'invalid'}), 'Last seen Unknown time');
  assert.equal(instanceSeen({last_seen:'2099-01-01T00:00:00Z',seen_seconds_ago:null}), 'Last seen 0s ago');
});
