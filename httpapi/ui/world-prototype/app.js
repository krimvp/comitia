// THROWAWAY PROTOTYPE: three spatial interpretations of one read-only conversation.
import * as THREE from 'three';
import {OrbitControls} from './vendor/OrbitControls.js';
import {createReader} from './reader.js';
import {rooms, initLife, react, goToRoom, updateLife} from './life.js';
const $ = id => document.getElementById(id);
const variants = ['studio','orbit','forum'];
let variant = variants.includes(new URLSearchParams(location.search).get('variant')) ? new URLSearchParams(location.search).get('variant') : 'studio';
const palette = ['#548f7f','#be8f65','#858ab7','#bf839a','#8b9b59','#6b9eb6'];
const demos = [ ['atlas','Atlas','Coordinator'],['sage','Sage','Researcher'],['pixel','Pixel','Builder'],['iris','Iris','Reviewer'],['moss','Moss','Tester'],['echo','Echo','Writer'] ].map(([id,name,role],i)=>({id,name,role,color:palette[i]}));
let agents = demos.map(a=>({...a})), messages = [], selectedAgent='', selectedMessage=null, sequence=0, observed=0;
let roaming=true, beaconPos=new THREE.Vector3(), lastLifeUI=0;
let mode='demo', paused=false, reduced=matchMedia('(prefers-reduced-motion: reduce)').matches;
let token='', contextID='', contextName='', generation=0, pollTimer, requestController=new AbortController(), contexts=[];
let animationTime=0, nextDemo=3, demoIndex=0, scene, camera, renderer, controls, room, beacons, robotNodes=new Map(), flights=[], pulses=[];
const host=$('canvas-host'), raycaster=new THREE.Raycaster(), pointer=new THREE.Vector2();
const maxVisible=24;
const reader=createReader({name,targets,typeOf,getMessages:()=>messages,onSelect:m=>{selectedMessage=m;renderSelection();renderFeed();}});
function node(tag,text,cls){const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;}
function name(id){return id==='@context'?'Shared context':agents.find(a=>a.id===id)?.name||id.slice(0,8);}
function announce(text){$('announcement').textContent=text;}
function typeOf(m){const s=m.envelope?.schema||'message';return s.includes('review')?'review':s.includes('result')?'result':'message';}
function typeColor(m){return {message:'#72a89a',review:'#a793d0',result:'#daaa5b'}[typeOf(m)];}
function targets(m){return m.envelope?.recipient_session_ids?.length?[...new Set(m.envelope.recipient_session_ids)]:['@context'];}
function disposeTree(object){object.traverse(o=>{o.geometry?.dispose();const mats=Array.isArray(o.material)?o.material:[o.material];mats.forEach(m=>m?.dispose());});}
function mesh(geometry,color,parent=room){const m=new THREE.Mesh(geometry,new THREE.MeshStandardMaterial({color,roughness:.72}));m.castShadow=true;m.receiveShadow=true;parent.add(m);return m;}
function box(w,h,d,color,x,y,z,parent=room){const m=mesh(new THREE.BoxGeometry(w,h,d),color,parent);m.position.set(x,y,z);return m;}
function cylinder(r,h,color,x,y,z,parent=room){const m=mesh(new THREE.CylinderGeometry(r,r,h,48),color,parent);m.position.set(x,y,z);return m;}
function ring(r,color,x,y,z,parent=room){const m=mesh(new THREE.TorusGeometry(r,.025,8,72),color,parent);m.rotation.x=Math.PI/2;m.position.set(x,y,z);return m;}
function plant(x,z,size=1){const g=new THREE.Group();g.position.set(x,.12,z);room.add(g);cylinder(.23,.42,'#bdab8e',0,.21,0,g);for(let i=0;i<5;i++){const leaf=mesh(new THREE.DodecahedronGeometry(.36,0),i%2?'#749b75':'#91ad83',g);leaf.position.set(Math.sin(i*2)*.18,.65+i*.13,Math.cos(i*2)*.18);leaf.scale.set(.75,1.25,.75);}g.scale.setScalar(size);}
function makeRobot(agent,idx,pos){
 const station=new THREE.Group();station.position.copy(pos);room.add(station);
 const dark=variant==='orbit';
 if(variant==='studio'){
  box(1.45,.12,.72,'#c7b69a',0,.65,-.48,station);
  for(const x of [-.57,.57])box(.07,.62,.07,'#a29e88',x,.32,-.48,station);
  box(.56,.34,.065,'#3e554c',0,.94,-.64,station);box(.46,.24,.02,'#a7cfbb',0,.95,-.60,station);
  box(.11,.15,.08,'#516257',0,.75,-.64,station);
  box(.36,.02,.15,'#e3e5d8',0,.725,-.28,station);
  cylinder(.075,.13,agent.color,.48,.77,-.42,station);
 }else if(variant==='orbit'){
  cylinder(.81,.18,'#284a3d',0,.12,0,station);ring(.74,agent.color,0,.22,0,station);
 }else{
  cylinder(.75,.24,'#d9c3a6',0,.12,0,station);cylinder(.70,.06,'#f3e9d8',0,.27,0,station);
 }
 const actor=new THREE.Group();actor.position.copy(pos);room.add(actor);
 const robot=new THREE.Group();actor.add(robot);robot.position.set(0,variant==='studio'?.08:.30,variant==='studio'?.27:0);
 const arms=[],legs=[];
 const body=mesh(new THREE.CapsuleGeometry(.23,.25,5,12),agent.color,robot);body.position.y=.48;
 const head=new THREE.Group();robot.add(head);head.position.y=.93;
 const shell=mesh(new THREE.SphereGeometry(.29,24,18),'#f5f1df',head);shell.scale.set(1,.86,.90);
 const visor=mesh(new THREE.SphereGeometry(.235,24,16),'#263f36',head);visor.position.set(0,.01,.11);visor.scale.set(1,.60,.75);
 for(const x of [-.082,.082]){const eye=mesh(new THREE.SphereGeometry(.029,10,8),'#d5f5d9',head);eye.position.set(x,.024,.277);}
 for(const x of [-.29,.29]){const arm=new THREE.Group();arm.position.set(x,.64,0);robot.add(arm);const hand=mesh(new THREE.CapsuleGeometry(.075,.19,4,8),agent.color,arm);hand.position.y=-.15;arms.push(arm);}
 for(const x of [-.11,.11]){const foot=mesh(new THREE.SphereGeometry(.10,12,8),'#394f45',robot);foot.position.set(x,.10,.065);foot.scale.set(1,.65,1.6);legs.push(foot);}
 cylinder(.016,.16,'#526b5b',0,.30,0,head);const antenna=mesh(new THREE.SphereGeometry(.049,12,8),agent.color,head);antenna.position.y=.40;
 const halo=ring(.57,agent.color,0,.035,0,robot);halo.material.transparent=true;halo.material.opacity=.2;
 const label=node('button',undefined,'agent-label');label.append(node('span',agent.name),node('small','At their station'));label.setAttribute('aria-label',agent.name);label.title=`${agent.name} · ${agent.id}`;label.addEventListener('click',()=>selectAgent(agent.id));$('labels').append(label);
 station.traverse(o=>{o.userData.agent=agent.id;});actor.traverse(o=>{o.userData.agent=agent.id;});
 const record={station,actor,robot,head,arms,legs,halo,antenna,label,pos:actor.position,base:robot.position.y,phase:idx*.9,pulse:0};initLife(record,idx);record.nextWander+=animationTime;robotNodes.set(agent.id,record);
}
function positions(n){return Array.from({length:n},(_,i)=>{
 if(variant==='studio'&&n<=6)return new THREE.Vector3(...[[-5,0,-3.4],[2.4,0,-4.3],[5.3,0,-3.7],[2.5,0,3.9],[5.3,0,3.1],[-5,0,3.6]][i]);
 const r=n>12?5.8:4.1,a=i/n*Math.PI*2-Math.PI/2;
 return new THREE.Vector3(Math.cos(a)*r,variant==='orbit'?(i%3)*.32:0,Math.sin(a)*r);
 });}
