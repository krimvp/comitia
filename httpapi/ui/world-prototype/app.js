// THROWAWAY PROTOTYPE: three spatial interpretations of one read-only conversation.
import * as THREE from 'three';
import {OrbitControls} from './vendor/OrbitControls.js';
const $ = id => document.getElementById(id);
const variants = ['studio','orbit','forum'];
let variant = variants.includes(new URLSearchParams(location.search).get('variant')) ? new URLSearchParams(location.search).get('variant') : 'studio';
const palette = ['#548f7f','#be8f65','#858ab7','#bf839a','#8b9b59','#6b9eb6'];
const demos = [ ['atlas','Atlas','Coordinator'],['sage','Sage','Researcher'],['pixel','Pixel','Builder'],['iris','Iris','Reviewer'],['moss','Moss','Tester'],['echo','Echo','Writer'] ].map(([id,name,role],i)=>({id,name,role,color:palette[i]}));
let agents = demos.map(a=>({...a})), messages = [], selectedAgent='', selectedMessage=null, sequence=0, observed=0;
let mode='demo', paused=false, reduced=matchMedia('(prefers-reduced-motion: reduce)').matches;
let token='', contextID='', contextName='', generation=0, pollTimer, requestController=new AbortController(), contexts=[];
let animationTime=0, nextDemo=3, demoIndex=0, scene, camera, renderer, controls, room, beacons, robotNodes=new Map(), flights=[], pulses=[], allLines=[];
const host=$('canvas-host'), raycaster=new THREE.Raycaster(), pointer=new THREE.Vector2();
const maxVisible=24;
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
 const robot=new THREE.Group();station.add(robot);robot.position.set(0,variant==='studio'?.08:.30,variant==='studio'?.27:0);
 const body=mesh(new THREE.CapsuleGeometry(.23,.25,5,12),agent.color,robot);body.position.y=.48;
 const head=mesh(new THREE.SphereGeometry(.29,24,18),'#f5f1df',robot);head.position.y=.93;head.scale.set(1,.86,.90);
 const visor=mesh(new THREE.SphereGeometry(.235,24,16),'#263f36',robot);visor.position.set(0,.94,.11);visor.scale.set(1,.60,.75);
 for(const x of [-.082,.082]){const eye=mesh(new THREE.SphereGeometry(.029,10,8),'#d5f5d9',robot);eye.position.set(x,.954,.277);}
 for(const x of [-.29,.29]){const hand=mesh(new THREE.CapsuleGeometry(.075,.19,4,8),agent.color,robot);hand.position.set(x,.48,0);hand.rotation.z=x>0?-.2:.2;}
 for(const x of [-.11,.11]){const foot=mesh(new THREE.SphereGeometry(.10,12,8),'#394f45',robot);foot.position.set(x,.10,.065);foot.scale.set(1,.65,1.6);}
 cylinder(.016,.16,'#526b5b',0,1.23,0,robot);const antenna=mesh(new THREE.SphereGeometry(.049,12,8),agent.color,robot);antenna.position.y=1.33;
 const halo=ring(.57,agent.color,0,.035,0,robot);halo.material.transparent=true;halo.material.opacity=.2;
 const label=node('button',agent.name,'agent-label');label.title=`${agent.name} · ${agent.id}`;label.addEventListener('click',()=>selectAgent(agent.id));$('labels').append(label);
 station.traverse(o=>{o.userData.agent=agent.id;});
 robotNodes.set(agent.id,{station,robot,halo,antenna,label,pos:pos.clone(),base:robot.position.y,phase:idx*.9,pulse:0});
}
function positions(n){return Array.from({length:n},(_,i)=>{
 if(variant==='studio'&&n<=6)return new THREE.Vector3(...[[-3.05,0,-2.1],[0,0,-2.85],[3.05,0,-2.1],[-3.05,0,1.8],[0,0,2.8],[3.05,0,1.8]][i]);
 const r=n>12?5.8:4.1,a=i/n*Math.PI*2-Math.PI/2;
 return new THREE.Vector3(Math.cos(a)*r,variant==='orbit'?(i%3)*.32:0,Math.sin(a)*r);
 });}
