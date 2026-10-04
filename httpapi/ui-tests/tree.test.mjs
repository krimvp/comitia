import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {contextTree, readableContexts, taskBoard} from '../ui/tree.mjs';

const epic = {ID:'epic', Name:'Epic', WorkspaceID:'w'};
const granted = {ID:'child-a', Name:'issue-6', WorkspaceID:'w', ParentContextID:'epic', TaskID:'t1', CreatorSessionID:'coord'};
const inherited = {ID:'child-b', Name:'issue-7', WorkspaceID:'w', ParentContextID:'epic', TaskID:'t2', CreatorSessionID:'coord', ParentReadable:true};
const locked = {ID:'child-c', Name:'<img src=x onerror=bad()>', WorkspaceID:'w', ParentContextID:'epic', TaskID:'t3', CreatorSessionID:'coord'};
const grandchild = {ID:'child-b1', Name:'nested', WorkspaceID:'w', ParentContextID:'child-b', ParentReadable:true};
const old = {ID:'old', Name:'Old epic', WorkspaceID:'w', Archived:true, ArchivedAt:'2026-10-01T10:00:00Z'};
const orphan = {ID:'orphan', Name:'Orphan', WorkspaceID:'w', ParentContextID:'hidden-parent'};

test('context tree nests readable children, locks the rest and follows parent_readable chains',()=>{
  const shape = rows => rows.map(r => [r.context.ID, r.depth, r.locked]);
  assert.deepEqual(shape(contextTree([granted, epic, old, orphan])), [['epic',0,false],['child-a',1,false],['old',0,false],['orphan',0,false]]);
  const children = new Map([['epic',[granted, inherited, locked]], ['child-b',[grandchild]], ['child-c',[{ID:'never', ParentContextID:'child-c', ParentReadable:true}]]]);
  assert.deepEqual(shape(contextTree([epic, granted, old], children)), [['epic',0,false],['child-a',1,false],['child-b',1,false],['child-b1',2,false],['child-c',1,true],['old',0,false]]);
  // A parent_readable child of a locked child stays unreachable.
  assert.deepEqual([...readableContexts([epic, granted], children).keys()].sort(), ['child-a','child-b','child-b1','epic']);
  // Children metadata of a context that is not readable, or mislabelled entries, attach nowhere.
  assert.deepEqual(shape(contextTree([old], new Map([['epic',[locked]], ['old',[{...locked, ParentContextID:'epic'}]]]))), [['old',0,false]]);
  // A parent_readable entry listed under the wrong parent grants nothing.
  const misfiled = {ID:'misfiled', Name:'misfiled', ParentContextID:'elsewhere', ParentReadable:true};
  assert.deepEqual([...readableContexts([epic], new Map([['epic',[misfiled]]])).keys()], ['epic']);
  assert.deepEqual(shape(contextTree([epic], new Map([['epic',[misfiled]]]))), [['epic',0,false]]);
});

