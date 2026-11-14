import test from 'node:test';
import assert from 'node:assert/strict';
import {takeLauncherKey,viewerSession} from './viewer-session.mjs';

function storage(){const map=new Map();return {getItem:k=>map.get(k),setItem:(k,v)=>map.set(k,v),removeItem:k=>map.delete(k)};}

test('launcher fragment is cleared before any authentication request',()=>{
 let replaced;
 const key=takeLauncherKey({hash:'#viewer-key=private%2Bkey',pathname:'/',search:'?variant=studio'},{replaceState:(...args)=>{replaced=args;}});
 assert.equal(key,'private+key');assert.deepEqual(replaced,[null,'','/?variant=studio']);
});

test('launcher exchanges its key once and reloads use only a viewer session',async()=>{
 const saved=storage(),calls=[];
 const fetch=async(path,options)=>{
  calls.push([path,options]);assert.equal(options.credentials,'omit');
  if(path==='/viewer/login'){assert.deepEqual(JSON.parse(options.body),{key:'private'});return {ok:true,json:async()=>({session:'viewer-session'})};}
  assert.deepEqual(options.headers,{'X-Comitia-Viewer-Session':'viewer-session'});
  return {ok:true,json:async()=>({automatic:true})};
 };
 assert.equal(await viewerSession('private',{fetch,storage:saved}),'viewer-session');
 assert.equal(await viewerSession(null,{fetch,storage:saved}),'viewer-session');
 assert.deepEqual(calls.map(c=>c[0]),['/viewer/login','/viewer/session','/viewer/session']);
 assert.equal(saved.getItem('comitia-viewer-session'),'viewer-session');
});

test('expired viewer sessions are cleared and a failed handoff cannot connect',async()=>{
 const saved=storage();saved.setItem('comitia-viewer-session','expired');
 assert.equal(await viewerSession(null,{storage:saved,fetch:async()=>({ok:true,json:async()=>({automatic:false})})}),'');
 assert.equal(saved.getItem('comitia-viewer-session'),undefined);
 await assert.rejects(viewerSession('bad',{storage:saved,fetch:async()=>({ok:false})}),/private launcher/);
});
