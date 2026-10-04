import test from 'node:test';
import assert from 'node:assert/strict';
import {normalizeResponse} from '../ui/wire.mjs';

test('inspector accepts canonical and legacy records without changing opaque payloads', () => {
  const envelope = {schema:'comitia.discussion.v1', task_id:'task', payload:{context_id:'opaque', Body:'opaque'}};
  const result = normalizeResponse({contexts:[{id:'c', workspace_id:'w', name:'Context', parent_readable:false}], context:{id:'c', archived:true}, participants:[{session_id:'s', principal_id:'p', name:'Sender'}], tasks:[{id:'t', lease_owner_instance:{id:'worker'}, lease_version:2}], messages:[{id:'m', sequence:1, body:'Hello', envelope, agent_instance:{id:'worker'}}], page:{messages:[{id:'m', body:'Hello', envelope}], next_after:1}, instances:[{id:'worker', resume:{context_id:'opaque'}}]});
  assert.equal(result.contexts[0].ID,'c'); assert.equal(result.contexts[0].WorkspaceID,'w');
  assert.equal(result.context.Archived,true); assert.equal(result.participants[0].SessionID,'s');
  assert.equal(result.tasks[0].LeaseVersion,2); assert.equal(result.tasks[0].lease_owner_instance.id,'worker');
  assert.equal(result.messages[0].Body,'Hello'); assert.equal(result.page.messages[0].ID,'m');
  assert.equal(result.messages[0].envelope,envelope); assert.equal(result.instances[0].resume.context_id,'opaque');
  const legacy = {contexts:[{ID:'c',Name:'Context'}], messages:[{ID:'m',Body:'Hello',envelope}]};
  assert.deepEqual(normalizeResponse(legacy),legacy);
  for (const old of [{}, {messages:null}, {messages:[]}]) assert.deepEqual(normalizeResponse(old),old);
});

test('inspector discovery pages accept canonical and legacy records', () => {
  const result = normalizeResponse({task_page:{tasks:[{id:'t', title:'Task'}], next_after:'cursor', has_more:true}, participant_page:{participants:[{session_id:'s', name:'Agent'}], next_after:'s', has_more:false}});
  assert.equal(result.task_page.tasks[0].ID,'t'); assert.equal(result.task_page.tasks[0].Title,'Task');
  assert.equal(result.task_page.next_after,'cursor'); assert.equal(result.task_page.has_more,true);
  assert.equal(result.participant_page.participants[0].SessionID,'s'); assert.equal(result.participant_page.next_after,'s');
  const legacy = {task_page:{tasks:[{ID:'t',Title:'Task'}], next_after:'cursor', has_more:false}, participant_page:{participants:[{SessionID:'s'}], next_after:'s', has_more:false}};
  assert.deepEqual(normalizeResponse(legacy),legacy);
});
