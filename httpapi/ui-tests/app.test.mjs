import {Element} from './dom-fixture.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';

// A minimal DOM seam for controller tests, not a browser or visual-layout test.
const text = element => [element.textContent,...element.children.map(text)].join(' ');
const settle = async () => {for(let i=0;i<25;i++) await Promise.resolve();};

test('inspector controller uses only read endpoints, rejects stale contexts and clears auth loss',async()=>{
  const html=await readFile(new URL('../ui/index.html',import.meta.url),'utf8');
  const elements=new Map([...html.matchAll(/id="([^"]+)"/g)].map(m=>[m[1],new Element()]));
  const doc={getElementById:id=>elements.get(id),createElement:tag=>new Element(tag),createElementNS:(_ns,tag)=>new Element(tag)};
  let nextTimer=0; const timers=new Map(); const calls=[];
  const storage=new Map();
  const credentialKey='comitia.inspector.credential:http://localhost:8080/v1/';
  const saved={document:globalThis.document,window:globalThis.window,location:globalThis.location,Option:globalThis.Option,fetch:globalThis.fetch,setTimeout:globalThis.setTimeout,clearTimeout:globalThis.clearTimeout,sessionStorage:globalThis.sessionStorage};
  globalThis.sessionStorage={getItem:key=>storage.get(key) || null,setItem:(key,value)=>storage.set(key,value),removeItem:key=>storage.delete(key)};
  Object.assign(globalThis,{document:doc,window:new Element(),location:{href:'http://localhost:8080/ui/'},Option:class extends Element {constructor(label,value){super('option');this.textContent=label;this.value=value;}},setTimeout:(fn,delay)=>{timers.set(++nextTimer,{fn,delay});return nextTimer;},clearTimeout:id=>timers.delete(id)});
  const contexts=[{ID:'a',Name:'Context A',WorkspaceID:'workspace'},{ID:'b',Name:'Context B',WorkspaceID:'workspace'}];
  const msg=(cid,body,seq=1)=>({ID:`${cid}-${seq}`,ContextID:cid,SessionID:'agent',Sequence:seq,Body:body,CreatedAt:'2026-09-10T10:00:00Z',agent_instance:{id:seq===1?'worker':'reviewer',label:seq===1?'<b>Issue worker</b>':'Issue reviewer'},agent_instance_self_reported:true});
  let delayed,expired=false;
  globalThis.fetch=async(url,options)=>{
    const path=new URL(url).pathname,body=JSON.parse(options.body);calls.push({path,body,options});
    assert.equal(options.headers.Authorization,'Bearer test-observer');
    assert.equal(options.credentials,'omit');assert.equal(options.redirect,'error');assert.equal(options.cache,'no-store');
    const ok=data=>({ok:true,status:200,json:async()=>data});
    if(expired) return {ok:false,status:403};
    if(path.endsWith('/contexts/list'))return ok({contexts});
    if(path.endsWith('/contexts/get'))return ok({context:contexts.find(c=>c.ID===body.context_id)});
    if(path.endsWith('/contexts/children'))return ok({contexts:[]});
    if(path.endsWith('/participants/list'))return ok({participants:[{SessionID:'agent',Name:'<script>not executable</script>'}]});
    if(path.endsWith('/instances/list'))return ok({instances:[{id:`${body.context_id}-worker`,label:'<b>Worker</b>',session_id:'agent',active:false,last_seen:new Date(Date.now()-120000).toISOString(),resume:{harness:'codex',pid:123}}]});
    if(path.endsWith('/tasks/list'))return ok({tasks:[]});
    if(path.endsWith('/messages/replay')) {
      if(body.context_id==='a'&&body.after===2) return new Promise(resolve=>{delayed=()=>resolve(ok({messages:[msg('a','stale A result',3)]}));});
      return ok({messages:body.after?[]:[msg(body.context_id,body.context_id==='a'?'<img src=x onerror=bad()>':'Current B message'),msg(body.context_id,'Reviewer reply',2)]});
    }
    throw new Error(`Unexpected endpoint ${path}`);
  };
  try {
    await import(`../ui/app.js?controller-test=${Date.now()}`);
    elements.get('credential').value='test-observer';
    await elements.get('connect-form').fire('submit');
    assert.equal(elements.get('credential').value,'');
    assert.equal(storage.get(credentialKey),'test-observer');
    assert.equal(elements.get('connect-panel').hidden,true);
    assert.match(text(elements.get('messages')),/<img src=x onerror=bad\(\)>/);
    assert.match(text(elements.get('messages')),/<b>Issue worker<\/b> \(worker\) \(self-reported\)/);
    assert.match(text(elements.get('messages')),/Issue reviewer \(reviewer\) \(self-reported\)/);
    assert.match(text(elements.get('sequence-routes')),/Issue worker/);
    assert.match(text(elements.get('sequence-routes')),/Issue reviewer/);
    assert.equal(elements.get('context-title').textContent,'Context A');
    assert.match(text(elements.get('instances')),/<b>Worker<\/b> \(a-worker\)/);
    assert.match(text(elements.get('instances')),/Last seen 2m ago/);
    assert.match(text(elements.get('instances')),/codex/);
    assert.match(text(elements.get('instances')),/Session revoked or expired/);
    assert.match(text(elements.get('instances-note')),/Self-reported/);
    assert.equal(elements.get('sequence-panel').hidden,false);
    assert.equal(elements.get('interaction-panel').hidden,true);
    assert.equal(elements.get('messages').hidden,true);
    assert.match(text(elements.get('sequence-routes')),/Everyone/);
    await elements.get('sequence-routes').children[0].fire('click');
    assert.match(text(elements.get('details')),/Read status is unknown/);
    assert.match(text(elements.get('details')),/Agent instance \(self-reported\)/);
    assert.match(text(elements.get('details')),/authenticated sender is the session/);
    assert.match(text(elements.get('details')),/<img src=x onerror=bad\(\)>/);
    await elements.get('show-timeline').fire('click');
    assert.equal(elements.get('messages').hidden,false);
    assert.equal(elements.get('sequence-panel').hidden,true);
    elements.get('search').value='reviewer';
    await elements.get('search').fire('input');
    assert.match(text(elements.get('messages')),/Reviewer reply/);
    assert.doesNotMatch(text(elements.get('messages')),/<img src=x onerror=bad\(\)>/);
    elements.get('search').value='';
    await elements.get('search').fire('input');
    await elements.get('show-sequence').fire('click');
    const pending=elements.get('load-more').fire('click');await settle();assert.ok(delayed);
    const switchContext=elements.get('contexts').children[1].fire('click');await switchContext;
    assert.equal(elements.get('context-title').textContent,'Context B');
    assert.match(text(elements.get('instances')),/b-worker/);
    assert.doesNotMatch(text(elements.get('instances')),/a-worker/);
    delayed();await pending;
    assert.match(text(elements.get('messages')),/Current B message/);
    assert.doesNotMatch(text(elements.get('messages')),/stale A result/);
    assert.doesNotMatch(text(elements.get('sequence-routes')),/stale A result/);
    assert.ok(calls.every(c=>['/v1/contexts/list','/v1/contexts/get','/v1/contexts/children','/v1/messages/replay','/v1/participants/list','/v1/tasks/list','/v1/instances/list'].includes(c.path)));
    assert.equal(calls.find(c=>c.path.endsWith('/messages/replay')&&c.body.context_id==='b').body.after,0);
    expired=true;await elements.get('load-more').fire('click');
    assert.equal(elements.get('workspace').hidden,true);
    assert.equal(elements.get('connect-panel').hidden,false);
    assert.equal(elements.get('messages').children.length,0);
    assert.equal(elements.get('details').children.length,0);
    for(const id of ['interaction-map','connections','sequence-canvas','sequence-routes','instances'])assert.equal(elements.get(id).children.length,0,id);
    assert.match(elements.get('connect-error').textContent,/Read access/);
    assert.equal(timers.size,0);
    assert.equal(storage.has(credentialKey),false);

    expired=false;
    await elements.get('disconnect').fire('click');
    elements.get('credential').value='test-observer';
    await elements.get('connect-form').fire('submit');
    await globalThis.window.fire('pagehide');
    assert.equal(storage.get(credentialKey),'test-observer');
    // Loading a new controller models refresh while retaining tab storage.
    await import(`../ui/app.js?refresh-test=${Date.now()}`);
    assert.equal(elements.get('connect-panel').hidden,true);
    assert.equal(elements.get('context-title').textContent,'Context A');
    await elements.get('disconnect').fire('click');
    assert.equal(storage.has(credentialKey),false);
    const callsBefore=calls.length;
    await import(`../ui/app.js?disconnected-test=${Date.now()}`);
    assert.equal(calls.length,callsBefore);
    assert.equal(elements.get('connect-panel').hidden,false);
  } finally {Object.assign(globalThis,saved);}
});

test('inspector renders messages when presence is unavailable and recovers independently',async()=>{
  const html=await readFile(new URL('../ui/index.html',import.meta.url),'utf8');
  const elements=new Map([...html.matchAll(/id="([^"]+)"/g)].map(m=>[m[1],new Element()]));
  const saved={document:globalThis.document,window:globalThis.window,location:globalThis.location,Option:globalThis.Option,fetch:globalThis.fetch,setTimeout:globalThis.setTimeout,clearTimeout:globalThis.clearTimeout,sessionStorage:globalThis.sessionStorage};
  Object.assign(globalThis,{document:{getElementById:id=>elements.get(id),createElement:tag=>new Element(tag),createElementNS:(_ns,tag)=>new Element(tag)},window:new Element(),location:{href:'http://localhost:8080/ui/'},Option:class extends Element {constructor(label,value){super('option');this.textContent=label;this.value=value;}},setTimeout:()=>0,clearTimeout:()=>{},sessionStorage:{getItem:()=>null,setItem:()=>{},removeItem:()=>{}}});
  let presence='reject', sequence=0;
  const context={ID:'readable',Name:'Readable context',WorkspaceID:'w'};
  globalThis.fetch=async(url,options)=>{
    const path=new URL(url).pathname;
    const ok=data=>({ok:true,status:200,json:async()=>data});
    if(path.endsWith('/contexts/list'))return ok({contexts:[context]});
    if(path.endsWith('/contexts/get'))return ok({context});
    if(path.endsWith('/participants/list'))return ok({participants:[{SessionID:'agent',Name:'Worker'}]});
    if(path.endsWith('/tasks/list'))return ok({tasks:[]});
    if(path.endsWith('/instances/list')) {
      if(presence==='reject')throw new Error('Presence network failure');
      if(presence==='malformed')return ok({instances:{unexpected:true}});
      if(presence==='malformed-row')return ok({instances:[null]});
      if(presence!=='ready')return {ok:false,status:Number(presence)};
      return ok({instances:[{id:'worker',session_id:'agent',active:true,last_seen:new Date().toISOString()}]});
    }
    if(path.endsWith('/messages/replay')) {
      sequence++;
      return ok({messages:[{ID:`message-${sequence}`,ContextID:context.ID,SessionID:'agent',Sequence:sequence,Body:`Loaded message ${sequence}`,CreatedAt:new Date().toISOString()}]});
    }
    throw new Error(`Unexpected endpoint ${path}`);
  };
  const assertAvailable=()=>{
    assert.equal(elements.get('workspace').hidden,false);
    assert.match(text(elements.get('messages')),new RegExp(`Loaded message ${sequence}`));
    assert.equal(elements.get('timeline-error').textContent,'');
  };
  try {
    await import(`../ui/app.js?presence-failure-test=${Date.now()}`);
    elements.get('credential').value='observer';
    await elements.get('connect-form').fire('submit');
    await elements.get('show-timeline').fire('click');
    assertAvailable();
    assert.match(text(elements.get('instances-note')),/Instances unavailable/);
    for(presence of ['404','503','403','malformed','malformed-row']) {
      await elements.get('load-more').fire('click');
      assertAvailable();
      assert.match(text(elements.get('instances-note')),/Instances unavailable/);
      assert.equal(elements.get('instances').children.length,0);
    }
    presence='ready';
    await elements.get('load-more').fire('click');
    assertAvailable();
    assert.match(text(elements.get('instances')),/worker/);
    assert.doesNotMatch(text(elements.get('instances')),/Session revoked or expired/);
    assert.match(text(elements.get('instances-note')),/Self-reported/);
    presence='reject';
    await elements.get('load-more').fire('click');
    assertAvailable();
    assert.equal(elements.get('instances').children.length,0);
    assert.match(text(elements.get('instances-note')),/Instances unavailable/);
    await elements.get('disconnect').fire('click');
  } finally {Object.assign(globalThis,saved);}
});

test('inspector pages task and participant tails and retains them on refresh',async()=>{
  const html=await readFile(new URL('../ui/index.html',import.meta.url),'utf8');
  const elements=new Map([...html.matchAll(/id="([^"]+)"/g)].map(m=>[m[1],new Element()]));
  const saved={document:globalThis.document,window:globalThis.window,location:globalThis.location,Option:globalThis.Option,fetch:globalThis.fetch,setTimeout:globalThis.setTimeout,clearTimeout:globalThis.clearTimeout,sessionStorage:globalThis.sessionStorage};
  const calls=[],timers=new Map();let timer=0,delayTail=false,resolveTail;
  Object.assign(globalThis,{
    document:{getElementById:id=>elements.get(id),createElement:tag=>new Element(tag),createElementNS:(_ns,tag)=>new Element(tag)},
    window:new Element(),location:{href:'http://localhost:8080/ui/'},
    Option:class extends Element {constructor(label,value){super('option');this.textContent=label;this.value=value;}},
    sessionStorage:{getItem:()=>null,setItem(){},removeItem(){}},
    setTimeout:(fn,delay)=>{timers.set(++timer,{fn,delay});return timer;},clearTimeout:id=>timers.delete(id)
  });
  const contexts=[{ID:'a',Name:'A',WorkspaceID:'w'},{ID:'b',Name:'B',WorkspaceID:'w'}];
  globalThis.fetch=async(url,options)=>{
    const path=new URL(url).pathname,body=JSON.parse(options.body);calls.push({path,body});
    const ok=data=>({ok:true,status:200,json:async()=>data});
    if(path.endsWith('/contexts/list'))return ok({contexts});
    if(path.endsWith('/contexts/get'))return ok({context:contexts.find(c=>c.ID===body.context_id)});
    if(path.endsWith('/contexts/children'))return ok({contexts:[]});
    if(path.endsWith('/messages/replay'))return ok({messages:[]});
    if(path.endsWith('/instances/list'))return ok({instances:[]});
    if(path.endsWith('/tasks/list')){
      assert.equal(body.page,true);
      if(body.context_id==='b')return ok({task_page:{tasks:[],next_after:'',has_more:false}});
      if(body.after_id){
        const result=ok({task_page:{tasks:[{ID:'tail-task',Title:'Tail task',ContextID:'a',Status:'pending'}],next_after:'tail-end',has_more:false}});
        if(delayTail)return new Promise(resolve=>{resolveTail=()=>resolve(result);});
        return result;
      }
      return ok({task_page:{tasks:Array.from({length:1000},(_,i)=>({ID:`t${i}`,Title:`Task ${i}`,ContextID:'a',Status:'pending'})),next_after:'task-cursor',has_more:true}});
    }
    if(path.endsWith('/participants/list')){
      assert.equal(body.page,true);
      if(body.context_id==='b')return ok({participant_page:{participants:[],next_after:'',has_more:false}});
      return body.after_id?ok({participant_page:{participants:[{SessionID:'tail-session',Name:'Tail participant'}],next_after:'tail-session',has_more:false}}):ok({participant_page:{participants:Array.from({length:1000},(_,i)=>({SessionID:`s${i}`,Name:`Participant ${i}`})),next_after:'s999',has_more:true}});
    }
    throw new Error(path);
  };
  try {
    await import(`../ui/app.js?discovery-test=${Date.now()}`);
    elements.get('credential').value='observer';await elements.get('connect-form').fire('submit');
    assert.ok(elements.get('more-tasks'),'task continuation control missing');
    assert.ok(elements.get('more-participants'),'participant continuation control missing');
    assert.equal(elements.get('more-tasks').hidden,false);assert.equal(elements.get('more-participants').hidden,false);
    await elements.get('more-tasks').fire('click');await elements.get('more-participants').fire('click');
    assert.equal(Number(elements.get('task-count').textContent),1001);
    assert.match(text(elements.get('tasks')),/Tail task/);
    assert.ok(elements.get('agent-filter').children.some(c=>c.value==='tail-session'));
    assert.equal(elements.get('more-tasks').hidden,true);assert.equal(elements.get('more-participants').hidden,true);
    assert.ok(calls.some(c=>c.path.endsWith('/tasks/list')&&c.body.after_id==='task-cursor'));
    assert.ok(calls.some(c=>c.path.endsWith('/participants/list')&&c.body.after_id==='s999'));
    await elements.get('load-more').fire('click');
    assert.equal(Number(elements.get('task-count').textContent),1001);
    assert.ok(elements.get('agent-filter').children.some(c=>c.value==='tail-session'));
    // A context reset discards both cursors and rejects an old continuation.
    await elements.get('contexts').children[0].fire('click');await settle();
    delayTail=true;const pending=elements.get('more-tasks').fire('click');await settle();
    await elements.get('contexts').children[1].fire('click');await settle();
    resolveTail();await pending;await settle();
    assert.equal(elements.get('context-title').textContent,'B');assert.equal(Number(elements.get('task-count').textContent),0);
    assert.equal(elements.get('more-tasks').hidden,true);assert.equal(elements.get('more-participants').hidden,true);
  } finally {Object.assign(globalThis,saved);}
});

test('inspector pauses polling in hidden tabs and backs off after failed syncs',async()=>{
  const html=await readFile(new URL('../ui/index.html',import.meta.url),'utf8');
  const elements=new Map([...html.matchAll(/id="([^"]+)"/g)].map(m=>[m[1],new Element()]));
  const saved={document:globalThis.document,window:globalThis.window,location:globalThis.location,Option:globalThis.Option,fetch:globalThis.fetch,setTimeout:globalThis.setTimeout,clearTimeout:globalThis.clearTimeout,sessionStorage:globalThis.sessionStorage};
  const timers=new Map(),docEvents={};let timer=0,failing=false,replays=0;
  const doc={hidden:false,getElementById:id=>elements.get(id),createElement:tag=>new Element(tag),createElementNS:(_ns,tag)=>new Element(tag),addEventListener:(event,fn)=>{docEvents[event]=fn;}};
  Object.assign(globalThis,{
    document:doc,window:new Element(),location:{href:'http://localhost:8080/ui/'},
    Option:class extends Element {constructor(label,value){super('option');this.textContent=label;this.value=value;}},
    sessionStorage:{getItem:()=>null,setItem(){},removeItem(){}},
    setTimeout:(fn,delay)=>{timers.set(++timer,{fn,delay});return timer;},clearTimeout:id=>timers.delete(id)
  });
  const context={ID:'a',Name:'A',WorkspaceID:'w'};
  globalThis.fetch=async url=>{
    const path=new URL(url).pathname;
    const ok=data=>({ok:true,status:200,json:async()=>data});
    if(path.endsWith('/contexts/list'))return ok({contexts:[context]});
    if(path.endsWith('/contexts/get'))return ok({context});
    if(path.endsWith('/contexts/children'))return ok({contexts:[]});
    if(path.endsWith('/messages/replay')){replays++;return failing?{ok:false,status:503}:ok({messages:[]});}
    if(path.endsWith('/participants/list'))return ok({participants:[]});
    if(path.endsWith('/tasks/list'))return ok({tasks:[]});
    if(path.endsWith('/instances/list'))return ok({instances:[]});
    throw new Error(path);
  };
  // Request deadlines are cleared on completion; only the poll timer remains.
  const polls=()=>[...timers].filter(([,t])=>t.delay!==15000);
  const pollDelays=()=>polls().map(([,t])=>t.delay);
  const firePoll=async()=>{const [[id,{fn}]]=polls();timers.delete(id);await fn();await settle();};
  try {
    await import(`../ui/app.js?polling-test=${Date.now()}`);
    elements.get('credential').value='observer';await elements.get('connect-form').fire('submit');await settle();
    assert.deepEqual(pollDelays(),[3000]);
    failing=true;
    for(const delay of [6000,12000,24000,48000,60000,60000]){await firePoll();assert.deepEqual(pollDelays(),[delay]);}
    assert.equal(elements.get('sync-status').textContent,'Connection interrupted');
    failing=false;await firePoll();
    assert.deepEqual(pollDelays(),[3000]);
    doc.hidden=true;docEvents.visibilitychange();
    assert.deepEqual(pollDelays(),[]);
    // A sync finishing while hidden does not reschedule.
    await elements.get('load-more').fire('click');await settle();
    assert.deepEqual(pollDelays(),[]);
    const before=replays;
    doc.hidden=false;docEvents.visibilitychange();await settle();
    assert.equal(replays,before+1);
    assert.deepEqual(pollDelays(),[3000]);
    await elements.get('disconnect').fire('click');
  } finally {Object.assign(globalThis,saved);}
});