test('task board groups task-status reports per task with the linked child',()=>{
  const tasks = [{ID:'t1',Title:'Child contexts'},{ID:'t2',Title:'Invites'},{ID:'t3',Title:'Locked task'}];
  const report = (seq, task, state, child) => ({ID:`m${seq}`, ContextID:'epic', Sequence:seq, envelope:{schema:'comitia.task-status.v1', task_id:task, payload:{state, summary:`${state} report`, ...(child ? {child_context_id:child} : {})}}});
  const rows = contextTree([epic, granted], new Map([['epic',[granted, inherited, locked]]]));
  const board = taskBoard('epic', [report(1,'t1','in_progress','child-a'), report(3,'t1','review'), report(2,'t3','blocked','child-c'), report(4,'t9','done','elsewhere'), {ID:'p',Sequence:5,envelope:{schema:'comitia.finding.v1',task_id:'t1',payload:{}}}], tasks, rows);
  assert.deepEqual(board.map(e => [e.taskID, e.latest?.state || null, e.reports, e.child?.context.ID || null, e.child?.locked ?? null]),
    [['t1','review',2,'child-a',false],['t2',null,0,'child-b',false],['t3','blocked',1,'child-c',true],['t9','done',1,null,null]]);
  assert.equal(board[3].childContextID, 'elsewhere');
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
const find = (element, pattern) => element.tag==='button' && pattern.test(text(element)) ? element : element.children.map(c=>find(c,pattern)).find(Boolean);

test('inspector shows the context tree, never requests locked children and marks archived contexts',async()=>{
  const html=await readFile(new URL('../ui/index.html',import.meta.url),'utf8');
  const elements=new Map([...html.matchAll(/id="([^"]+)"/g)].map(m=>[m[1],new Element()]));
  const saved={document:globalThis.document,window:globalThis.window,location:globalThis.location,Option:globalThis.Option,fetch:globalThis.fetch,setTimeout:globalThis.setTimeout,clearTimeout:globalThis.clearTimeout,sessionStorage:globalThis.sessionStorage};
  const storage=new Map();
  globalThis.sessionStorage={getItem:key=>storage.get(key) || null,setItem:(key,value)=>storage.set(key,value),removeItem:key=>storage.delete(key)};
  Object.assign(globalThis,{document:{getElementById:id=>elements.get(id),createElement:tag=>new Element(tag),createElementNS:(_ns,tag)=>new Element(tag)},window:new Element(),location:{href:'http://localhost:8080/ui/'},Option:class extends Element {constructor(label,value){super('option');this.textContent=label;this.value=value;}},setTimeout:()=>0,clearTimeout:()=>{}});
  const listed=[epic, granted, old], all=[epic, granted, inherited, locked, old];
  const at='2026-10-04T10:00:00Z';
  const status=(seq,task,state,child)=>({ID:`s${seq}`,ContextID:'epic',SessionID:'coord',Sequence:seq,Body:'',CreatedAt:at,envelope:{schema:'comitia.task-status.v1',task_id:task,payload:{state,summary:`${task} is ${state}`,child_context_id:child}}});
  const messages={epic:[status(1,'t1','in_progress','child-a'),status(2,'t3','blocked','child-c')],'child-a':[{ID:'a1',ContextID:'child-a',SessionID:'coord',Sequence:1,Body:'Inside issue-6',CreatedAt:at}],'child-b':[],old:[]};
  const calls=[];
  globalThis.fetch=async(url,options)=>{
    const path=new URL(url).pathname,body=JSON.parse(options.body);calls.push({path,body});
    const ok=data=>({ok:true,status:200,json:async()=>data});
    if(path.endsWith('/contexts/list'))return ok({contexts:body.after_id?[]:listed});
    if(path.endsWith('/contexts/get'))return ok({context:all.find(c=>c.ID===body.context_id)});
    // child-b has more children than the inspector pages through.
    if(path.endsWith('/contexts/children')&&body.context_id==='child-b'){
      const start=body.after_id?Number(body.after_id.slice(-5))+1:0;
      return ok({contexts:Array.from({length:body.limit},(_,i)=>({ID:`child-b-${String(start+i).padStart(5,'0')}`,Name:`many ${start+i}`,WorkspaceID:'w',ParentContextID:'child-b'}))});
    }
    if(path.endsWith('/contexts/children'))return body.context_id==='old'?{ok:false,status:503}:ok({contexts:all.filter(c=>c.ParentContextID===body.context_id)});
    if(path.endsWith('/participants/list'))return ok({participants:[{SessionID:'coord',Name:'Coordinator'}]});
    if(path.endsWith('/tasks/list'))return ok({tasks:body.context_id==='epic'?[{ID:'t1',ContextID:'epic',Title:'Child contexts',Status:'leased'},{ID:'t2',ContextID:'epic',Title:'Invites',Status:'open'},{ID:'t3',ContextID:'epic',Title:'Locked task',Status:'leased'}]:[]});
    if(path.endsWith('/messages/replay'))return ok({messages:body.after?[]:messages[body.context_id]});
    throw new Error(`Unexpected endpoint ${path}`);
  };
  try {
    await import(`../ui/app.js?tree-test=${Date.now()}`);
    elements.get('credential').value='observer';
    await elements.get('connect-form').fire('submit');
    assert.equal(elements.get('context-title').textContent,'Epic');
    const rows=elements.get('contexts').children;
    assert.deepEqual(rows.map(r=>[r.title,r.dataset.depth,r.tag]),[['epic','0','button'],['child-a','1','button'],['child-b','1','button'],['child-c','1','div'],['old','0','button']]);
    const lockedRow=rows[3];
    assert.equal(lockedRow.textContent,'<img src=x onerror=bad()>');
    assert.match(text(lockedRow),/Locked · metadata only/);assert.match(text(lockedRow),/Task: Locked task/);assert.match(text(lockedRow),/Created by Coordinator/);
    assert.equal(lockedRow['aria-disabled'],'true');assert.deepEqual(lockedRow.events,{});
    assert.match(text(rows[1]),/Task: Child contexts/);
    assert.match(text(rows[4]),/Archived/);assert.doesNotMatch(text(rows[0]),/Archived/);

    // Polling with unchanged data keeps the sidebar nodes, so keyboard focus survives a tick.
    const board=elements.get('task-board');
    const [firstRow,firstBoard]=[rows[0],board.children[0]];
    await elements.get('load-more').fire('click');
    assert.equal(elements.get('contexts').children[0],firstRow);assert.equal(board.children[0],firstBoard);
    assert.equal(board.hidden,false);
    // Status cards and details never offer to open a locked child.
    await elements.get('show-timeline').fire('click');
    assert.ok(find(elements.get('messages'),/Child context issue-6/));
    assert.ok(!find(elements.get('messages'),/Child context <img/));
    await elements.get('messages').children[1].children[0].fire('click');
    assert.match(text(elements.get('details')),/Child context child-c is not in your loaded readable contexts/);
    assert.ok(!find(elements.get('details'),/child context <img/i));
    await elements.get('show-sequence').fire('click');
    assert.match(text(board),/Child contexts/);assert.match(text(board),/in progress/);assert.match(text(board),/t3 is blocked/);
    assert.match(text(board),/Child context <img src=x onerror=bad\(\)> · locked/);assert.match(text(board),/No status reported/);
    assert.ok(!find(board,/Open child context <img/));
    await find(board,/Locked task/).fire('click');
    assert.equal(elements.get('task-filter').value,'t3');

    await find(board,/Open child context issue-6/).fire('click');
    assert.equal(elements.get('context-title').textContent,'issue-6');
    assert.match(text(elements.get('messages')),/Inside issue-6/);
    assert.equal(board.hidden,true);
    assert.equal(elements.get('contexts').children[1]['aria-current'],'page');
    assert.notEqual(elements.get('contexts').children[0],firstRow);

    await elements.get('contexts').children[2].fire('click');
    assert.equal(elements.get('context-title').textContent,'issue-7');
    const pages=calls.filter(c=>c.path.endsWith('/contexts/children')&&c.body.context_id==='child-b');
    assert.deepEqual(pages.map(c=>c.body.after_id),['','child-b-00999','child-b-01999','child-b-02999','child-b-03999']);
    assert.equal(elements.get('contexts').children.filter(r=>r.title.startsWith('child-b-')).length,5000);
    assert.match(text(elements.get('contexts').children[2]),/More child contexts not shown/);
    assert.doesNotMatch(text(elements.get('contexts').children[0]),/More child contexts not shown/);
    await elements.get('refresh-contexts').fire('click');
    assert.equal(elements.get('context-title').textContent,'issue-7');

    await elements.get('contexts').children.at(-1).fire('click');
    assert.equal(elements.get('context-title').textContent,'Old epic');
    assert.match(elements.get('context-meta').textContent,/Archived/);
    // An unavailable children listing leaves the context open.
    assert.match(elements.get('contexts-note').textContent,/Child contexts are unavailable/);
    assert.equal(elements.get('workspace').hidden,false);

    assert.ok(calls.some(c=>c.path.endsWith('/contexts/children')&&c.body.context_id==='epic'));
    assert.deepEqual(calls.filter(c=>c.body.context_id==='child-c'),[]);
    await elements.get('disconnect').fire('click');
    assert.equal(elements.get('task-board').children.length,0);
  } finally {Object.assign(globalThis,saved);}
});
