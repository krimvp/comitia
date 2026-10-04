import test from 'node:test';
import assert from 'node:assert/strict';
import {taskMarker, taskEvent} from '../ui/task-outcomes.mjs';
import {taskStatus, stateLabel} from '../ui/model.mjs';
import {normalizeResponse} from '../ui/wire.mjs';

test('terminal task outcomes are distinct from pending and advisory reports', () => {
  assert.equal(taskMarker('pending'), '□');
  assert.equal(taskMarker('completed'), '✓');
  assert.equal(taskMarker('failed'), '✕');
  assert.equal(taskMarker('cancelled'), '⊘');
  assert.equal(taskMarker('other'), '?');
  for (const state of ['failed', 'cancelled']) {
    assert.equal(stateLabel(state), state);
    const envelope = {schema:'comitia.task-status.v1',task_id:'t',payload:{state,summary:'reported'}};
    assert.equal(taskStatus({envelope}).state, state);
    assert.equal(taskEvent({envelope}), null);
    const event = {schema:'comitia.task-event.v1',task_id:'t',payload:{status:state,reason:'stopped'}};
    assert.equal(taskEvent({envelope:event}), `Task ${state}: stopped`);
  }
  assert.equal(taskEvent({envelope:{schema:'comitia.task-event.v1',payload:{status:'<b>',reason:1}}}), 'Task unknown');
});

test('both task wire forms preserve terminal reason', () => {
  for (const task of [{id:'t',status:'failed',outcome_reason:'check failed'}, {ID:'t',Status:'failed',outcome_reason:'check failed'}]) {
    const result = normalizeResponse({tasks:[task]}).tasks[0];
    assert.equal(result.Status, 'failed');
    assert.equal(result.outcome_reason, 'check failed');
  }
});
