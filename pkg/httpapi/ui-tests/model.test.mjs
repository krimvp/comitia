import test from 'node:test';
import assert from 'node:assert/strict';
import {ConversationState,filterMessages,threadIDs,pollDelay} from '../ui/model.mjs';
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

test('self-reported instances split session nodes and route instance recipients',async()=>{
  const {interactionGraph,recipientActors,instanceClaims,BROADCAST}=await import('../ui/model.mjs');
  const claim=id=>({agent_instance:{id},agent_instance_self_reported:true});
  const messages=[
    {...message('w1',1,{recipient_session_ids:['orch'],recipient_instances:['lead']}),SessionID:'codex',...claim('worker')},
    {...message('o1',2,{recipient_instances:['worker']}),SessionID:'orch',...claim('lead')},
    {...message('o2',3,{recipient_session_ids:['codex']}),SessionID:'orch'},
    {...message('o3',4,{recipient_instances:['ghost']}),SessionID:'orch',...claim('lead')},
    {...message('w2',5),SessionID:'codex',...claim('worker')}
  ];
  const graph=interactionGraph(messages,[{SessionID:'codex',Name:'codex'},{SessionID:'orch',Name:'claude'}]);
  // codex only ever spoke as an instance but still received a session-addressed message.
  assert.deepEqual(graph.nodes.map(n=>n.id).sort(),['#ghost',BROADCAST,'codex','codex#worker','orch','orch#lead']);
  const worker=graph.nodes.find(n=>n.id==='codex#worker');
  assert.deepEqual([worker.session,worker.instance,worker.name,worker.sent,worker.received],['codex','worker','codex',2,1]);
  assert.equal(graph.edges.find(e=>e.source==='codex#worker'&&e.target==='orch#lead').count,1);
  assert.equal(graph.edges.find(e=>e.source==='orch#lead'&&e.target==='codex#worker').count,1);
  assert.equal(graph.edges.find(e=>e.source==='orch'&&e.target==='codex').count,1);
  assert.equal(graph.nodes.find(n=>n.id==='#ghost').name,'Session');
  assert.deepEqual(recipientActors(messages[1],instanceClaims(messages)),['codex#worker']);
  assert.deepEqual(filterMessages(messages,{agent:'codex#worker'}).map(m=>m.ID),['w1','o1','w2']);
  assert.deepEqual(filterMessages(messages,{agent:'codex'}).map(m=>m.ID),['w1','o2','w2']);
  assert.deepEqual(filterMessages(messages,{from:'orch#lead',to:'codex#worker'}).map(m=>m.ID),['o1']);
  assert.deepEqual(filterMessages(messages,{to:BROADCAST}).map(m=>m.ID),['w2']);
  // Without any instance participants, an idle participant still appears.
  assert.ok(interactionGraph([],[{SessionID:'idle',Name:'idle'}]).nodes.some(n=>n.id==='idle'));
});

test('poll delay doubles after consecutive failures up to one minute', () => {
  assert.deepEqual([0,1,2,3,4,5,6,50,-1].map(pollDelay),[3000,6000,12000,24000,48000,60000,60000,60000,3000]);
});
