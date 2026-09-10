import test from 'node:test';
import assert from 'node:assert/strict';
import {sequenceLayout, occurrenceLink, LANE_LIMIT, ARROW_LIMIT} from '../ui/sequence.mjs';
import {BROADCAST} from '../ui/model.mjs';

const message=(sequence,source,targets,time=sequence*1000)=>({ID:`m${sequence}`,Sequence:sequence,SessionID:source,CreatedAt:new Date(time).toISOString(),envelope:{recipient_session_ids:targets}});

test('occurrence links run left to right and end at circle boundaries, including self messages',()=>{
  for(const targetY of [0,88,176]) {
    const link=occurrenceLink(200,88,targetY);
    assert.equal(link.targetX,264);
    assert.equal(link.radius,18);
    const points=link.d.match(/-?\d+(?:\.\d+)?/g).map(Number);
    assert.ok(points[2]>points[0]);
    assert.ok(Math.abs(Math.hypot(points[0]-200,points[1]-88)-18)<1e-8);
    assert.ok(Math.abs(Math.hypot(points[2]-264,points[3]-targetY)-18)<1e-8);
  }
});

test('event order follows log sequence despite tied or reversed send timestamps',()=>{
  const messages=[message(3,'a',['b'],1000),message(1,'a',['b','c'],2000),message(2,'b',['a'],1000)];
  const flow=sequenceLayout(messages,[],{spacing:'order'});
  assert.equal(flow.spacing,'order');
  assert.deepEqual(flow.routes.map(r=>r.message.Sequence),[1,1,2,3]);
  assert.deepEqual(flow.routes.map(r=>r.time),[2000,2000,1000,1000]);
  assert.deepEqual(sequenceLayout(messages,[],{spacing:'time'}).routes.map(r=>r.message.Sequence),[2,3,1,1]);
});

test('time-axis layout preserves time spacing, ties, self routes, broadcasts and recipient deduplication',()=>{
  const messages=[message(3,'a',['a'],9000),message(1,'a',['b','b'],1000),message(2,'b',[],3000),message(4,'b',['a'],3000)];
  const flow=sequenceLayout(messages,[{SessionID:'a',Name:'Same'},{SessionID:'b',Name:'Same'}]);
  assert.deepEqual(flow.routes.map(r=>[r.message.ID,r.target,r.position]),[['m1','b',0],['m2',BROADCAST,.25],['m4','a',.25],['m3','a',1]]);
  assert.equal(flow.lanes.length,3);
  assert.deepEqual(flow.lanes.filter(l=>l.name==='Same').map(l=>l.id),['a','b']);
  assert.equal(flow.routeCount,4);
});

test('200 agents and dense equal-time messages stay bounded with every addressed route reachable',()=>{
  const agents=Array.from({length:200},(_,i)=>`a${String(i).padStart(3,'0')}`);
  const messages=agents.map((source,i)=>message(i+1,source,[agents[(i+101)%200],agents[(i+1)%200]],1000));
  // A wide fan-out crosses many lane pages, and shares an exact timestamp.
  messages.push(message(201,agents[0],agents,1000));
  const found=new Set();
  for(let lanePage=0;lanePage<Math.ceil(200/LANE_LIMIT);lanePage++) {
    const first=sequenceLayout(messages,[],{lanePage});
    assert.ok(first.visible.length<=LANE_LIMIT);
    for(let arrowPage=0;arrowPage<Math.ceil(first.routeCount/ARROW_LIMIT);arrowPage++) {
      const flow=sequenceLayout(messages,[],{lanePage,arrowPage});
      assert.ok(flow.routes.length<=ARROW_LIMIT);
      for(const route of flow.routes) {
        assert.equal(route.position,0);
        found.add(`${route.message.ID}:${route.target}`);
      }
    }
  }
  assert.equal(found.size,600);
  const flow=sequenceLayout(messages,[],{lanePage:900,arrowPage:900});
  assert.equal(flow.lanePage,24);
  assert.ok(flow.routes.length>0);
});

test('time zoom, unknown timestamps and message seeking do not imply receipt times',()=>{
  const messages=Array.from({length:80},(_,i)=>message(i+1,`a${String(i).padStart(3,'0')}`,['target'],i*1000));
  messages.push({...message(81,'unknown',['target']),CreatedAt:'bad'});
  const zoom=sequenceLayout(messages,[],{span:10000,start:30000});
  assert.equal(zoom.from,30000);assert.equal(zoom.to,40000);assert.equal(zoom.inRange,11);assert.equal(zoom.unknownTimes,1);
  const sought=sequenceLayout(messages,[],{span:10000,seekMessage:'m70'});
  assert.ok(sought.visible.some(l=>l.id==='a069'));
  assert.ok(sought.routes.some(r=>r.message.ID==='m70'));
  assert.ok(sought.from<=69000&&sought.to>=69000);
  assert.equal(sequenceLayout([],[],{span:60000}).routes.length,0);
});
