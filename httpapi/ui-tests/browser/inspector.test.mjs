// Run: COMITIA_PLAYWRIGHT_MODULE=/path/to/playwright-core/index.mjs \
// COMITIA_CHROMIUM=/path/to/chrome-headless-shell node --test httpapi/ui-tests/browser/*.test.mjs
import test, {before, after} from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {readFile, mkdtemp, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {spawn} from 'node:child_process';
let server, browser, base, liveMessages, taskTitle, tasksAvailable, participantsAvailable, resume, expired, daemon, daemonHome, embedded;
let unavailable;
try {
  const {chromium} = await import(process.env.COMITIA_PLAYWRIGHT_MODULE || 'playwright');
  browser = await chromium.launch({headless:true,...(process.env.COMITIA_CHROMIUM?{executablePath:process.env.COMITIA_CHROMIUM}:{})});
} catch(error) {
  if (process.env.COMITIA_BROWSER_REQUIRED === '1') throw error;
  unavailable = 'Playwright/Chromium unavailable; see docs/inspector.md for browser test setup';
}
const browserTest = (name, fn) => test(name, {skip:unavailable}, fn);
async function startEmbedded() {
  const reserve=createServer();await new Promise(resolve=>reserve.listen(0,'127.0.0.1',resolve));
  const port=reserve.address().port;await new Promise(resolve=>reserve.close(resolve));
  daemonHome=await mkdtemp(join(tmpdir(),'comitia-inspector-'));
  daemon=spawn(process.env.COMITIA_BINARY,['--db',join(daemonHome,'test.db'),'serve','--listen',`127.0.0.1:${port}`],{env:{PATH:process.env.PATH,HOME:daemonHome},stdio:'ignore'});
  let failure;daemon.on('error',error=>failure=error);
  embedded=`http://127.0.0.1:${port}`;
  for(let attempt=0;attempt<200;attempt++) {
    if(failure)throw failure;
    if(daemon.exitCode!==null)throw new Error('Embedded test server exited before readiness');
    try {if((await fetch(`${embedded}/ui/`)).ok)return;} catch {}
    await new Promise(resolve=>setTimeout(resolve,25));
  }
  throw new Error('Embedded test server did not become ready');
}
const context = {ID:'context',Name:'Regression context',WorkspaceID:'workspace'};
const messages = Array.from({length:72}, (_, i) => ({ID:`m${i+1}`,Sequence:i+1,ContextID:context.ID,SessionID:'worker',Body:`Message ${i+1}`,CreatedAt:new Date(1700000000000+i*1000).toISOString(),envelope:{schema:'comitia.finding.v1',task_id:'task',recipient_session_ids:['reviewer'],payload:{summary:`Finding ${i+1}`,evidence_refs:['https://example.com/evidence','https://example.com/evidence']}}}));
before(async () => {
  if(unavailable)return;
  if(process.env.COMITIA_BINARY&&!process.env.COMITIA_UI_URL)await startEmbedded();
  server = createServer(async (req,res) => {
    try {
      if (req.url.startsWith('/ui/')) {
        const name = req.url === '/ui/' ? 'index.html' : req.url.slice(4);
        if (!/^[\w.-]+$/.test(name)) {res.writeHead(404).end();return;}
        if(embedded) {const response=await fetch(`${embedded}${req.url}`);res.writeHead(response.status,{'Content-Type':response.headers.get('content-type')});res.end(Buffer.from(await response.arrayBuffer()));return;}
        res.setHeader('Content-Type',name.endsWith('.css')?'text/css':name.endsWith('.html')?'text/html':name.endsWith('.woff2')?'font/woff2':name.endsWith('.txt')?'text/plain':'text/javascript');
        res.end(await readFile(new URL(name,new URL(process.env.COMITIA_UI_URL || '../../ui/',import.meta.url))));return;
      }
      let input='';for await(const chunk of req) input+=chunk;
      const body=JSON.parse(input), path=req.url.split('/').at(-1);
      if(expired) {res.writeHead(403).end();return;}
      const data=path==='get'?{context}:path==='children'?{contexts:[]}:req.url.includes('/contexts/')?{contexts:[context]}:req.url.includes('/participants/')?{participants:participantsAvailable?[{SessionID:'worker',Name:'Worker'},{SessionID:'reviewer',Name:'Reviewer'}]:[]}:req.url.includes('/tasks/')?{tasks:tasksAvailable?[{ID:'task',Title:taskTitle,Status:'pending',ContextID:context.ID}]:[]}:req.url.includes('/instances/')?{instances:[{id:'worker',session_id:'worker',active:true,last_seen:new Date().toISOString(),resume}]}:{messages:liveMessages.filter(m=>m.Sequence>body.after).slice(0,body.limit || 100)};
      res.setHeader('Content-Type','application/json');res.end(JSON.stringify(data));
    } catch(error) {res.writeHead(500).end(error.message);}
  });
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  base=`http://127.0.0.1:${server.address().port}`;
});
after(async()=>{
  await browser?.close();
  if(server)await new Promise(resolve=>server.close(resolve));
  if(daemon&&daemon.exitCode===null) {const stopped=new Promise(resolve=>daemon.once('exit',resolve));daemon.kill();await stopped;}
  if(daemonHome)await rm(daemonHome,{recursive:true,force:true});
});
async function pageFor(t,width=1440,options={}) {
  const count=options.messageCount || messages.length;
  liveMessages=Array.from({length:count},(_,i)=>({...messages[i%messages.length],ID:`m${i+1}`,Sequence:i+1}));
  if(options.transform)liveMessages=liveMessages.map(options.transform);
  if(count>200)liveMessages.at(-1).envelope={...liveMessages.at(-1).envelope,reply_to:'m1'};
  tasksAvailable=options.tasksAvailable!==false;participantsAvailable=options.participantsAvailable!==false;taskTitle='Pending task';resume={harness:'codex',checkpoint:'ready'};expired=false;
  const page=await browser.newPage({viewport:{width,height:844},colorScheme:options.colorScheme || 'light'});
  t.after(()=>page.close());
  const errors=[];page.on('pageerror',e=>errors.push(e.message));t.after(()=>assert.deepEqual(errors,[]));
  await page.goto(`${base}/ui/`);
  await page.locator('#credential').fill('test-observer');await page.locator('#connect').click();
  await page.waitForFunction(count=>document.getElementById('message-count').textContent.includes(`${count} loaded`),Math.min(count,100));
  return page;
}
async function poll(page) {
  // Exercise the real scheduled three-second timer, never call sync() directly.
  await page.waitForResponse(r=>r.url().endsWith('/messages/replay'));
  await page.waitForFunction(()=>document.getElementById('load-more').disabled===false);
}
async function rememberFocus(page,selector) {
  await page.locator(selector).first().focus();
  await page.evaluate(()=>window.focusBefore=document.activeElement);
}
async function sameFocus(page) {
  assert.equal(await page.evaluate(()=>document.activeElement===window.focusBefore && window.focusBefore.isConnected),true);
}
browserTest('keyboard message selection keeps its focused button',async t=>{
  const page=await pageFor(t);await page.locator('#pause').click();await page.locator('#show-timeline').click();
  await rememberFocus(page,'.message-select');await page.keyboard.press('Enter');await sameFocus(page);
  assert.match(await page.locator('#details').innerText(),/Message #1/);
});
browserTest('scheduled polling preserves message focus and instance disclosure',async t=>{
  const page=await pageFor(t);await page.locator('#show-timeline').click();
  await page.locator('#instances details').evaluate(e=>e.open=true);
  await rememberFocus(page,'.message-select');await poll(page);await sameFocus(page);
  assert.equal(await page.locator('#instances details').evaluate(e=>e.open),true);
});
browserTest('scheduled polling preserves SVG focus and horizontal diagram scroll',async t=>{
  const page=await pageFor(t);
  await page.locator('#sequence-canvas').evaluate(e=>e.scrollLeft=500);
  await rememberFocus(page,'.sequence-message');
  const scroll=await page.locator('#sequence-canvas').evaluate(e=>e.scrollLeft);
  await poll(page);await sameFocus(page);
  assert.equal(await page.locator('#sequence-canvas').evaluate(e=>e.scrollLeft),scroll);
});
browserTest('route selection and polling preserve route focus',async t=>{
  const page=await pageFor(t);await page.locator('.sequence-route-list summary').click();
  await rememberFocus(page,'.sequence-route');await page.keyboard.press('Enter');await sameFocus(page);
  await poll(page);await sameFocus(page);
});
browserTest('polling preserves selected detail link focus and scroll',async t=>{
  const page=await pageFor(t);await page.locator('#show-timeline').click();await page.locator('.message-select').first().click();
  await rememberFocus(page,'#details a');
  const scroll=await page.locator('.inspector').evaluate(e=>e.scrollTop);
  await poll(page);await sameFocus(page);
  assert.equal(await page.locator('.inspector').evaluate(e=>e.scrollTop),scroll);
});
for(const width of [390,960]) browserTest(`mobile/tablet selection opens nearby details and returns focus at ${width}px`,async t=>{
  const page=await pageFor(t,width);await page.locator('#pause').click();await page.locator('#show-timeline').click();
  await rememberFocus(page,'.message-select');
  const scroll=await page.evaluate(()=>scrollY);
  await page.keyboard.press('Enter');
  const detail=await page.locator('#details').boundingBox();
  assert.ok(detail.y>=0&&detail.y<844,`Selected details start outside viewport at ${detail.y}`);
  assert.equal(await page.evaluate(()=>document.querySelector('dialog[open]')?.contains(document.activeElement)),true);
  assert.match(await page.locator('#selection-status').innerText(),/Message #1/);
  await page.locator('#return-message').click();await sameFocus(page);
  assert.equal(await page.evaluate(()=>scrollY),scroll);
  await page.keyboard.press('Enter');await page.keyboard.press('Escape');await sameFocus(page);
});

browserTest('changed polling data updates task and replies while keeping focus and disclosures',async t=>{
  const page=await pageFor(t);await page.locator('#show-timeline').click();await page.locator('.message-select').first().click();
  await page.locator('#instances details').evaluate(e=>e.open=true);
  await rememberFocus(page,'#details a');
  taskTitle='Updated task title';resume={harness:'codex',checkpoint:'updated'};
  liveMessages.push({...messages[1],ID:'m73',Sequence:73,Body:'New reply',envelope:{...messages[1].envelope,reply_to:'m1'}});
  await poll(page);await sameFocus(page);
  assert.match(await page.locator('#details').innerText(),/Updated task title/);
  assert.match(await page.locator('#details').innerText(),/Replies · 1 loaded/);
  assert.equal(await page.locator('#instances details').evaluate(e=>e.open),true);
  assert.match(await page.locator('#instances pre').innerText(),/updated/);
  assert.equal(await page.locator('#details a').count(),2);
  await page.locator('#details button').filter({hasText:'#73'}).click();
  assert.match(await page.locator('#details').innerText(),/Message #73/);
});
browserTest('mobile SVG selection traps focus, preserves detail focus through polling, and closes on access loss',async t=>{
  const page=await pageFor(t,390);
  await rememberFocus(page,'.sequence-message');await page.keyboard.press('Enter');
  assert.equal(await page.locator('#details-dialog').evaluate(e=>e.open),true);
  await rememberFocus(page,'#details a');await poll(page);await sameFocus(page);
  assert.equal(await page.locator('#details-dialog').evaluate(e=>e.matches(':modal')),true);
  for(let i=0;i<5;i++) {
    await page.keyboard.press('Tab');
    // Native modal tabbing may visit browser chrome (activeElement is BODY), but never the inert workspace.
    assert.equal(await page.evaluate(()=>document.getElementById('workspace').contains(document.activeElement)),false);
  }
  expired=true;await page.waitForResponse(r=>r.url().endsWith('/messages/replay'));
  await page.waitForFunction(()=>!document.getElementById('connect-panel').hidden);
  assert.equal(await page.locator('#details-dialog').evaluate(e=>e.open),false);
  assert.equal(await page.locator('#details').innerText(),'');
  assert.equal(await page.locator('#workspace').isVisible(),false);
  assert.equal(await page.locator('#credential').evaluate(e=>document.activeElement===e),true);
});
browserTest('resizing an open mobile dialog returns the shared details to the desktop panel',async t=>{
  const page=await pageFor(t,390);await page.locator('#pause').click();await page.locator('#show-timeline').click();
  await rememberFocus(page,'.message-select');await page.keyboard.press('Enter');
  await page.setViewportSize({width:1440,height:844});
  await page.waitForFunction(()=>!document.getElementById('details-dialog').open);
  assert.equal(await page.locator('#details').evaluate(e=>e.parentNode.id),'details-home');
  await sameFocus(page);
  assert.match(await page.locator('#details').innerText(),/Message #1/);
});
browserTest('keyed reconciliation keeps reordered controls, updates handlers and preserves scrolled metadata',async t=>{
  const page=await pageFor(t);
  const result=await page.evaluate(async()=>{
    const {on,renderChildren}=await import('./dom.mjs');
    const target=document.createElement('div');document.body.append(target);target.style.cssText='height:100px;overflow:auto';
    const row=(id, label, action)=>{const el=document.createElement('button');el.dataset.key=id;el.textContent=label;on(el,'click',action);return el;};
    let value='';
    renderChildren(target,row('a','A',()=>value='old'),row('b','B',()=>{}));
    const focus=target.firstChild;focus.focus();
    renderChildren(target,row('b','New B',()=>{}),row('a','New A',()=>value='new'));
    const stable=document.activeElement===focus&&target.lastChild===focus;focus.click();
    const disclosure=document.createElement('details');disclosure.dataset.key='resume';
    const summary=document.createElement('summary');summary.textContent='Resume';
    const pre=document.createElement('pre');pre.textContent='line\n'.repeat(200);pre.style.cssText='height:80px;overflow:auto';
    disclosure.append(summary,pre);renderChildren(target,disclosure);disclosure.open=true;pre.scrollTop=100;
    const next=disclosure.cloneNode(true);next.removeAttribute('open');next.querySelector('pre').textContent+='Updated';
    renderChildren(target,next);
    return {stable,value,open:disclosure.open,scroll:pre.scrollTop,updated:pre.textContent.endsWith('Updated')};
  });
  assert.deepEqual(result,{stable:true,value:'new',open:true,scroll:100,updated:true});
});

browserTest('scheduled polling retains expanded resume metadata independently of focus',async t=>{
  const page=await pageFor(t);await page.locator('#instances details').evaluate(e=>e.open=true);
  await poll(page);
  assert.equal(await page.locator('#instances details').evaluate(e=>e.open),true);
});

browserTest('incoming messages crossing a route page retain the focused SVG route',async t=>{
  const page=await pageFor(t);await rememberFocus(page,'.sequence-message');
  liveMessages.push({...messages[0],ID:'m73',Sequence:73,Body:'Next route page'});
  await poll(page);await sameFocus(page);
  assert.match(await page.locator('#message-count').innerText(),/73 loaded/);
});
browserTest('incoming messages crossing the card limit retain the focused message page',async t=>{
  const page=await pageFor(t);await page.locator('#show-timeline').click();await rememberFocus(page,'.message-select');
  for(let sequence=73;sequence<=201;sequence++)liveMessages.push({...messages[0],ID:`m${sequence}`,Sequence:sequence});
  await poll(page);await sameFocus(page);await poll(page);await sameFocus(page);
  assert.match(await page.locator('#message-count').innerText(),/201 loaded/);
  assert.equal(await page.locator('.message-card').count(),200);
});

browserTest('mobile pointer selection returns to the retained SVG control',async t=>{
  const page=await pageFor(t,390);await page.locator('#pause').click();
  await page.locator('.sequence-message').first().evaluate(e=>window.focusBefore=e);
  await page.locator('.sequence-message').first().click();
  assert.equal(await page.locator('#details-dialog').evaluate(e=>e.open),true);
  await page.locator('#return-message').click();await sameFocus(page);
});

browserTest('task action focus survives previously absent task metadata arriving',async t=>{
  const page=await pageFor(t,1440,{tasksAvailable:false});await page.locator('#show-timeline').click();
  const action=page.locator('#message-m1 .message-actions button').filter({hasText:'Task task'});
  await action.focus();await action.evaluate(e=>window.focusBefore=e);
  tasksAvailable=true;await poll(page);await sameFocus(page);
  assert.equal(await page.evaluate(()=>window.focusBefore.textContent),'Pending task');
});
browserTest('reply action focus survives previously absent participant metadata arriving',async t=>{
  const page=await pageFor(t,1440,{participantsAvailable:false});await page.locator('#show-timeline').click();
  liveMessages.push({...messages[0],ID:'m73',Sequence:73,envelope:{...messages[0].envelope,reply_to:'m1'}});
  await poll(page);await page.locator('#message-m1 .message-select').click();
  const action=page.locator('#details button').filter({hasText:'#73'});await action.focus();await action.evaluate(e=>window.focusBefore=e);
  participantsAvailable=true;await poll(page);await sameFocus(page);
  assert.match(await page.evaluate(()=>window.focusBefore.textContent),/Worker/);
});
for(const direction of ['parent','reply']) for(const close of ['Return','Escape']) browserTest(`mobile ${direction} navigation across message pages restores a displayed trigger with ${close}`,async t=>{
  const page=await pageFor(t,390,{messageCount:201});await page.locator('#pause').click();
  await page.locator('#load-more').click();await page.locator('#load-more').click();
  await page.locator('#show-timeline').click();
  if(direction==='reply')await page.locator('#older-matches').click();
  const origin=direction==='parent'?'m201':'m1', current=direction==='parent'?'m1':'m201';
  const trigger=page.locator(`#message-${origin} .message-select`);
  await trigger.focus();await trigger.evaluate(e=>window.originTrigger=e);await page.keyboard.press('Enter');
  await page.locator('#details button').filter({hasText:direction==='parent'?'Open parent message':'#201'}).click();
  assert.equal(await page.evaluate(()=>window.originTrigger.isConnected),false);
  if(close==='Return')await page.locator('#return-message').click();else await page.keyboard.press('Escape');
  assert.equal(await page.evaluate(()=>document.activeElement.closest('.message-card')?.id),`message-${current}`);
  assert.equal(await page.evaluate(()=>document.activeElement.matches('.message-select')),true);
  const box=await page.locator(`#message-${current} .message-select`).boundingBox();
  assert.ok(box.y<844&&box.y+box.height>0,'Returned message control must be in the viewport');
  assert.ok(await page.locator('.message-card').count()<=200);
});


browserTest('full history keeps 5,000 messages, revisits its boundary and jumps to a sequence',async t=>{
  const page=await pageFor(t,1440,{messageCount:5001});await page.locator('#pause').click();await page.locator('.sequence-route-list summary').click();
  // Load through the real controller, 100 at a time; never inject UI state.
  await page.evaluate(async()=>{
    for(let count=200;count<=5000;count+=100) {
      document.getElementById('load-more').click();
      await new Promise(resolve=>{const check=()=>document.getElementById('message-count').textContent.includes(`${count} loaded`) && !document.getElementById('load-more').disabled ? resolve() : setTimeout(check,10);check();});
    }
  });
  assert.equal(await page.locator('.message-card').count(),200);
  await page.locator('#next-window').click();await page.waitForFunction(()=>document.getElementById('message-count').textContent.includes('1 loaded'));
  assert.match(await page.locator('#sequence-routes').innerText(),/#5001/);
  await page.locator('#previous-window').click();await page.waitForFunction(()=>document.getElementById('message-count').textContent.includes('100 loaded'));
  await page.locator('#pause').click();
  assert.match(await page.locator('#history-note').innerText(),/after #0/);
  await page.locator('#next-window').click();await page.waitForFunction(()=>document.getElementById('message-count').textContent.includes('1 loaded'));
  assert.match(await page.locator('#sequence-routes').innerText(),/#5001/);
  await page.locator('#jump-sequence').fill('4500');await page.locator('#jump-history button').click();
  await page.waitForFunction(()=>document.getElementById('history-note').textContent.includes('after #4499'));
  await page.locator('#show-timeline').click();
  assert.equal(await page.locator('.message-card').first().getAttribute('id'),'message-m4500');
  assert.ok(await page.locator('.message-card').count()<=200);
});

browserTest('narrow flow opens on the newest route, pins lane labels and supports Follow latest',async t=>{
  const page=await pageFor(t,390);await page.locator('#pause').click();await page.locator('.sequence-route-list summary').click();
  assert.match(await page.locator('#sequence-routes').innerText(),/#72/);
  assert.equal(await page.locator('.sequence-route').count(),1);
  const canvas=page.locator('#sequence-canvas'),labels=page.locator('.sequence-labels');
  const before=await labels.boundingBox();
  await canvas.evaluate(e=>e.scrollLeft=120);
  assert.ok(await canvas.evaluate(e=>e.scrollLeft)>0);
  const after=await labels.boundingBox();assert.ok(Math.abs(after.x-before.x)<1,'Lane names scrolled away');
  await page.locator('#sequence-previous-arrows').click();
  assert.match(await page.locator('#sequence-routes').innerText(),/#71/);
  liveMessages.push({...messages[0],ID:'m73',Sequence:73,SessionID:'zz-new-worker',CreatedAt:new Date(1700000073000).toISOString()});
  await page.locator('#load-more').click();await page.waitForFunction(()=>document.getElementById('message-count').textContent.includes('73 loaded'));
  assert.match(await page.locator('#sequence-routes').innerText(),/#71/);
  await page.locator('#sequence-follow').click();
  assert.match(await page.locator('#sequence-routes').innerText(),/#73/);
  assert.equal(await page.locator('#sequence-follow').getAttribute('aria-pressed'),'true');
  assert.equal(await canvas.evaluate(e=>e.scrollLeft),0);
  assert.ok(await page.locator('.sequence-message').count()<=36);
});

browserTest('payload-only blockers and both recipient lists are readable as safe text everywhere',async t=>{
  const reason='<img src=x onerror="window.injected=true"> Needs review';
  const page=await pageFor(t,1440,{messageCount:4,transform:(m,i)=>({...m,Body:'',envelope:i===0?{schema:'comitia.blocker.v1',recipient_instances:['instance-only'],payload:{reason,needs:['review']}}:i===1?{schema:'comitia.verification-result.v1',recipient_session_ids:['reviewer'],recipient_instances:['review-worker'],payload:{status:'not_run',reason:'Missing fixtures'}}:i===2?{schema:'extension.unknown.v1',payload:{summary:'Hidden extension preview'}}:{schema:'comitia.finding.v1',payload:{severity:'warning',summary:'Shared finding'}}})});
  await page.locator('#pause').click();await page.locator('.sequence-route-list summary').click();
  while(await page.locator('#sequence-previous-arrows').isEnabled())await page.locator('#sequence-previous-arrows').click();
  assert.match(await page.locator('#sequence-routes').innerText(),/Needs review/);
  await page.locator('#show-timeline').click();
  const first=page.locator('.message-card').first();
  assert.match(await first.innerText(),/Needs review/);assert.match(await first.innerText(),/Instances \(self-reported\): instance-only/);assert.doesNotMatch(await first.innerText(),/Everyone/i);
  assert.match(await page.locator('.message-card').nth(1).innerText(),/not_run · Missing fixtures/);
  assert.match(await page.locator('.message-card').nth(1).innerText(),/Sessions: Reviewer.*Instances \(self-reported\): review-worker/);
  assert.doesNotMatch(await page.locator('.message-card').nth(2).innerText(),/Hidden extension preview/);
  assert.match(await page.locator('.message-card').nth(3).innerText(),/Everyone in this context/);
  await first.locator('.message-select').click();
  assert.match(await page.locator('#details').innerText(),/Needs review/);assert.doesNotMatch(await page.locator('#details').innerText(),/Everyone/);
  assert.match(await page.locator('#details pre').innerText(),/comitia.blocker.v1/);
  assert.equal(await page.locator('#workspace img').count(),0);
  assert.equal(await page.evaluate(()=>window.injected),undefined);
});

browserTest('active sidebar presence and diagram metadata meet readable contrast and type sizes',async t=>{
  const page=await pageFor(t);await page.locator('#pause').click();
  const styles=await page.evaluate(()=>{
    const get=selector=>{const element=document.querySelector(selector),s=getComputedStyle(element);return {color:s.color,fill:s.fill,size:parseFloat(s.fontSize)};};
    return {sidebar:getComputedStyle(document.querySelector('.sidebar')).backgroundColor,presence:get('.instance-item small'),resume:get('.instance-item summary'),sequence:get('.sequence-id'),time:get('.sequence-time')};
  });
  const lum=color=>{
    const rgb=color.match(/[\d.]+/g).slice(0,3).map(Number).map(v=>{v/=255;return v<=.04045?v/12.92:((v+.055)/1.055)**2.4;});
    return .2126*rgb[0]+.7152*rgb[1]+.0722*rgb[2];
  };
  const contrast=(a,b)=>{const x=lum(a),y=lum(b);return (Math.max(x,y)+.05)/(Math.min(x,y)+.05);};
  assert.ok(contrast(styles.presence.color,styles.sidebar)>=4.5);
  assert.ok(contrast(styles.resume.color,styles.sidebar)>=4.5);
  assert.ok(contrast(styles.sequence.fill,'rgb(248, 250, 255)')>=4.5);
  assert.ok(contrast(styles.time.fill,'rgb(248, 250, 255)')>=4.5);
  assert.ok(styles.presence.size>=13);assert.ok(styles.sequence.size>=12);assert.ok(styles.time.size>=12);
  await page.locator('#show-map').click();
  const map=await page.locator('.node-id').first().evaluate(e=>({fill:getComputedStyle(e).fill,size:parseFloat(getComputedStyle(e).fontSize)}));
  assert.ok(contrast(map.fill,'rgb(246, 248, 252)')>=4.5);assert.ok(map.size>=12);
});

browserTest('recipient-only instance routes never show Everyone in cards or details',async t=>{
  const page=await pageFor(t,1440,{messageCount:3,transform:(m,i)=>({...m,envelope:i===0?{recipient_instances:['instance-only']}:i===1?{recipient_session_ids:['reviewer'],recipient_instances:['review-worker']}:{recipient_session_ids:[],recipient_instances:[]}})});
  await page.locator('#pause').click();await page.locator('#show-timeline').click();
  const first=page.locator('.message-card').first();
  assert.doesNotMatch(await first.innerText(),/everyone/i);assert.match(await first.innerText(),/Instances \(self-reported\): instance-only/);
  assert.match(await page.locator('.message-card').nth(1).innerText(),/Sessions: Reviewer.*Instances \(self-reported\): review-worker/);
  assert.match(await page.locator('.message-card').nth(2).innerText(),/Everyone in this context/);
  await first.locator('.message-select').click();
  assert.doesNotMatch(await page.locator('#details').innerText(),/Everyone/);assert.match(await page.locator('#details').innerText(),/instance-only/);
});


// Reviewer R1 reproduction: the sticky gutter must not paint over the first
// occurrence or its centered metadata, including the leftmost time-axis tick.
for(const width of [390,1440]) for(const colorScheme of ['light','dark']) {
  browserTest(`first route clears pinned labels at ${width}px in ${colorScheme} theme`,async t=>{
    const page=await pageFor(t,width,{colorScheme});await page.locator('#pause').click();
    assert.equal(await page.evaluate(()=>matchMedia('(prefers-color-scheme: dark)').matches),colorScheme==='dark');
    const assertClear=async spacing=>{
      assert.equal(await page.locator('#sequence-canvas').evaluate(e=>e.scrollLeft),0);
      const bounds=await page.evaluate(spacing=>{
        const rect=e=>{const r=e.getBoundingClientRect();return {x:r.x,y:r.y,right:r.right,bottom:r.bottom,width:r.width,height:r.height};};
        return {
          labels:rect(document.querySelector('.sequence-labels')),
          circle:rect(document.querySelector('.sequence-occurrence')),
          time:rect(document.querySelector(`.sequence-${spacing} > .sequence-time`)),
          ...(spacing==='ordered'?{caption:rect(document.querySelector('.sequence-ordered > .sequence-name'))}:{})
        };
      },spacing);
      for(const [name,box] of Object.entries(bounds)) {
        if(name==='labels')continue;
        assert.ok(box.width>0 && box.height>0,`${name} has no visible bounds`);
        assert.ok(box.x>=bounds.labels.right,`${name} is hidden under pinned labels: ${JSON.stringify(bounds)}`);
      }
    };
    await assertClear('ordered');
    await page.locator('#sequence-previous-arrows').click();
    await page.locator('#sequence-canvas').evaluate(e=>e.scrollLeft=80);
    await page.locator('#sequence-follow').click();await assertClear('ordered');
    await page.locator('#sequence-spacing').selectOption('time');await assertClear('timed');
    await page.locator('#sequence-canvas').evaluate(e=>e.scrollLeft=80);
    await page.locator('#sequence-follow').click();await assertClear('timed');
  });
}

for(const colorScheme of ['light','dark']) browserTest(`identity fonts and colours load locally in ${colorScheme}`,async t=>{
  const page=await pageFor(t,1440,{colorScheme});await page.locator('#pause').click();
  const result=await page.evaluate(async()=>{
    await Promise.all(['400 16px "IBM Plex Sans"','600 16px "IBM Plex Sans"','400 12px "IBM Plex Mono"','600 12px "IBM Plex Mono"'].map(font=>document.fonts.load(font)));
    const style=selector=>{const s=getComputedStyle(document.querySelector(selector));return {color:s.color,fill:s.fill,background:s.backgroundColor,font:s.fontFamily};};
    return {body:style('body'),canvas:style('.sequence-canvas'),time:style('.sequence-time'),id:style('.sequence-id'),input:style('#search'),fonts:[...document.fonts].map(f=>f.status),urls:performance.getEntriesByType('resource').map(r=>r.name)};
  });
  assert.deepEqual(result.fonts,['loaded','loaded','loaded','loaded']);
  assert.match(result.body.font,/IBM Plex Sans/);assert.match(result.id.font,/IBM Plex Mono/);
  const fontURLs=result.urls.filter(url=>url.endsWith('.woff2'));assert.equal(fontURLs.length,4);
  for(const url of result.urls)assert.equal(new URL(url).origin,new URL(page.url()).origin);
  const lum=color=>{const [r,g,b]=color.match(/[\d.]+/g).slice(0,3).map(Number).map(v=>{v/=255;return v<=.04045?v/12.92:((v+.055)/1.055)**2.4;});return .2126*r+.7152*g+.0722*b;};
  const check=(fg,bg)=>{const [hi,lo]=[lum(fg),lum(bg)].sort((a,b)=>b-a);assert.ok((hi+.05)/(lo+.05)>=4.5,`${fg} on ${bg}`);};
  check(result.body.color,result.input.background);check(result.time.fill,result.canvas.background);check(result.id.fill,result.canvas.background);
  await page.locator('#show-map').click();
  const map=await page.locator('.node-id').first().evaluate(e=>({fg:getComputedStyle(e).fill,bg:getComputedStyle(document.querySelector('.node-bg')).fill}));check(map.fg,map.bg);
  await page.locator('#disconnect').click();
  const primary=await page.locator('.primary').evaluate(e=>({fg:getComputedStyle(e).color,bg:getComputedStyle(e).backgroundColor}));check(primary.fg,primary.bg);
});
