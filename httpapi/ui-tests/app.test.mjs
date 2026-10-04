import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';

// A minimal DOM seam for controller tests, not a browser or visual-layout test.
class Element {
  constructor(tag='div') {this.tag=tag;this.children=[];this.events={};this.dataset={};this.value='';this.hidden=false;this.textContent='';}
  append(...children) {this.children.push(...children);}
  replaceChildren(...children) {this.children=children;}
  add(child) {this.children.push(child);}
  addEventListener(event, fn) {this.events[event]=fn;}
  setAttribute(name,value) {this[name]=value;}
  focus() {}
  scrollIntoView() {}
  querySelector() {return this.children.find(c=>c.tag==='button');}
  async fire(event) {return this.events[event]?.({preventDefault(){}});}
}
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
    if(path.endsWith('/instances/list'))return ok({instances:[{id:`${body.context_id}-worker`,label:'<b>Worker</b>',session_id:'agent',last_seen:new Date(Date.now()-120000).toISOString(),resume:{harness:'codex',pid:123}}]});
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
      return ok({instances:[{id:'worker',session_id:'agent',last_seen:new Date().toISOString()}]});
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
    assert.match(text(elements.get('instances-note')),/Self-reported/);
    presence='reject';
    await elements.get('load-more').fire('click');
    assertAvailable();
    assert.equal(elements.get('instances').children.length,0);
    assert.match(text(elements.get('instances-note')),/Instances unavailable/);
    await elements.get('disconnect').fire('click');
  } finally {Object.assign(globalThis,saved);}
});
