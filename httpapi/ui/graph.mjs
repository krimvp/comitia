import {BROADCAST,shortID,senderActor,recipientActors} from './model.mjs';
const NS='http://www.w3.org/2000/svg';
const palette=['#5372de','#9570cc','#168b87','#c77635','#ce6080','#558742'];
export function agentColor(id) {
  let hash=0;for (const c of id) hash=(hash*31+c.charCodeAt(0))>>>0;
  return palette[hash%palette.length];
}
function svg(tag,attrs={},text) {
  const el=document.createElementNS(NS,tag);
  for(const [key,value] of Object.entries(attrs)) el.setAttribute(key,String(value));
  if(text!==undefined) el.textContent=String(text);
  return el;
}
function actionable(el,label,action) {
  el.setAttribute('role','button');el.setAttribute('tabindex','0');el.setAttribute('aria-label',label);
  el.addEventListener('click',action);
  el.addEventListener('keydown',event=>{if(event.key==='Enter'||event.key===' '){event.preventDefault();action();}});
}
// Instance nodes lead with the self-reported claim, then the owning session's name.
const nodeLabel=node=>node.instance?`${node.instance} (self-reported, ${node.name}${node.session?` ${shortID(node.session)}`:''})`:node.id===BROADCAST?node.name:`${node.name} ${shortID(node.id)}`;
const clip=(text,size)=>text.length>size?text.slice(0,size-1)+'…':text;
function endpoint(from,to,padding=0) {
  const dx=to.x-from.x,dy=to.y-from.y;
  const scale=Math.min((78+padding)/Math.max(.001,Math.abs(dx)),(35+padding)/Math.max(.001,Math.abs(dy)));
  return {x:from.x+dx*scale,y:from.y+dy*scale};
}
export function renderInteractionMap(container,graph,{agent='',route=null,selectedMessage=null,onAgent,onRoute}={}) {
  const rank=node=>node.sent+node.received;
  let nodes=[...graph.nodes].sort((a,b)=>rank(b)-rank(a)||a.id.localeCompare(b.id));
  if(agent) {
    const neighbors=new Set([agent]);
    for(const edge of graph.edges) if(edge.source===agent||edge.target===agent){neighbors.add(edge.source);neighbors.add(edge.target);}
    nodes=nodes.filter(n=>neighbors.has(n.id));
  }
  if(route) nodes.sort((a,b)=>Number([route.from,route.to].includes(b.id))-Number([route.from,route.to].includes(a.id))||rank(b)-rank(a));
  if(agent) nodes.sort((a,b)=>Number(b.id===agent)-Number(a.id===agent)||rank(b)-rank(a));
  const total=nodes.length;
  nodes=nodes.slice(0,16).sort((a,b)=>a.id.localeCompare(b.id));
  const mobile=container.clientWidth>0&&container.clientWidth<440;
  const width=mobile?340:760,height=mobile?Math.max(220,nodes.length*116+30):nodes.length>6?520:350,positions=new Map();
  // Stable ID ordering means counts changing during polling do not move nodes.
  nodes.forEach((node,i)=>{
    let x,y;
    if(mobile){x=width/2;y=60+i*116;}
    else if(nodes.length===1){x=width/2;y=height/2;}
    else if(nodes.length===2){x=190+i*380;y=height/2;}
    else if(nodes.length===3){[x,y]=[[width/2,75],[170,265],[590,265]][i];}
    else if(nodes.length<=6){const angle=-Math.PI/2+i*2*Math.PI/nodes.length;x=width/2+260*Math.cos(angle);y=height/2+112*Math.sin(angle);}
    else {x=100+(i%4)*185;y=70+Math.floor(i/4)*125;}
    positions.set(node.id,{x,y});
  });
  const canvas=svg('svg',{viewBox:`0 0 ${width} ${height}`,role:'group','aria-label':'Agent sessions and directed message counts'});
  const defs=svg('defs');
  for(const [id,color] of [['normal','#8493b4'],['active','#4664d8']]) {
    const marker=svg('marker',{id:`arrow-${id}`,viewBox:'0 0 10 10',refX:9,refY:5,markerWidth:7,markerHeight:7,orient:'auto',markerUnits:'userSpaceOnUse'});
    marker.append(svg('path',{d:'M 1 1 L 9 5 L 1 9 z',fill:color}));defs.append(marker);
  }
  canvas.append(defs);
  const drawnEdges=graph.edges.filter(e=>positions.has(e.source)&&positions.has(e.target));
  const edgeGroup=svg('g',{'class':'map-edges'});
  const edgeKeys=new Set(graph.edges.map(e=>JSON.stringify([e.source,e.target])));
  const byID=new Map(graph.nodes.map(n=>[n.id,n]));
  for(const edge of drawnEdges) {
    const source=positions.get(edge.source),target=positions.get(edge.target);
    const reciprocal=edgeKeys.has(JSON.stringify([edge.target,edge.source]));
    const length=Math.hypot(target.x-source.x,target.y-source.y)||1;
    const normal={x:-(target.y-source.y)/length,y:(target.x-source.x)/length};
    const bend=mobile?190:reciprocal?32:14;
    const control={x:(source.x+target.x)/2+normal.x*bend,y:(source.y+target.y)/2+normal.y*bend};
    let start=endpoint(source,control,3),end=endpoint(target,control,9);
    let d=`M ${start.x} ${start.y} Q ${control.x} ${control.y} ${end.x} ${end.y}`;
    let label={x:.25*start.x+.5*control.x+.25*end.x,y:.25*start.y+.5*control.y+.25*end.y};
    if(edge.source===edge.target){start={x:source.x+48,y:source.y-35};end={x:source.x+78,y:source.y-5};d=`M ${start.x} ${start.y} C ${source.x+135} ${source.y-100}, ${source.x+155} ${source.y+10}, ${end.x} ${end.y}`;label={x:source.x+113,y:source.y-48};}
    const selectedRoute=route?.from===edge.source&&route?.to===edge.target;
    const messageRoute=Boolean(selectedMessage)&&senderActor(selectedMessage)===edge.source&&recipientActors(selectedMessage,graph.claims).includes(edge.target);
    const related=!agent||edge.source===agent||edge.target===agent;
    const active=route?selectedRoute:messageRoute||Boolean(agent&&related);
    const faded=Boolean(route&&!selectedRoute)||!related;
    const group=svg('g',{'class':`map-edge${active?' active':''}${faded?' faded':''}`});
    const from=byID.get(edge.source),to=byID.get(edge.target);
    const labelText=`${nodeLabel(from)} to ${nodeLabel(to)}: ${edge.count} messages`;
    group.append(svg('title',{},labelText),svg('path',{d,'class':'edge-hit'}),svg('path',{d,'class':'edge-line','stroke-width':Math.min(4,1.2+Math.log2(edge.count+1)*.65),'marker-end':`url(#arrow-${active?'active':'normal'})`}),svg('rect',{x:label.x-16,y:label.y-12,width:32,height:24,rx:12,'class':'edge-count-bg'}),svg('text',{x:label.x,y:label.y+4,'text-anchor':'middle','class':'edge-count'},edge.count));
    actionable(group,labelText,()=>onRoute?.(edge.source,edge.target));edgeGroup.append(group);
  }
  canvas.append(edgeGroup);
  for(const node of nodes) {
    const {x,y}=positions.get(node.id),isBroadcast=node.id===BROADCAST;
    const active=agent===node.id||route?.from===node.id||route?.to===node.id;
    const group=svg('g',{'class':`map-node${active?' active':''}${isBroadcast?' broadcast':''}`,transform:`translate(${x-78} ${y-35})`,id:`map-node-${node.id}`});
    const title=node.instance?node.instance:node.name, sub=isBroadcast?'Shared context':node.instance?`${node.name} · self-reported`:shortID(node.id);
    group.append(svg('title',{},`${node.instance?`${node.instance} (self-reported instance) · ${node.name} · ${node.session||'unknown session'}`:`${node.name} · ${node.id}`}\n${node.sent} sent · ${node.received} received${node.active?' · active session':''}`),svg('rect',{width:156,height:70,rx:12,'class':'node-bg'}),svg('circle',{cx:25,cy:27,r:13,fill:isBroadcast?'#71819a':agentColor(node.id)}),svg('text',{x:25,y:31,'text-anchor':'middle','class':'node-initials'},isBroadcast?'◎':node.name.slice(0,2).toUpperCase()),svg('text',{x:46,y:26,'class':'node-name'},clip(title,14)),svg('text',{x:46,y:44,'class':'node-id'},clip(sub,17)),svg('text',{x:14,y:61,'class':'node-stats'},isBroadcast?`${node.received} broadcasts`:`${node.sent} sent · ${node.received} received`));
    actionable(group,isBroadcast?`Show ${node.received} broadcast messages`:`Show messages involving ${nodeLabel(node)}`,()=>isBroadcast?onRoute?.('',BROADCAST):onAgent?.(node.id));canvas.append(group);
  }
  if(!nodes.length) canvas.append(svg('text',{x:width/2,y:height/2,'text-anchor':'middle','class':'map-empty'},'Interactions appear as agent messages arrive.'));
  container.replaceChildren(canvas);
  return {shown:nodes.length,total,drawnEdges:drawnEdges.length};
}