function resetCamera(){if(!camera)return;const isSmall=host.clientWidth<550;camera.position.set(12,12,14);controls.target.set(0,.25,0);camera.zoom=isSmall?.76:1;controls.update();resize();}
function buildWorld(){
 if(!scene)return;
 for(const f of flights){scene.remove(f.group);disposeTree(f.group);}for(const p of pulses){scene.remove(p.mesh);disposeTree(p.mesh);}flights=[];pulses=[];allLines=[];
 if(room){scene.remove(room);disposeTree(room);}room=new THREE.Group();scene.add(room);robotNodes.clear();$('labels').replaceChildren();
 const isDark=variant==='orbit';scene.background=new THREE.Color(isDark?'#12221d':variant==='forum'?'#efe5d7':'#e7eee4');
 const n=Math.min(agents.length,maxVisible),extent=n>12?15:11;
 if(variant==='studio'){
  box(extent,.35,extent,'#bdc8b8',0,-.2,0);box(extent-.12,.09,extent-.12,'#f0efdf',0,.025,0);
  const grid=new THREE.GridHelper(extent-.18,Math.round(extent),'#d8decd','#dde1d4');grid.position.y=.076;room.add(grid);
  box(extent,.80,.15,'#d2dac9',0,.41,-extent/2);box(.15,.80,extent,'#c8d3c1',-extent/2,.41,0);
  for(const x of [-4.6,4.6]){plant(x,-4.5,1.3);plant(x,4.4,.8);}
  // A low shelf gives the room a sense of scale without obscuring the agents.
  box(2.5,.48,.47,'#b9aa8b',1.7,.30,-5.05);
  for(let i=0;i<8;i++)box(.12,.25+(i%3)*.06,.26,palette[i%6],.7+i*.21,.68,-5.04);
 }else if(variant==='forum'){
  cylinder(extent*.51,.24,'#d8bfa2',0,-.10,0);cylinder(extent*.46,.10,'#f2e5d2',0,.06,0);
  for(let i=0;i<3;i++)ring(2+i*.55,'#d6b994',0,.13,0);
  for(let i=0;i<12;i++){const a=i/12*Math.PI*2;cylinder(.11,1.05,'#d0b798',Math.cos(a)*5.15,.62,Math.sin(a)*5.15);}
 }else{
  const grid=new THREE.PolarGridHelper(6,12,5,96,'#294e3d','#203d30');grid.position.y=-.15;room.add(grid);
  for(let i=0;i<70;i++){const s=mesh(new THREE.SphereGeometry(.018,5,4),'#537767');s.position.set(Math.sin(i*13.7)*7,-.7+Math.cos(i*2.3)*.4,Math.cos(i*7.1)*7);}
 }
 cylinder(.93,.15,isDark?'#2f5141':'#d6d7c0',0,.15,0);cylinder(.68,.19,isDark?'#3b6250':'#a5bfa5',0,.31,0);ring(.53,isDark?'#9ed5ae':'#5d9278',0,.43,0);
 beacons=mesh(new THREE.OctahedronGeometry(.38,0),isDark?'#c4e9ba':'#75a98b');beacons.position.y=1.05;beacons.material.emissive=new THREE.Color('#3c8053');beacons.material.emissiveIntensity=.2;
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
 let down;renderer.domElement.addEventListener('pointerdown',e=>{down={x:e.clientX,y:e.clientY};});renderer.domElement.addEventListener('pointerup',e=>{if(!down||Math.hypot(e.clientX-down.x,e.clientY-down.y)>5)return;const r=renderer.domElement.getBoundingClientRect();pointer.set((e.clientX-r.left)/r.width*2-1,-(e.clientY-r.top)/r.height*2+1);raycaster.setFromCamera(pointer,camera);const hit=raycaster.intersectObjects(room.children,true).find(h=>h.object.userData.agent);if(hit)selectAgent(hit.object.userData.agent);});
 buildWorld();let last=performance.now();renderer.setAnimationLoop(now=>{const dt=Math.min((now-last)/1000,.05);last=now;if(!paused){animationTime+=dt;step(dt);}controls.update();projectLabels();renderer.render(scene,camera);});
}
function resize(){if(!renderer)return;const w=host.clientWidth,h=host.clientHeight;renderer.setSize(w,h);const span=8;camera.left=-span*w/h;camera.right=span*w/h;camera.top=span;camera.bottom=-span;camera.updateProjectionMatrix();}
function projectLabels(){for(const [id,r] of robotNodes){const p=r.station.position.clone().add(new THREE.Vector3(0,-.1,1.02)).project(camera);r.label.style.left=`${(p.x*.5+.5)*host.clientWidth}px`;r.label.style.top=`${(-p.y*.5+.5)*host.clientHeight}px`;r.label.style.display=p.z>1?'none':'';}const p=new THREE.Vector3(0,.1,.6).project(camera),b=$('beacon-label');if(b){b.style.left=`${(p.x*.5+.5)*host.clientWidth}px`;b.style.top=`${(-p.y*.5+.5)*host.clientHeight}px`;}}
function addPulse(pos,color){if(!scene||reduced)return;const m=ring(.25,color,pos.x,.20+pos.y,pos.z,scene);m.material.transparent=true;pulses.push({mesh:m,age:0});}
function fly(m){if(!scene)return;const source=robotNodes.get(m.SessionID);if(!source)return;source.pulse=1;addPulse(source.pos,typeColor(m));if(reduced)return;
 for(const target of targets(m).slice(0,24)){
  const targetNode=robotNodes.get(target);if(target!=='@context'&&!targetNode)continue;
  if(flights.length>=80)break;
  const from=source.pos.clone().add(new THREE.Vector3(0,1.55,.25));const to=target==='@context'?new THREE.Vector3(0,1.05,0):targetNode.pos.clone().add(new THREE.Vector3(0,1.4,.25));
  const mid=from.clone().lerp(to,.5);mid.y+=1.25+from.distanceTo(to)*.15;
  if(from.distanceTo(to)<.1)mid.x+=1.3;
  const curve=new THREE.QuadraticBezierCurve3(from,mid,to),group=new THREE.Group();scene.add(group);
  const line=new THREE.Line(new THREE.BufferGeometry().setFromPoints(curve.getPoints(50)),new THREE.LineBasicMaterial({color:typeColor(m),transparent:true,opacity:.33}));group.add(line);
  const packet=mesh(new THREE.OctahedronGeometry(.105),typeColor(m),group);packet.material.emissive=new THREE.Color(typeColor(m));packet.material.emissiveIntensity=.6;
  flights.push({group,curve,packet,line,age:0,duration:1.6,target,color:typeColor(m),landed:false});
 }
}
function step(dt){
 if(mode==='demo'&&animationTime>=nextDemo){sendDemo();nextDemo=animationTime+3.5;}
 if(beacons&&!reduced){beacons.rotation.y=animationTime*.35;beacons.position.y=1.05+Math.sin(animationTime*1.7)*.07;}
 for(const [id,r] of robotNodes){r.pulse=Math.max(0,r.pulse-dt*.7);r.robot.position.y=r.base+(reduced?0:Math.sin(animationTime*2+r.phase)*.018+Math.sin(r.pulse*Math.PI)*.19);r.halo.material.opacity=selectedAgent===id?.9:.18+r.pulse*.7;r.halo.scale.setScalar(1+r.pulse*.16);r.antenna.material.emissive=new THREE.Color(agents.find(a=>a.id===id)?.color||'#548f7f');r.antenna.material.emissiveIntensity=r.pulse;}
 flights=flights.filter(f=>{f.age+=dt;const p=Math.min(f.age/f.duration,1);f.packet.position.copy(f.curve.getPoint(p));f.packet.rotation.y+=dt*4;if(p===1&&!f.landed){f.landed=true;f.packet.visible=false;const dest=robotNodes.get(f.target);if(dest){dest.pulse=.65;addPulse(dest.pos,f.color);}else addPulse(new THREE.Vector3(0,0,0),f.color);}f.line.material.opacity=.33*Math.max(0,1-(f.age-f.duration)/1.1);if(f.age>f.duration+1.1){scene.remove(f.group);disposeTree(f.group);return false;}return true;});
 pulses=pulses.filter(p=>{p.age+=dt;p.mesh.scale.setScalar(1+p.age*3);p.mesh.material.opacity=Math.max(0,.6-p.age*.55);if(p.age>1.1){scene.remove(p.mesh);disposeTree(p.mesh);return false;}return true;});
}
function ensureAgents(incoming){let changed=false;for(const m of incoming)for(const id of [m.SessionID,...targets(m)]){if(id==='@context'||agents.some(a=>a.id===id))continue;agents.push({id,name:id.slice(0,8),role:'Observed session',color:palette[agents.length%palette.length]});changed=true;}return changed;}
function append(incoming,animate=true){const ids=new Set(messages.map(m=>m.ID));const fresh=incoming.filter(m=>!ids.has(m.ID));if(ensureAgents(fresh))buildWorld();for(const m of fresh){messages.push(m);observed++;if(animate)fly(m);}messages=messages.slice(-1000);renderRoster();renderFeed();}
const script=[['atlas','sage','message','Can you map the requirements for the new inspector?'],['sage','pixel','message','The context API already exposes participant sessions.'],['pixel','iris','review','The scene is ready. Could you review the message routing?'],['iris','pixel','review','Keep shared broadcasts distinct from addressed messages.'],['moss','atlas','result','Replay and deduplication checks passed.'],['echo','@context','message','I’ve documented the agent interaction model.'],['pixel','moss','message','Please check reconnect behavior after a missed poll.'],['atlas','@context','result','Ready for the next iteration.']];
function sendDemo(broadcast=false){if(mode!=='demo')return;const [from,to,type,body]=script[demoIndex++%script.length];const m={ID:'demo-'+(++sequence),Sequence:sequence,SessionID:from,ContextID:'demo',Body:broadcast?'A new update is available in the shared context.':body,CreatedAt:new Date().toISOString(),envelope:{schema:type==='message'?'comitia.message.v1':`comitia.${type}.v1`,recipient_session_ids:broadcast||to==='@context'?[]:[to]}};append([m]);}
function renderRoster(){
 $('agent-count').textContent=String(agents.length).padStart(2,'0');$('roster').replaceChildren(...agents.map(a=>{const b=node('button',undefined,'roster-agent');b.setAttribute('aria-pressed',String(selectedAgent===a.id));b.title=`${a.name} · ${a.id}`;const avatar=node('span',a.name.slice(0,1).toUpperCase(),'avatar');avatar.style.setProperty('--agent',a.color);const text=node('span',a.name,'roster-name');text.append(node('small',a.role));b.append(avatar,text);b.onclick=()=>selectAgent(a.id);return b;}));
}
function selectAgent(id){selectedAgent=selectedAgent===id?'':id;selectedMessage=null;renderRoster();renderFeed();renderSelection();announce(selectedAgent?`Selected ${name(id)}`:'Showing all agents');}
function renderSelection(){for(const [id,r]of robotNodes)r.label.classList.toggle('selected',id===selectedAgent);$('clear-selection').hidden=!selectedAgent&&!selectedMessage;$('selection').hidden=!selectedAgent&&!selectedMessage;if(selectedMessage){const m=selectedMessage;$('selection-kind').textContent='OBSERVED MESSAGE';$('selection-title').textContent=`${name(m.SessionID)} → ${targets(m).map(name).join(', ')}`;$('selection-body').textContent=m.Body||'(No text body)';$('selection-meta').textContent=`Sequence ${m.Sequence} · ${m.ID}\n${m.envelope?JSON.stringify(m.envelope,null,2):'Plain-text broadcast'}\nAddressing is shared-context routing, not a delivery receipt.`;}
 else if(selectedAgent){const a=agents.find(a=>a.id===selectedAgent);$('selection-kind').textContent='SELECTED AGENT';$('selection-title').textContent=a?.name||selectedAgent;const sent=messages.filter(m=>m.SessionID===selectedAgent).length,addressed=messages.filter(m=>targets(m).includes(selectedAgent)).length;$('selection-body').textContent=`${sent} sent · ${addressed} addressed to this agent in loaded history.`;$('selection-meta').textContent=`Session: ${selectedAgent}${robotNodes.has(selectedAgent)?'':' · Outside the first 24 displayed agents'}`;}}
