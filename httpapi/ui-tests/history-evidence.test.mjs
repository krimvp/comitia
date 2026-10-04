import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {Element} from './dom-fixture.mjs';
import {HistoryWindows, HISTORY_LIMIT, messagePreview, recipientLabel} from '../ui/model.mjs';

const text = element => element.textContent;
const context = {ID:'context',Name:'History',WorkspaceID:'workspace'};
const message = Sequence => ({ID:`m${Sequence}`,Sequence,ContextID:context.ID,SessionID:'worker',Body:`Message ${Sequence}`,CreatedAt:new Date(1700000000000+Sequence*1000).toISOString(),envelope:{recipient_session_ids:['reviewer']}});
async function controller(t, replay) {
  const html=await readFile(new URL('../ui/index.html',import.meta.url),'utf8');
  const elements=new Map([...html.matchAll(/id="([^"]+)"/g)].map(m=>[m[1],new Element()]));
  const names=['document','window','location','Option','fetch','setTimeout','clearTimeout','sessionStorage'];
  const saved=Object.fromEntries(names.map(name=>[name,globalThis[name]]));
  t.after(()=>Object.assign(globalThis,saved));
  const calls=[];
  Object.assign(globalThis,{
    document:{getElementById:id=>elements.get(id),createElement:tag=>new Element(tag),createElementNS:(_ns,tag)=>new Element(tag)},
    window:new Element(),location:{href:'http://localhost:8080/ui/'},
    Option:class extends Element {constructor(label,value){super('option');this.textContent=label;this.value=value;}},
    setTimeout:()=>0,clearTimeout(){},sessionStorage:{getItem:()=>null,setItem(){},removeItem(){}},
    fetch:async(url,options)=>{
      const path=new URL(url).pathname,body=JSON.parse(options.body);calls.push({path,body});
      let result;
      if(path.endsWith('/contexts/list'))result={contexts:[context]};
      else if(path.endsWith('/contexts/get'))result={context};
      else if(path.endsWith('/contexts/children'))result={contexts:[]};
      else if(path.endsWith('/participants/list'))result={participants:[{SessionID:'worker',Name:'Worker'},{SessionID:'reviewer',Name:'Reviewer'}]};
      else if(path.endsWith('/tasks/list'))result={tasks:[]};
      else if(path.endsWith('/instances/list'))result={instances:[{id:'worker',session_id:'worker',active:true,last_seen:new Date().toISOString(),resume:{checkpoint:'ready'}}]};
      else if(path.endsWith('/messages/replay'))result=await replay(body);
      else throw new Error(path);
      return {ok:true,status:200,json:async()=>result};
    }
  });
  elements.get('sequence-canvas').clientWidth=420;
  await import(`../ui/app.js?history-evidence=${Date.now()}-${Math.random()}`);
  elements.get('credential').value='observer';await elements.get('connect-form').fire('submit');
  return {elements,calls};
}

test('controller navigates beyond 5,000 and back, jumps, and advances sparse scanned cursors',async t=>{
  let sparse=false;
  const {elements:e,calls}=await controller(t,body=>{
    assert.equal(body.page,true);assert.ok(body.limit<=100);
    const rows=sparse?(body.after===6999?[message(7010)]:[]):Array.from({length:Math.min(body.limit,Math.max(0,5001-body.after))},(_,i)=>message(body.after+i+1));
    return {page:{messages:rows,scanned_through:sparse?8000:rows.at(-1)?.Sequence || body.after,has_more:!sparse&&body.after+rows.length<5001}};
  });
  for(let i=1;i<50;i++)await e.get('load-more').fire('click');
  assert.match(text(e.get('message-count')),/5000 loaded/);
  assert.equal(e.get('messages').children.length,200);
  assert.equal(e.get('next-window').hidden,false);
  await e.get('next-window').fire('click');
  assert.match(text(e.get('messages')),/Message 5001/);
  assert.match(text(e.get('message-count')),/1 loaded/);
  assert.equal(e.get('previous-window').hidden,false);
  await e.get('previous-window').fire('click');
  assert.match(text(e.get('messages')),/Message 1/);
  assert.doesNotMatch(text(e.get('messages')),/Message 5001/);
  // The saved forward bookmark remains directly reachable after only one batch.
  await e.get('next-window').fire('click');
  assert.match(text(e.get('messages')),/Message 5001/);
  e.get('jump-sequence').value='4500';await e.get('jump-history').fire('submit');
  assert.match(text(e.get('messages')),/Message 4500/);
  assert.equal(calls.filter(c=>c.path.endsWith('/messages/replay')).at(-1).body.after,4499);
  const before=calls.length;e.get('jump-sequence').value='9007199254740992';await e.get('jump-history').fire('submit');
  assert.equal(calls.length,before);assert.match(text(e.get('timeline-error')),/safe sequence/);
  sparse=true;e.get('jump-sequence').value='7000';await e.get('jump-history').fire('submit');await e.get('load-more').fire('click');
  assert.equal(calls.filter(c=>c.path.endsWith('/messages/replay')).at(-1).body.after,8000);
  await e.get('disconnect').fire('click');
});

test('cursor-only history stays bounded and still reaches the beginning after eviction',()=>{
  const history=new HistoryWindows();
  for(let i=1;i<=HISTORY_LIMIT+10;i++)history.next(i*5000);
  assert.equal(history.frames.length,HISTORY_LIMIT);
  assert.ok(history.frames.every(frame=>Object.keys(frame).sort().join(',')==='after,end'));
  for(let i=1;i<HISTORY_LIMIT;i++)history.previous();
  const first=history.current.after;
  assert.ok(first>0);history.previous();assert.deepEqual(history.current,{after:0,end:first});
  history.reset(9000);assert.deepEqual(history.previous(),{after:0,end:9000});
});

test('controller renders both recipient lists and safe known-schema previews in list, flow and details',async t=>{
  const reason='<img src=x onerror=alert(1)> Cannot proceed';
  const rows=[
    {...message(1),Body:'',envelope:{schema:'comitia.blocker.v1',recipient_instances:['only-worker'],payload:{reason,needs:['review']}}},
    {...message(2),Body:'',envelope:{schema:'comitia.verification-result.v1',recipient_session_ids:['reviewer'],recipient_instances:['review-process'],payload:{status:'not_run',reason:'Missing fixture'}}},
    {...message(3),Body:'',envelope:{schema:'extension.unknown.v1',payload:{summary:'Untrusted extension summary'}}},
    {...message(4),envelope:{}},
  ];
  const {elements:e}=await controller(t,()=>({messages:rows}));
  assert.match(text(e.get('messages')),new RegExp('Cannot proceed'));
  const first=e.get('messages').children[0];
  assert.match(text(first),/Instances \(self-reported\): only-worker/);assert.doesNotMatch(text(first),/everyone/i);
  assert.match(text(e.get('messages').children[1]),/Sessions: Reviewer.*Instances \(self-reported\): review-process/);
  assert.match(text(e.get('messages').children[3]),/Everyone in this context/);
  assert.doesNotMatch(text(e.get('messages')),/Untrusted extension summary/);
  for(let i=0;i<3;i++)await e.get('sequence-previous-arrows').fire('click');
  assert.match(text(e.get('sequence-routes')),/Cannot proceed/);
  assert.ok(e.get('sequence-routes').children.length<=1);
  await first.children[0].fire('click');
  assert.match(text(e.get('details')),/Cannot proceed/);assert.match(text(e.get('details')),/only-worker/);assert.doesNotMatch(text(e.get('details')),/Everyone/);
  assert.match(text(e.get('details')),/Structured payload/);
  const all=root=>[root,...root.children.flatMap(all)];
  assert.equal(all(e.get('details')).some(el=>el.tag==='img'),false);
  assert.equal(all(e.get('sequence-canvas')).filter(el=>el.className==='sequence-labels').length,1);
  await e.get('disconnect').fire('click');
});

test('controller width-sized flow pages freeze during browsing and explicitly follow new lanes',async t=>{
  let rows=Array.from({length:100},(_,i)=>message(i+1));
  const {elements:e}=await controller(t,body=>({messages:rows.filter(m=>m.Sequence>body.after).slice(0,body.limit)}));
  assert.equal(e.get('sequence-routes').children.length,1);
  assert.match(text(e.get('sequence-routes')),/#100/);
  await e.get('sequence-previous-arrows').fire('click');
  assert.match(text(e.get('sequence-routes')),/#99/);
  assert.equal(e.get('sequence-follow').getAttribute('aria-pressed'),'false');
  rows.push({...message(101),SessionID:'zz-new-lane'});
  await e.get('load-more').fire('click');assert.match(text(e.get('sequence-routes')),/#99/);
  await e.get('sequence-follow').fire('click');assert.match(text(e.get('sequence-routes')),/#101/);
  assert.equal(e.get('sequence-follow').getAttribute('aria-pressed'),'true');
  await e.get('disconnect').fire('click');
});

test('preview and recipient formatters tolerate missing and non-string payload fields',()=>{
  assert.equal(messagePreview({envelope:{schema:'comitia.blocker.v1',payload:{reason:{html:'bad'}}}}),'Structured message');
  assert.equal(messagePreview({envelope:{schema:'comitia.verification-result.v1',payload:{status:'failed',reason:'Evidence missing'}}}),'failed · Evidence missing');
  assert.equal(messagePreview({Body:'Body wins',envelope:{schema:'comitia.blocker.v1',payload:{reason:'hidden'}}}),'Body wins');
  assert.equal(recipientLabel({recipient_session_ids:[],recipient_instances:[]}), 'Everyone in this context');
});

test('controller presence uses sidebar text tokens and readable diagram metadata',async t=>{
  const {elements:e}=await controller(t,()=>({messages:[message(1)]}));
  assert.match(text(e.get('instances')),/Last seen/);
  const css=await readFile(new URL('../ui/style.css',import.meta.url),'utf8');
  const sidebar=[...css.matchAll(/\.sidebar\{([^}]+)\}/g)].at(-1)[1];
  const token=name=>sidebar.match(new RegExp(`--${name}:(#[a-f0-9]{6})`))?.[1];
  const metadata=[...css.matchAll(/\.sequence-id,\.sequence-time\{([^}]+)\}/g)].at(-1)[1];
  const lum=hex=>{
    assert.match(hex || '',/^#[a-f0-9]{6}$/);
    const [r,g,b]=hex.slice(1).match(/../g).map(v=>parseInt(v,16)/255).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4);
    return .2126*r+.7152*g+.0722*b;
  };
  const contrast=(a,b)=>(Math.max(lum(a),lum(b))+.05)/(Math.min(lum(a),lum(b))+.05);
  const background=css.match(/--ink:(#[a-f0-9]{6})/)[1];
  assert.ok(contrast(token('muted'),background)>=4.5);assert.ok(contrast(token('blue'),background)>=4.5);
  assert.ok(contrast(metadata.match(/fill:(#[a-f0-9]{6})/)[1],'#f8faff')>=4.5);
  assert.ok(Number(metadata.match(/font-size:(\d+)px/)[1])>=12);
  await e.get('disconnect').fire('click');
});

test('controller recipient-only routes are never labelled Everyone in cards or details',async t=>{
  const rows=[
    {...message(1),envelope:{recipient_instances:['instance-only']}},
    {...message(2),envelope:{recipient_session_ids:['reviewer'],recipient_instances:['review-worker']}},
    {...message(3),envelope:{recipient_session_ids:[],recipient_instances:[]}},
  ];
  const {elements:e}=await controller(t,()=>({messages:rows}));
  const first=e.get('messages').children[0];
  assert.doesNotMatch(text(first),/everyone/i);assert.match(text(first),/Instances \(self-reported\): instance-only/);
  assert.match(text(e.get('messages').children[1]),/Sessions: Reviewer.*Instances \(self-reported\): review-worker/);
  assert.match(text(e.get('messages').children[2]),/Everyone in this context/);
  await first.children[0].fire('click');
  assert.doesNotMatch(text(e.get('details')),/Everyone/);assert.match(text(e.get('details')),/instance-only/);
  await e.get('disconnect').fire('click');
});
