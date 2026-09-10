import test from 'node:test';
import assert from 'node:assert/strict';
import {ConversationState,filterMessages,threadIDs} from '../ui/model.mjs';
const message = (ID, Sequence, envelope = {}) => ({ID,Sequence,ContextID:'a',SessionID:'sender',Body:`Message ${ID}`,envelope});

test('context switch and reconnect discard stale responses; dedupe preserves sequence',()=>{
  const state = new ConversationState();
  const first = state.reset('a');
  state.append(first,[message('two',2),message('one',1)]);
  state.append(first,[message('two',2),message('three',3)]);
  assert.deepEqual(state.messages.map(m=>m.Sequence),[1,2,3]);
  assert.equal(state.after,3);
  const second = state.reset('b');
  assert.equal(state.append(first,[message('late',4)]),false);
  assert.equal(state.after,0); assert.deepEqual(state.messages,[]);
  assert.throws(()=>state.append(second,[message('wrong-context',5)]),/Invalid/);
  const third = state.reset('a',200);
  assert.equal(state.append(first,[message('old-session',100)]),false);
  state.append(third,[message('new-window',201)]);assert.equal(state.after,201);
});

test('filters combine sender/recipient, schema, task, text and full reply thread',()=>{
  const messages=[message('root',1,{schema:'comitia.assignment.v1',task_id:'task',recipient_session_ids:['reviewer']}),message('reply',2,{schema:'comitia.finding.v1',task_id:'task',reply_to:'root',payload:{summary:'Evidence'}}),message('leaf',3,{reply_to:'reply'}),message('other',4,{task_id:'other'})];
  assert.deepEqual(filterMessages(messages,{agent:'reviewer'}).map(m=>m.ID),['root']);
  assert.deepEqual(filterMessages(messages,{query:'evidence',task:'task',type:'comitia.finding.v1'}).map(m=>m.ID),['reply']);
  assert.deepEqual([...threadIDs(messages,'reply')],['root','reply','leaf']);
  assert.deepEqual(filterMessages(messages,{thread:'leaf'}).map(m=>m.ID),['root','reply','leaf']);
  assert.equal(filterMessages(messages,{agent:'absent'}).length,0);
});

test('invalid page is atomic and cannot corrupt a replay cursor',()=>{
  const state=new ConversationState(),generation=state.reset('a');
  state.append(generation,[message('one',1)]);
  assert.throws(()=>state.append(generation,[message('valid',2),message('bad',NaN)]));
  assert.equal(state.after,1);assert.equal(state.messages.length,1);
});

test('interaction graph preserves direction, broadcasts, duplicate names and multi-recipient counts',async()=>{
  const {interactionGraph,BROADCAST}=await import('../ui/model.mjs');
  const messages=[
    {...message('a1',1,{recipient_session_ids:['b','c']}),SessionID:'a'},
    {...message('b1',2,{recipient_session_ids:['a']}),SessionID:'b'},
    {...message('a2',3),SessionID:'a'},
    {...message('a3',4,{recipient_session_ids:['a']}),SessionID:'a'}
  ];
  const graph=interactionGraph(messages,[{SessionID:'a',Name:'Reviewer'},{SessionID:'b',Name:'Reviewer'}]);
  assert.equal(graph.messageCount,4);assert.equal(graph.nodes.length,4);
  assert.equal(graph.nodes.find(n=>n.id==='a').sent,3);
  assert.equal(graph.edges.find(e=>e.source==='a'&&e.target==='b').count,1);
  assert.equal(graph.edges.find(e=>e.source==='b'&&e.target==='a').count,1);
  assert.equal(graph.edges.find(e=>e.target===BROADCAST).count,1);
  assert.equal(graph.nodes.find(n=>n.id==='c').name,'Session');
  assert.deepEqual(filterMessages(messages,{from:'a',to:'b'}).map(m=>m.ID),['a1']);
  assert.deepEqual(filterMessages(messages,{to:BROADCAST}).map(m=>m.ID),['a2']);
  assert.deepEqual(filterMessages(messages,{from:'a',to:'a'}).map(m=>m.ID),['a3']);
});