function resetCamera(){if(!camera)return;camera.position.set(12,12,14);controls.target.set(0,.25,0);camera.zoom=variant==='forum'?1.2:1;controls.update();resize();}
function buildWorld(){
 if(!scene)return;
 for(const f of flights){scene.remove(f.group);disposeTree(f.group);}for(const p of pulses){scene.remove(p.mesh);disposeTree(p.mesh);}flights=[];pulses=[];
 if(room){scene.remove(room);disposeTree(room);}room=new THREE.Group();scene.add(room);robotNodes.clear();$('labels').replaceChildren();
 const isDark=variant==='orbit';scene.background=new THREE.Color(isDark?'#12221d':variant==='forum'?'#efe5d7':'#e7eee4');
 const n=Math.min(agents.length,maxVisible),extent=variant==='studio'?15:n>12?15:11;
 beaconPos.set(variant==='studio'?-3:0,0,0);
 if(variant==='studio'){
  box(15,.35,13,'#bdc8b8',0,-.2,0);box(14.9,.09,12.9,'#f0efdf',0,.025,0);
  box(7.55,.016,12.6,rooms.commons.color,-3.65,.081,0);
  box(6.85,.016,6.0,rooms.workshop.color,3.87,.081,-3.16);
  box(6.85,.016,5.95,rooms.review.color,3.87,.081,3.26);
  box(15,.8,.15,'#d2dac9',0,.41,-6.5);box(.15,.8,13,'#c8d3c1',-7.5,.41,0);
  // Short partitions leave the figures visible. Gaps are real walkable doors.
  for(const [z,length] of [[-4.75,3.35],[0,2.3],[4.9,3.05]])box(.15,.72,length,'#c3cfbd',.4,.40,z);
  box(7.0,.72,.15,'#cbbfcf',3.95,.40,.30);
  for(const z of [-2,2.1]){
   box(.22,.025,1.9,'#99ac91',.4,.098,z);
   for(const edge of [-.96,.96])box(.24,.92,.14,'#a5b69b',.4,.5,z+edge);
  }
  // Commons: a sofa, coffee table, and plants. Workshop: a workbench.
  box(1.95,.40,.75,'#a5b793',-5.6,.31,.25);box(1.95,.45,.18,'#a5b793',-5.6,.59,-.10);
  box(.16,.35,.75,'#91a982',-6.5,.58,.25);box(.16,.35,.75,'#91a982',-4.7,.58,.25);
  cylinder(.5,.25,'#c6ad87',-5.6,.2,1.6);cylinder(.54,.08,'#e5cfaa',-5.6,.37,1.6);
  box(2.8,.48,.55,'#b9aa8b',3.9,.3,-5.9);
  for(let i=0;i<9;i++)box(.13,.25+(i%3)*.06,.26,palette[i%6],2.9+i*.21,.68,-5.9);
  // Review board and a small meeting table.
  box(1.8,1.10,.10,'#e9e8dd',5.9,1.05,5.4);
  for(const x of [5.2,6.6])box(.06,.72,.06,'#ab9d89',x,.40,5.4);
  for(let i=0;i<6;i++)box(.28,.19,.018,palette[i%6],5.4+(i%3)*.48,1.30-Math.floor(i/3)*.35,5.465);
  cylinder(.66,.10,'#d0b49e',3.25,.68,1.6);cylinder(.12,.6,'#aa957f',3.25,.33,1.6);
  plant(-6.7,-5.6,1.1);plant(6.7,-5.5,.9);plant(-6.7,5.5,.9);plant(6.7,5.7,.75);
  for(const [id,info] of Object.entries(rooms)){
   const label=node('span',info.name,'room-label');label.dataset.room=id;$('labels').append(label);
  }
 }else if(variant==='forum'){
  cylinder(extent*.51,.24,'#d8bfa2',0,-.10,0);cylinder(extent*.46,.10,'#f2e5d2',0,.06,0);
  for(let i=0;i<3;i++)ring(2+i*.55,'#d6b994',0,.13,0);
  for(let i=0;i<12;i++){const a=i/12*Math.PI*2;cylinder(.11,1.05,'#d0b798',Math.cos(a)*5.15,.62,Math.sin(a)*5.15);}
 }else{
  const grid=new THREE.PolarGridHelper(6,12,5,96,'#294e3d','#203d30');grid.position.y=-.15;room.add(grid);
  for(let i=0;i<70;i++){const s=mesh(new THREE.SphereGeometry(.018,5,4),'#537767');s.position.set(Math.sin(i*13.7)*7,-.7+Math.cos(i*2.3)*.4,Math.cos(i*7.1)*7);}
 }
 cylinder(.93,.15,isDark?'#2f5141':'#d6d7c0',beaconPos.x,.15,0);cylinder(.68,.19,isDark?'#3b6250':'#a5bfa5',beaconPos.x,.31,0);ring(.53,isDark?'#9ed5ae':'#5d9278',beaconPos.x,.43,0);
 beacons=mesh(new THREE.OctahedronGeometry(.38,0),isDark?'#c4e9ba':'#75a98b');beacons.position.set(beaconPos.x,1.05,0);beacons.material.emissive=new THREE.Color('#3c8053');beacons.material.emissiveIntensity=.2;
 const beaconLabel=node('span','Shared context','agent-label');beaconLabel.id='beacon-label';beaconLabel.style.pointerEvents='none';$('labels').append(beaconLabel);
 const pos=positions(n);agents.slice(0,n).forEach((a,i)=>makeRobot(a,i,pos[i]));
 $('world-count').textContent=`${n}${agents.length>n?' / '+agents.length:''} agents shown · 1 shared context`;
 resetCamera();renderSelection();
}
function initScene(){
 scene=new THREE.Scene();camera=new THREE.OrthographicCamera(-8,8,8,-8,.1,100);
 renderer=new THREE.WebGLRenderer({antialias:true,alpha:false});renderer.setPixelRatio(Math.min(devicePixelRatio,2));renderer.shadowMap.enabled=true;renderer.shadowMap.type=THREE.PCFSoftShadowMap;renderer.outputColorSpace=THREE.SRGBColorSpace;renderer.setClearColor('#e7eee4');
 renderer.domElement.setAttribute('aria-label','3D agent world. Agent buttons and activity list provide keyboard access.');renderer.domElement.setAttribute('role','img');host.append(renderer.domElement);$('loading').remove();
 scene.add(new THREE.HemisphereLight('#fffbec','#8aa590',3));const sun=new THREE.DirectionalLight('#fff4d9',3);sun.position.set(-3,12,7);sun.castShadow=true;sun.shadow.mapSize.set(2048,2048);Object.assign(sun.shadow.camera,{left:-10,right:10,top:10,bottom:-10});sun.shadow.normalBias=.03;scene.add(sun);
 controls=new OrbitControls(camera,renderer.domElement);controls.enableDamping=true;controls.enablePan=false;controls.minZoom=.55;controls.maxZoom=2;controls.minPolarAngle=.3;controls.maxPolarAngle=1.35;
 new ResizeObserver(resize).observe(host);
 let down;renderer.domElement.addEventListener('pointerdown',e=>{down={x:e.clientX,y:e.clientY};});renderer.domElement.addEventListener('pointerup',e=>{if(!down||Math.hypot(e.clientX-down.x,e.clientY-down.y)>5)return;const r=renderer.domElement.getBoundingClientRect();pointer.set((e.clientX-r.left)/r.width*2-1,-(e.clientY-r.top)/r.height*2+1);raycaster.setFromCamera(pointer,camera);raycaster.params.Line.threshold=.12;const hit=raycaster.intersectObjects([room,...flights.map(f=>f.group)],true).find(h=>h.object.userData.agent||h.object.userData.messageID);if(hit?.object.userData.messageID){const message=messages.find(m=>m.ID===hit.object.userData.messageID);if(message)reader.open(message);}else if(hit)selectAgent(hit.object.userData.agent);});
 buildWorld();let last=performance.now();renderer.setAnimationLoop(now=>{const dt=Math.min((now-last)/1000,.05);last=now;if(!paused){animationTime+=dt;step(dt);}controls.update();projectLabels();renderer.render(scene,camera);});
}
function resize(){if(!renderer)return;const w=host.clientWidth,h=host.clientHeight;renderer.setSize(w,h);const base=variant==='studio'?10:8;const span=Math.max(base,(variant==='studio'?11.2:8.4)*h/w);camera.left=-span*w/h;camera.right=span*w/h;camera.top=span;camera.bottom=-span;camera.updateProjectionMatrix();}
function projectLabels(){
 const width=host.clientWidth,height=host.clientHeight,placed=[];
 const project=p=>{const v=p.project(camera);return {x:(v.x*.5+.5)*width,y:(-v.y*.5+.5)*height,z:v.z};};
 const entries=[...robotNodes.values()].map(r=>({r,p:project(r.actor.position.clone().add(new THREE.Vector3(0,-.1,.85)))})).sort((a,b)=>a.p.y-b.p.y);
 for(const {r,p}of entries){
  r.label.style.display=p.z>1?'none':'';if(p.z>1)continue;
  const w=r.label.offsetWidth,h=r.label.offsetHeight;
  const offsets=[[0,0],[0,24],[0,-24],[-48,8],[48,8],[0,48],[0,-48],[-75,25],[75,25],[0,72],[0,-72]];
  let position;
  for(const [dx,dy]of offsets){
   const candidate={x:Math.max(6,Math.min(width-w-6,p.x-w/2+dx)),y:Math.max(6,Math.min(height-h-6,p.y+dy)),w,h};
   position=candidate;
   if(!placed.some(q=>candidate.x<q.x+q.w+5&&candidate.x+w+5>q.x&&candidate.y<q.y+q.h+4&&candidate.y+h+4>q.y))break;
  }
  placed.push(position);r.label.style.left=`${position.x+w/2}px`;r.label.style.top=`${position.y}px`;
 }
 for(const label of document.querySelectorAll('.room-label')){const info=rooms[label.dataset.room];const p=project(new THREE.Vector3(info.x,.15,info.z+(label.dataset.room==='commons'?-5.6:-2.5)));label.style.left=`${p.x}px`;label.style.top=`${p.y}px`;}
 const p=project(beaconPos.clone().add(new THREE.Vector3(0,.1,.6))),b=$('beacon-label');if(b){b.style.left=`${p.x}px`;b.style.top=`${p.y}px`;}
}
function addPulse(pos,color){if(!scene||reduced)return;const m=ring(.25,color,pos.x,.20+pos.y,pos.z,scene);m.material.transparent=true;pulses.push({mesh:m,age:0});}
function fly(m){if(!scene)return;const source=robotNodes.get(m.SessionID);if(!source)return;react(source,typeOf(m)==='result'?'result':'send',animationTime);addPulse(source.pos,typeColor(m));if(reduced)return;
 for(const target of targets(m).slice(0,24)){
  const targetNode=robotNodes.get(target);if(targetNode)targetNode.holdUntil=animationTime+1.65;if(target!=='@context'&&!targetNode)continue;
  if(flights.length>=80)break;
  const from=source.pos.clone().add(new THREE.Vector3(0,1.55,.25));const to=target==='@context'?beaconPos.clone().add(new THREE.Vector3(0,1.05,0)):targetNode.pos.clone().add(new THREE.Vector3(0,1.4,.25));
  const mid=from.clone().lerp(to,.5);mid.y+=1.25+from.distanceTo(to)*.15;
  if(from.distanceTo(to)<.1)mid.x+=1.3;
  const curve=new THREE.QuadraticBezierCurve3(from,mid,to),group=new THREE.Group();scene.add(group);
  const line=new THREE.Line(new THREE.BufferGeometry().setFromPoints(curve.getPoints(50)),new THREE.LineBasicMaterial({color:typeColor(m),transparent:true,opacity:.33}));group.add(line);
  const packet=mesh(new THREE.OctahedronGeometry(.105),typeColor(m),group);packet.material.emissive=new THREE.Color(typeColor(m));packet.material.emissiveIntensity=.6;
  group.traverse(o=>{o.userData.messageID=m.ID;});
  flights.push({group,curve,packet,line,age:0,duration:1.6,target,color:typeColor(m),kind:typeOf(m),landed:false});
 }
}
function step(dt){
 if(mode==='demo'&&animationTime>=nextDemo){sendDemo();nextDemo=animationTime+3.5;}
 if(beacons&&!reduced){beacons.rotation.y=animationTime*.35;beacons.position.y=1.05+Math.sin(animationTime*1.7)*.07;}
 let index=0;
 for(const [id,r] of robotNodes){
  r.pulse=Math.max(0,r.pulse-dt*.7);
  updateLife(r,dt,animationTime,{reduced,roaming,studio:variant==='studio',index:index++});
  r.halo.material.opacity=selectedAgent===id?.9:.18+r.pulse*.7;r.halo.scale.setScalar(1+r.pulse*.16);
  r.antenna.material.emissive.set(agents.find(a=>a.id===id)?.color||'#548f7f');r.antenna.material.emissiveIntensity=r.pulse;
  r.label.querySelector('small').textContent=r.activity;
 }
 if(animationTime-lastLifeUI>.6){lastLifeUI=animationTime;for(const row of document.querySelectorAll('.roster-agent')){const r=robotNodes.get(row.dataset.agent);if(r)row.querySelector('small').textContent=r.activity;}}
 flights=flights.filter(f=>{f.age+=dt;const p=Math.min(f.age/f.duration,1);f.packet.position.copy(f.curve.getPoint(p));f.packet.rotation.y+=dt*4;if(p===1&&!f.landed){f.landed=true;f.packet.visible=false;const dest=robotNodes.get(f.target);if(dest){react(dest,f.kind==='review'?'review':f.kind==='result'?'result':'receive',animationTime);addPulse(dest.pos,f.color);if(variant==='studio'&&roaming&&f.kind!=='message')goToRoom(dest,f.kind==='review'?'review':'commons',animationTime,[...robotNodes.keys()].indexOf(f.target));}else addPulse(beaconPos,f.color);}f.line.material.opacity=.33*Math.max(0,1-(f.age-f.duration)/1.1);if(f.age>f.duration+1.1){scene.remove(f.group);disposeTree(f.group);return false;}return true;});
 pulses=pulses.filter(p=>{p.age+=dt;p.mesh.scale.setScalar(1+p.age*3);p.mesh.material.opacity=Math.max(0,.6-p.age*.55);if(p.age>1.1){scene.remove(p.mesh);disposeTree(p.mesh);return false;}return true;});
}
function ensureAgents(incoming){let changed=false;for(const m of incoming)for(const id of [m.SessionID,...targets(m)]){if(id==='@context'||agents.some(a=>a.id===id))continue;agents.push({id,name:id.slice(0,8),role:'Observed session',color:palette[agents.length%palette.length]});changed=true;}return changed;}
function append(incoming,animate=true){const ids=new Set(messages.map(m=>m.ID));const fresh=incoming.filter(m=>!ids.has(m.ID));if(ensureAgents(fresh))buildWorld();for(const m of fresh){messages.push(m);observed++;if(animate)fly(m);}messages=messages.slice(-1000);renderRoster();renderFeed();}
const script=[['atlas','sage','message','Can you map the requirements for the new inspector?'],['sage','pixel','message','The context API already exposes participant sessions.'],['pixel','iris','review','The scene is ready for review. Could you check the message routing?\n\nThe sender waves as a packet leaves, and the addressed agent reacts when the visual packet arrives. Broadcasts travel to the shared-context beacon.\n\nPlease check these three things:\n• Addressed messages use exact session IDs.\n• Broadcasts remain distinct from direct addressing.\n• Reconnecting loads missed messages without acknowledging them.\n\nThe arrival animation is illustrative. We should never present it as proof that an agent read the message.'],['iris','pixel','review','Keep shared broadcasts distinct from addressed messages.'],['moss','atlas','result','Replay and deduplication checks passed.'],['echo','@context','message','I’ve documented the agent interaction model.'],['pixel','moss','message','Please check reconnect behavior after a missed poll.'],['atlas','@context','result','Ready for the next iteration.']];
function sendDemo(broadcast=false){if(mode!=='demo')return;const [from,to,type,body]=script[demoIndex++%script.length];const m={ID:'demo-'+(++sequence),Sequence:sequence,SessionID:from,ContextID:'demo',Body:broadcast?'A new update is available in the shared context.':body,CreatedAt:new Date().toISOString(),envelope:{schema:type==='message'?'comitia.message.v1':`comitia.${type}.v1`,recipient_session_ids:broadcast||to==='@context'?[]:[to],payload:type==='review'?{checks:['Verify session routing','Keep broadcasts distinct','Preserve the observer cursor'],artifact_refs:['prototype:agent-world']}:type==='result'?{summary:body}:{} }};append([m]);}
function renderRoster(){
 $('agent-count').textContent=String(agents.length).padStart(2,'0');$('roster').replaceChildren(...agents.map(a=>{const b=node('button',undefined,'roster-agent');b.dataset.agent=a.id;b.setAttribute('aria-pressed',String(selectedAgent===a.id));b.title=`${a.name} · ${a.id}`;const avatar=node('span',a.name.slice(0,1).toUpperCase(),'avatar');avatar.style.setProperty('--agent',a.color);const text=node('span',a.name,'roster-name');text.append(node('small',a.role));b.append(avatar,text);b.onclick=()=>selectAgent(a.id);return b;}));
}
function selectAgent(id){selectedAgent=selectedAgent===id?'':id;selectedMessage=null;renderRoster();renderFeed();renderSelection();announce(selectedAgent?`Selected ${name(id)}`:'Showing all agents');}
function renderSelection(){
 $('room-controls').hidden=!selectedAgent||variant!=='studio';
 for(const [id,r]of robotNodes)r.label.classList.toggle('selected',id===selectedAgent);
 $('clear-selection').hidden=!selectedAgent&&!selectedMessage;
 $('selection').hidden=!selectedAgent;
 if(!selectedAgent)return;
 const a=agents.find(a=>a.id===selectedAgent);$('selection-kind').textContent='SELECTED AGENT';$('selection-title').textContent=a?.name||selectedAgent;
 const sent=messages.filter(m=>m.SessionID===selectedAgent).length,addressed=messages.filter(m=>targets(m).includes(selectedAgent)).length;
 $('selection-body').textContent=`${sent} sent · ${addressed} addressed to this agent in loaded history.`;
 $('selection-meta').textContent=`Session: ${selectedAgent}${robotNodes.has(selectedAgent)?'':' · Outside the first 24 displayed agents'}`;
}
function renderFeed(){reader.refresh();const filtered=messages.filter(m=>!selectedAgent||m.SessionID===selectedAgent||targets(m).includes(selectedAgent));$('message-count').textContent=`${observed} observed`;$('feed').replaceChildren(...filtered.slice(-60).reverse().map(m=>{const b=node('button',undefined,'event'),head=node('div',undefined,'event-head');head.append(node('strong',`${name(m.SessionID)} → ${targets(m).map(name).join(', ')}`),node('span',`#${m.Sequence}`));b.append(head,node('p',(m.Body||'(Structured message)').slice(0,180)),node('small',`${typeOf(m)} · ${targets(m)[0]==='@context'?'shared broadcast':'addressed'}`));b.setAttribute('aria-pressed',String(selectedMessage?.ID===m.ID));b.setAttribute('aria-label',`Read message ${m.Sequence} from ${name(m.SessionID)}`);b.append(node('span','Read message ↗','read-message'));b.onclick=()=>{reader.open(m);fly(m);announce('Opened message '+m.Sequence);};return b;}));if(!filtered.length)$('feed').append(node('p','Waiting for messages…','feed-note'));if(selectedAgent)renderSelection();}
function setVariant(v){variant=v;document.body.dataset.variant=v;const url=new URL(location.href);url.searchParams.set('variant',v);history.replaceState({},'',url);const copy={studio:['01 / The studio','A little room.\nA lot happening.','A shared space for agents working together.'],orbit:['02 / The constellation','Follow the signal.','Agents and their conversations, suspended in space.'],forum:['03 / The forum','Gather around\nthe conversation.','A common ground for collective work.']}[v];$('variant-label').textContent=copy[0];$('world-title').replaceChildren(...copy[1].split('\n').flatMap((s,i)=>i?[document.createElement('br'),document.createTextNode(s)]:[document.createTextNode(s)]));$('world-description').textContent=copy[2];$('roam').hidden=v!=='studio';buildWorld();}
$('roam').onclick=()=>{roaming=!roaming;$('roam').textContent=roaming?'Wander on':'Wander off';$('roam').setAttribute('aria-pressed',String(roaming));if(!roaming)for(const r of robotNodes.values())r.path=[];};
for(const button of document.querySelectorAll('[data-go-room]'))button.onclick=()=>{const r=robotNodes.get(selectedAgent);if(r&&variant==='studio'){goToRoom(r,button.dataset.goRoom,animationTime,[...robotNodes.keys()].indexOf(selectedAgent));announce(`${name(selectedAgent)} is walking to ${rooms[button.dataset.goRoom].name}. Illustrative movement only.`);}};
function cycle(dir){setVariant(variants[(variants.indexOf(variant)+dir+3)%3]);}
$('variant-prev').onclick=()=>cycle(-1);$('variant-next').onclick=()=>cycle(1);document.addEventListener('keydown',e=>{if(e.target.closest('input,textarea,select,[contenteditable]')||$('connection-dialog').open||$('message-reader').open)return;if(e.key==='ArrowLeft'){e.preventDefault();cycle(-1);}if(e.key==='ArrowRight'){e.preventDefault();cycle(1);}});
$('reset-camera').onclick=resetCamera;$('send-demo').onclick=()=>{sendDemo();nextDemo=animationTime+3.5;};$('broadcast-demo').onclick=()=>sendDemo(true);
$('clear-selection').onclick=()=>{selectedAgent='';selectedMessage=null;renderRoster();renderFeed();renderSelection();};
$('pause').onclick=()=>{paused=!paused;$('pause').textContent=paused?'▶ Resume':'Ⅱ Pause';$('pause').setAttribute('aria-pressed',String(paused));if(mode==='live'){clearTimeout(pollTimer);if(!paused)poll(generation);}};
function motionUI(){$('motion').textContent=reduced?'Motion off':'Motion on';$('motion').setAttribute('aria-pressed',String(reduced));}
$('motion').onclick=()=>{reduced=!reduced;motionUI();if(reduced){for(const f of flights){scene.remove(f.group);disposeTree(f.group);}for(const p of pulses){scene.remove(p.mesh);disposeTree(p.mesh);}flights=[];pulses=[];}};motionUI();
async function api(path,body){const response=await fetch('/v1/'+path,{method:'POST',headers:{'Content-Type':'application/json',Authorization:'Bearer '+token},body:JSON.stringify(body),signal:AbortSignal.any([requestController.signal,AbortSignal.timeout(15000)]),credentials:'omit',redirect:'error'});if(!response.ok){const e=new Error(response.status===401||response.status===403?'Credential invalid, expired, or missing read access.':response.status===502?'Comitia server unavailable. Start it or check the prototype’s --upstream setting.':`Request failed (${response.status}).`);e.status=response.status;throw e;}return response.json();}
function clearLive(){reader.clear();generation++;clearTimeout(pollTimer);requestController.abort();requestController=new AbortController();token='';contextID='';contextName='';contexts=[];sequence=0;observed=0;messages=[];agents=[];selectedAgent='';selectedMessage=null;$('credential').value='';$('context-select').replaceChildren();buildWorld();renderRoster();renderFeed();}
function returnDemo(error=''){clearLive();mode='demo';paused=false;agents=demos.map(a=>({...a}));$('connection-state').textContent='● Demo';$('context-label').textContent='DESIGN EXPLORATION · SIMULATED MESSAGES';$('connect-open').textContent='Connect Comitia ↗';document.querySelector('.demo-actions').hidden=false;$('live-notice').hidden=true;$('feed-note').textContent='Demo traffic. Arrival animations illustrate routing, not read receipts.';$('pause').textContent='Ⅱ Pause';$('pause').setAttribute('aria-pressed','false');$('error').textContent=error;buildWorld();renderRoster();renderFeed();nextDemo=animationTime+3;}
$('connect-open').onclick=()=>{if(mode==='live'){returnDemo();return;}$('connect-form').hidden=false;$('context-form').hidden=true;$('connect-error').textContent='';$('connection-dialog').showModal();};
function cancelConnect(){generation++;requestController.abort();requestController=new AbortController();token='';contexts=[];$('credential').value='';$('context-select').replaceChildren();$('connect-submit').disabled=false;$('connection-dialog').close();}
$('connect-cancel').onclick=cancelConnect;$('context-cancel').onclick=cancelConnect;$('connection-dialog').addEventListener('cancel',e=>{e.preventDefault();cancelConnect();});
$('connect-form').onsubmit=async e=>{e.preventDefault();token=$('credential').value.trim();$('credential').value='';const g=++generation;$('connect-submit').disabled=true;$('connect-error').textContent='';try{contexts=[];let after='';for(let page=0;page<100;page++){const result=await api('contexts/list',{after_id:after,limit:100});if(g!==generation)return;const list=result.contexts||[];contexts.push(...list);if(list.length<100)break;after=list.at(-1).ID;}if(!contexts.length)throw new Error('No contexts with read access for this credential.');$('context-select').replaceChildren(...contexts.map(c=>new Option(c.Name+' · '+c.ID.slice(0,8),c.ID)));$('connect-form').hidden=true;$('context-form').hidden=false;}catch(error){if(g===generation){token='';$('connect-error').textContent=error.message;}}finally{if(g===generation)$('connect-submit').disabled=false;}};
$('context-form').onsubmit=e=>{e.preventDefault();contextID=$('context-select').value;contextName=contexts.find(c=>c.ID===contextID)?.Name||'Shared context';mode='live';sequence=0;messages=[];agents=[];observed=0;selectedAgent='';selectedMessage=null;paused=false;$('pause').textContent='Ⅱ Pause';$('pause').setAttribute('aria-pressed','false');$('connection-dialog').close();$('context-label').textContent=contextName.toUpperCase();$('connect-open').textContent='Disconnect';$('connection-state').textContent='● Connecting';document.querySelector('.demo-actions').hidden=true;$('live-notice').hidden=false;$('error').textContent='';$('feed-note').textContent='Loaded history first, then new messages. Arcs show addressing; receipt is not confirmed.';buildWorld();renderRoster();renderFeed();poll(generation,true);};
let pollingGeneration=null;
async function poll(g,initial=false){if(mode!=='live'||paused||g!==generation||pollingGeneration===g)return;pollingGeneration=g;let full=false;try{const [p,r]=await Promise.all([api('participants/list',{context_id:contextID,limit:1000}),api('messages/replay',{context_id:contextID,after:sequence,limit:100})]);if(g!==generation)return;const incoming=r.messages||[];if(!Array.isArray(incoming)||!Array.isArray(p.participants||[]))throw new Error('Unexpected Comitia response.');for(const m of incoming)if(m.ContextID!==contextID||!Number.isSafeInteger(m.Sequence)||!m.ID||!m.SessionID)throw new Error('Invalid message data.');let changed=false;const active=new Set();for(const participant of p.participants||[]){active.add(participant.SessionID);let a=agents.find(a=>a.id===participant.SessionID);if(!a){agents.push({id:participant.SessionID,name:participant.Name,role:'Read participant',color:palette[agents.length%palette.length]});changed=true;}else if(a.name!==participant.Name){a.name=participant.Name;changed=true;}}for(const a of agents)a.role=active.has(a.id)?'Read participant':'Observed session';if(changed)buildWorld();append(incoming,!initial);sequence=Math.max(sequence,...incoming.map(m=>m.Sequence));full=incoming.length===100;$('connection-state').textContent=full?'● Loading history':'● Live · 1s polling';$('error').textContent='';}catch(error){if(g!==generation)return;if(error.status===401||error.status===403){returnDemo(error.message+' Live data cleared.');return;}$('connection-state').textContent='● Reconnecting';$('error').textContent=error.message;}finally{if(pollingGeneration===g)pollingGeneration=null;if(mode==='live'&&g===generation&&!paused)pollTimer=setTimeout(()=>poll(g,initial&&full),full?100:1000);}}
try{initScene();setVariant(variant);renderRoster();sendDemo();sendDemo();}catch(error){$('loading')?.remove();host.append(node('p','3D could not start. This browser needs WebGL. The conversation remains available.','feed-note'));renderRoster();renderFeed();$('error').textContent=error.message;}
