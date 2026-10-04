// Run: COMITIA_PLAYWRIGHT_MODULE=/path/to/playwright-core/index.mjs \
// COMITIA_CHROMIUM=/path/to/chrome-headless-shell node --test httpapi/ui-tests/browser/*.test.mjs
import test, {before, after} from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {readFile, mkdtemp, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {spawn} from 'node:child_process';
let server, browser, base, liveMessages, taskTitle, resume, expired, daemon, daemonHome, embedded;
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
        res.setHeader('Content-Type',name.endsWith('.css')?'text/css':name.endsWith('.html')?'text/html':'text/javascript');
        res.end(await readFile(new URL(name,new URL(process.env.COMITIA_UI_URL || '../../ui/',import.meta.url))));return;
      }
      let input='';for await(const chunk of req) input+=chunk;
      const body=JSON.parse(input), path=req.url.split('/').at(-1);
      if(expired) {res.writeHead(403).end();return;}
      const data=path==='get'?{context}:path==='children'?{contexts:[]}:req.url.includes('/contexts/')?{contexts:[context]}:req.url.includes('/participants/')?{participants:[{SessionID:'worker',Name:'Worker'},{SessionID:'reviewer',Name:'Reviewer'}]}:req.url.includes('/tasks/')?{tasks:[{ID:'task',Title:taskTitle,Status:'pending',ContextID:context.ID}]}:req.url.includes('/instances/')?{instances:[{id:'worker',session_id:'worker',last_seen:new Date().toISOString(),resume}]}:{messages:liveMessages.filter(m=>m.Sequence>body.after).slice(0,body.limit || 100)};
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
async function pageFor(t,width=1440) {
  liveMessages=[...messages];taskTitle='Pending task';resume={harness:'codex',checkpoint:'ready'};expired=false;
  const page=await browser.newPage({viewport:{width,height:844}});
  t.after(()=>page.close());
  const errors=[];page.on('pageerror',e=>errors.push(e.message));t.after(()=>assert.deepEqual(errors,[]));
  await page.goto(`${base}/ui/`);
  await page.locator('#credential').fill('test-observer');await page.locator('#connect').click();
  await page.waitForFunction(()=>document.getElementById('message-count').textContent.includes('72 loaded'));
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