function renderFeed(){const filtered=messages.filter(m=>!selectedAgent||m.SessionID===selectedAgent||targets(m).includes(selectedAgent));$('message-count').textContent=`${observed} observed`;$('feed').replaceChildren(...filtered.slice(-60).reverse().map(m=>{const b=node('button',undefined,'event'),head=node('div',undefined,'event-head');head.append(node('strong',`${name(m.SessionID)} → ${targets(m).map(name).join(', ')}`),node('span',`#${m.Sequence}`));b.append(head,node('p',(m.Body||'(Structured message)').slice(0,180)),node('small',`${typeOf(m)} · ${targets(m)[0]==='@context'?'shared broadcast':'addressed'}`));b.onclick=()=>{selectedMessage=m;renderSelection();fly(m);announce('Selected message '+m.Sequence);};return b;}));if(!filtered.length)$('feed').append(node('p','Waiting for messages…','feed-note'));if(selectedAgent)renderSelection();}
function setVariant(v){variant=v;document.body.dataset.variant=v;const url=new URL(location.href);url.searchParams.set('variant',v);history.replaceState({},'',url);const copy={studio:['01 / The studio','A little room.\nA lot happening.','A shared space for agents working together.'],orbit:['02 / The constellation','Follow the signal.','Agents and their conversations, suspended in space.'],forum:['03 / The forum','Gather around\nthe conversation.','A common ground for collective work.']}[v];$('variant-label').textContent=copy[0];$('world-title').replaceChildren(...copy[1].split('\n').flatMap((s,i)=>i?[document.createElement('br'),document.createTextNode(s)]:[document.createTextNode(s)]));$('world-description').textContent=copy[2];buildWorld();}
function cycle(dir){setVariant(variants[(variants.indexOf(variant)+dir+3)%3]);}
$('variant-prev').onclick=()=>cycle(-1);$('variant-next').onclick=()=>cycle(1);document.addEventListener('keydown',e=>{if(e.target.closest('input,textarea,select,[contenteditable]')||$('connection-dialog').open)return;if(e.key==='ArrowLeft'){e.preventDefault();cycle(-1);}if(e.key==='ArrowRight'){e.preventDefault();cycle(1);}});
$('reset-camera').onclick=resetCamera;$('send-demo').onclick=()=>{sendDemo();nextDemo=animationTime+3.5;};$('broadcast-demo').onclick=()=>sendDemo(true);
$('clear-selection').onclick=()=>{selectedAgent='';selectedMessage=null;renderRoster();renderFeed();renderSelection();};
$('pause').onclick=()=>{paused=!paused;$('pause').textContent=paused?'▶ Resume':'Ⅱ Pause';$('pause').setAttribute('aria-pressed',String(paused));if(mode==='live'){clearTimeout(pollTimer);if(!paused)poll(generation);}};
function motionUI(){$('motion').textContent=reduced?'Motion off':'Motion on';$('motion').setAttribute('aria-pressed',String(reduced));}
$('motion').onclick=()=>{reduced=!reduced;motionUI();if(reduced){for(const f of flights){scene.remove(f.group);disposeTree(f.group);}for(const p of pulses){scene.remove(p.mesh);disposeTree(p.mesh);}flights=[];pulses=[];}};motionUI();
async function api(path,body){const response=await fetch('/v1/'+path,{method:'POST',headers:{'Content-Type':'application/json',Authorization:'Bearer '+token},body:JSON.stringify(body),signal:AbortSignal.any([requestController.signal,AbortSignal.timeout(15000)]),credentials:'omit',redirect:'error'});if(!response.ok){const e=new Error(response.status===401||response.status===403?'Credential invalid, expired, or missing read access.':response.status===502?'Comitia server unavailable. Start it or check the prototype’s --upstream setting.':`Request failed (${response.status}).`);e.status=response.status;throw e;}return response.json();}
function clearLive(){generation++;clearTimeout(pollTimer);requestController.abort();requestController=new AbortController();token='';contextID='';contextName='';contexts=[];sequence=0;observed=0;messages=[];agents=[];selectedAgent='';selectedMessage=null;$('credential').value='';$('context-select').replaceChildren();buildWorld();renderRoster();renderFeed();}
function returnDemo(error=''){clearLive();mode='demo';paused=false;agents=demos.map(a=>({...a}));$('connection-state').textContent='● Demo';$('context-label').textContent='DESIGN EXPLORATION · SIMULATED MESSAGES';$('connect-open').textContent='Connect Comitia ↗';document.querySelector('.demo-actions').hidden=false;$('live-notice').hidden=true;$('feed-note').textContent='Demo traffic. Arrival animations illustrate routing, not read receipts.';$('pause').textContent='Ⅱ Pause';$('pause').setAttribute('aria-pressed','false');$('error').textContent=error;buildWorld();renderRoster();renderFeed();nextDemo=animationTime+3;}
$('connect-open').onclick=()=>{if(mode==='live'){returnDemo();return;}$('connect-form').hidden=false;$('context-form').hidden=true;$('connect-error').textContent='';$('connection-dialog').showModal();};
function cancelConnect(){generation++;requestController.abort();requestController=new AbortController();token='';contexts=[];$('credential').value='';$('context-select').replaceChildren();$('connect-submit').disabled=false;$('connection-dialog').close();}
$('connect-cancel').onclick=cancelConnect;$('context-cancel').onclick=cancelConnect;$('connection-dialog').addEventListener('cancel',e=>{e.preventDefault();cancelConnect();});
$('connect-form').onsubmit=async e=>{e.preventDefault();token=$('credential').value.trim();$('credential').value='';const g=++generation;$('connect-submit').disabled=true;$('connect-error').textContent='';try{contexts=[];let after='';for(let page=0;page<100;page++){const result=await api('contexts/list',{after_id:after,limit:100});if(g!==generation)return;const list=result.contexts||[];contexts.push(...list);if(list.length<100)break;after=list.at(-1).ID;}if(!contexts.length)throw new Error('No contexts with read access for this credential.');$('context-select').replaceChildren(...contexts.map(c=>new Option(c.Name+' · '+c.ID.slice(0,8),c.ID)));$('connect-form').hidden=true;$('context-form').hidden=false;}catch(error){if(g===generation){token='';$('connect-error').textContent=error.message;}}finally{if(g===generation)$('connect-submit').disabled=false;}};
$('context-form').onsubmit=e=>{e.preventDefault();contextID=$('context-select').value;contextName=contexts.find(c=>c.ID===contextID)?.Name||'Shared context';mode='live';sequence=0;messages=[];agents=[];observed=0;selectedAgent='';selectedMessage=null;paused=false;$('pause').textContent='Ⅱ Pause';$('pause').setAttribute('aria-pressed','false');$('connection-dialog').close();$('context-label').textContent=contextName.toUpperCase();$('connect-open').textContent='Disconnect';$('connection-state').textContent='● Connecting';document.querySelector('.demo-actions').hidden=true;$('live-notice').hidden=false;$('error').textContent='';$('feed-note').textContent='Loaded history first, then new messages. Arcs show addressing; receipt is not confirmed.';buildWorld();renderRoster();renderFeed();poll(generation,true);};
let polling=false;
async function poll(g,initial=false){if(mode!=='live'||paused||g!==generation||polling)return;polling=true;let full=false;try{const [p,r]=await Promise.all([api('participants/list',{context_id:contextID,limit:1000}),api('messages/replay',{context_id:contextID,after:sequence,limit:100})]);if(g!==generation)return;const incoming=r.messages||[];if(!Array.isArray(incoming)||!Array.isArray(p.participants||[]))throw new Error('Unexpected Comitia response.');for(const m of incoming)if(m.ContextID!==contextID||!Number.isSafeInteger(m.Sequence)||!m.ID||!m.SessionID)throw new Error('Invalid message data.');let changed=false;const active=new Set();for(const participant of p.participants||[]){active.add(participant.SessionID);let a=agents.find(a=>a.id===participant.SessionID);if(!a){agents.push({id:participant.SessionID,name:participant.Name,role:'Read participant',color:palette[agents.length%palette.length]});changed=true;}else if(a.name!==participant.Name){a.name=participant.Name;changed=true;}}for(const a of agents)a.role=active.has(a.id)?'Read participant':'Observed session';if(changed)buildWorld();append(incoming,!initial);sequence=Math.max(sequence,...incoming.map(m=>m.Sequence));full=incoming.length===100;$('connection-state').textContent=full?'● Loading history':'● Live · 1s polling';$('error').textContent='';}catch(error){if(g!==generation)return;if(error.status===401||error.status===403){returnDemo(error.message+' Live data cleared.');return;}$('connection-state').textContent='● Reconnecting';$('error').textContent=error.message;}finally{polling=false;if(mode==='live'&&g===generation&&!paused)pollTimer=setTimeout(()=>poll(g,initial&&full),full?100:1000);}}
try{initScene();setVariant(variant);renderRoster();sendDemo();sendDemo();}catch(error){$('loading')?.remove();host.append(node('p','3D could not start. This browser needs WebGL. The conversation remains available.','feed-note'));renderRoster();renderFeed();$('error').textContent=error.message;}
