import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {taskStatus, stateLabel, typeLabel} from '../ui/model.mjs';

const status = (payload, extra = {}) => ({ID:'m',Sequence:1,ContextID:'epic',SessionID:'coord',envelope:{schema:'comitia.task-status.v1',task_id:'t1',payload},...extra});

test('task status view reads state, task and only readable direct children',()=>{
  const tasks=[{ID:'t1',Title:'Child contexts'}];
  const contexts=[{ID:'epic',Name:'Epic'},{ID:'child',Name:'issue-6',ParentContextID:'epic'},{ID:'other',Name:'elsewhere',ParentContextID:'x'}];
  assert.equal(taskStatus({ID:'p',envelope:{schema:'comitia.finding.v1',payload:{}}}),null);
  assert.equal(taskStatus({ID:'p'}),null);
  const view=taskStatus(status({state:'blocked',summary:'waiting',child_context_id:'child',blockers:['decision',3],links:['https://x.test/pr/1']}),tasks,contexts);
  assert.deepEqual(view,{state:'blocked',summary:'waiting',taskID:'t1',taskTitle:'Child contexts',childContextID:'child',childContext:contexts[1],blockers:['decision'],links:['https://x.test/pr/1']});
  assert.equal(taskStatus(status({state:'done',summary:'s',child_context_id:'hidden'}),tasks,contexts).childContext,null);
  assert.equal(taskStatus(status({state:'done',summary:'s',child_context_id:'other'}),tasks,contexts).childContext,null);
  const odd=taskStatus(status({state:'<b>',summary:1,blockers:'x'}),[],contexts);
  assert.equal(odd.state,'unknown');assert.equal(odd.summary,'');assert.deepEqual(odd.blockers,[]);assert.equal(odd.taskTitle,'');
  assert.equal(stateLabel('in_progress'),'in progress');assert.equal(stateLabel('<b>'),'unknown state');
  assert.equal(typeLabel('comitia.task-status.v1'),'task status');
});

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
const find = (element, pattern) => element.tag==='button' && pattern.test(element.textContent) ? element : element.children.map(c=>find(c,pattern)).find(Boolean);

test('inspector shows task status and links only a readable child context',async()=>{
  const html=await readFile(new URL('../ui/index.html',import.meta.url),'utf8');
  const elements=new Map([...html.matchAll(/id="([^"]+)"/g)].map(m=>[m[1],new Element()]));
  const saved={document:globalThis.document,window:globalThis.window,location:globalThis.location,Option:globalThis.Option,fetch:globalThis.fetch,setTimeout:globalThis.setTimeout,clearTimeout:globalThis.clearTimeout,sessionStorage:globalThis.sessionStorage};
  const storage=new Map();
  globalThis.sessionStorage={getItem:key=>storage.get(key) || null,setItem:(key,value)=>storage.set(key,value),removeItem:key=>storage.delete(key)};
  Object.assign(globalThis,{document:{getElementById:id=>elements.get(id),createElement:tag=>new Element(tag),createElementNS:(_ns,tag)=>new Element(tag)},window:new Element(),location:{href:'http://localhost:8080/ui/'},Option:class extends Element {constructor(label,value){super('option');this.textContent=label;this.value=value;}},setTimeout:()=>0,clearTimeout:()=>{}});
  const contexts=[{ID:'epic',Name:'Epic',WorkspaceID:'w'},{ID:'child',Name:'issue-6 detail',WorkspaceID:'w',ParentContextID:'epic'}];
  const at='2026-10-04T10:00:00Z';
  const messages={
    epic:[
      {ID:'s1',ContextID:'epic',SessionID:'coord',Sequence:1,Body:'',CreatedAt:at,envelope:{schema:'comitia.task-status.v1',task_id:'t1',payload:{state:'blocked',summary:'Waiting on review',child_context_id:'child',blockers:['Need schema decision'],links:['https://example.test/pr/22']}}},
      {ID:'s2',ContextID:'epic',SessionID:'coord',Sequence:2,Body:'',CreatedAt:at,envelope:{schema:'comitia.task-status.v1',task_id:'t1',payload:{state:'done',summary:'Private child',child_context_id:'private'}}},
    ],
    child:[{ID:'c1',ContextID:'child',SessionID:'coord',Sequence:1,Body:'Detail inside the child',CreatedAt:at}],
  };
  globalThis.fetch=async(url,options)=>{
    const path=new URL(url).pathname,body=JSON.parse(options.body);
    const ok=data=>({ok:true,status:200,json:async()=>data});
    if(path.endsWith('/contexts/list'))return ok({contexts:body.after_id?[]:contexts});
    if(path.endsWith('/contexts/get'))return ok({context:contexts.find(c=>c.ID===body.context_id)});
    if(path.endsWith('/contexts/children'))return ok({contexts:contexts.filter(c=>c.ParentContextID===body.context_id)});
    if(path.endsWith('/participants/list'))return ok({participants:[{SessionID:'coord',Name:'Coordinator'}]});
    if(path.endsWith('/tasks/list'))return ok({tasks:body.context_id==='epic'?[{ID:'t1',ContextID:'epic',Title:'Child contexts',Status:'leased'}]:[]});
    if(path.endsWith('/messages/replay'))return ok({messages:body.after?[]:messages[body.context_id]});
    throw new Error(`Unexpected endpoint ${path}`);
  };
  try {
    await import(`../ui/app.js?task-status-test=${Date.now()}`);
    elements.get('credential').value='observer';
    await elements.get('connect-form').fire('submit');
    assert.equal(elements.get('context-title').textContent,'Epic');
    await elements.get('show-timeline').fire('click');
    const list=text(elements.get('messages'));
    assert.match(list,/task status/);assert.match(list,/blocked/);assert.match(list,/done/);
    assert.match(list,/Waiting on review/);assert.match(list,/Child contexts/);
    assert.ok(find(elements.get('messages'),/Child context issue-6 detail/));
    assert.ok(!find(elements.get('messages'),/private/));
    await elements.get('messages').children[1].children[0].fire('click');
    const hidden=text(elements.get('details'));
    assert.match(hidden,/Child context private is not in your loaded readable contexts/);
    assert.ok(!find(elements.get('details'),/Open child context/));
    await elements.get('messages').children[0].children[0].fire('click');
    const details=text(elements.get('details'));
    assert.match(details,/Task status/);assert.match(details,/Need schema decision/);assert.match(details,/https:\/\/example.test\/pr\/22/);
    await find(elements.get('details'),/Open child context issue-6 detail/).fire('click');
    assert.equal(elements.get('context-title').textContent,'issue-6 detail');
    assert.match(text(elements.get('messages')),/Detail inside the child/);
    await elements.get('disconnect').fire('click');
  } finally {Object.assign(globalThis,saved);}
});
