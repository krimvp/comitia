import {BROADCAST, shortID, threadIDs} from './model.mjs';
import {agentColor} from './graph.mjs';

export const LANE_LIMIT = 8;
export const ARROW_LIMIT = 36;
const NS = 'http://www.w3.org/2000/svg';
const clampPage = (page, count, size) => Math.min(Math.max(0, Math.floor(page) || 0), Math.max(0, Math.ceil(count / size) - 1));
export const timeLabel = time => new Date(time).toLocaleTimeString(undefined, {hour:'2-digit', minute:'2-digit', second:'2-digit'});
const recipients = message => [...new Set(message.envelope?.recipient_session_ids?.length ? message.envelope.recipient_session_ids : [BROADCAST])];

// Layout uses send timestamps only. A vertical arrow is an addressed message,
// not elapsed delivery time. Off-page endpoints preserve cross-page routes.
export function sequenceLayout(messages, participants = [], {lanePage = 0, arrowPage = 0, span = 0, start = null, seekMessage = '', spacing = 'time'} = {}) {
  const names = new Map(participants.map(p => [p.SessionID, p.Name]));
  const ids = new Set();
  const timed = [];
  let unknownTimes = 0;
  for (const message of messages) {
    ids.add(message.SessionID);
    for (const id of recipients(message)) ids.add(id);
    const time = Date.parse(message.CreatedAt);
    if (Number.isFinite(time)) timed.push({message,time}); else unknownTimes++;
  }
  const lanes = [...ids].sort().map(id => ({id, name:id === BROADCAST ? 'Everyone' : names.get(id) || 'Session'}));
  const sought=timed.find(item=>item.message.ID===seekMessage);
  if(sought) {lanePage=Math.floor(lanes.findIndex(lane=>lane.id===sought.message.SessionID)/LANE_LIMIT);start=sought.time-span/2;}
  lanePage = clampPage(lanePage, lanes.length, LANE_LIMIT);
  const visible = lanes.slice(lanePage * LANE_LIMIT, (lanePage + 1) * LANE_LIMIT);
  const shown = new Set(visible.map(lane => lane.id));
  timed.sort((a,b) => a.time-b.time || a.message.Sequence-b.message.Sequence || a.message.ID.localeCompare(b.message.ID));
  const min = timed[0]?.time ?? 0, max = timed.at(-1)?.time ?? min;
  const duration = span > 0 ? span : Math.max(1000, max - min);
  const from = span > 0 ? Math.max(min, Math.min(start ?? min, Math.max(min,max-duration))) : min;
  const to = from + duration;
  const routes = [];
  let inRange = 0;
  for (const {message,time} of timed) {
    if (time < from || time > to) continue;
    inRange++;
    for (const target of recipients(message)) {
      if (!shown.has(message.SessionID) && !shown.has(target)) continue;
      routes.push({message,source:message.SessionID,target,time,position:(time-from)/duration});
    }
  }
  if(spacing === 'order') routes.sort((a,b)=>a.message.Sequence-b.message.Sequence || a.message.ID.localeCompare(b.message.ID));
  if(sought)arrowPage=Math.floor(routes.findIndex(route=>route.message.ID===seekMessage)/ARROW_LIMIT);
  arrowPage = clampPage(arrowPage,routes.length,ARROW_LIMIT);
  return {lanes,visible,lanePage,arrowPage,spacing,routes:routes.slice(arrowPage*ARROW_LIMIT,(arrowPage+1)*ARROW_LIMIT),routeCount:routes.length,inRange,unknownTimes,from,to,min,max};
}

function svg(tag, attributes = {}, text) {
  const element = document.createElementNS(NS,tag);
  for (const [key,value] of Object.entries(attributes)) element.setAttribute(key,String(value));
  if (text !== undefined) element.textContent = String(text);
  return element;
}
// Recipient occurrence is offset for readability, not a recorded receipt time.
export function occurrenceLink(x, y1, y2) {
  const targetX=x+64, radius=18;
  const length=Math.hypot(targetX-x,y2-y1), dx=(targetX-x)/length, dy=(y2-y1)/length;
  return {targetX,radius,d:`M ${x+dx*radius} ${y1+dy*radius} L ${targetX-dx*radius} ${y2-dy*radius}`};
}
export function renderSequence(container, layout, {selected = '', messages = [], onSelect} = {}) {
  const ordered = layout.spacing === 'order';
  const width = Math.max(680,container.clientWidth || 920,ordered ? 290+Math.max(0,layout.routes.length-1)*140 : 0), left = 165, right = width-110, top = 90, gap = 88;
  const routeX = new Map(layout.routes.map((route,i)=>[route,ordered ? left+i*140 : left+route.position*(right-left)]));
  const positions = new Map(layout.visible.map((lane,i) => [lane.id,top+i*gap]));
  const outsideY = top+layout.visible.length*gap;
  const hasOutside = layout.routes.some(route => !positions.has(route.source) || !positions.has(route.target));
  const height = Math.max(210,outsideY+(hasOutside?75:10));
  const canvas = svg('svg',{viewBox:`0 0 ${width} ${height}`,role:'group','aria-label':'Agent lanes with addressed messages positioned at their send time'});
  canvas.setAttribute('width',width);
  canvas.setAttribute('class',ordered ? 'sequence-ordered' : 'sequence-timed');
  canvas.setAttribute('height',height);
  canvas.setAttribute('aria-label',ordered ? 'Agent messages in log sequence order, evenly spaced; horizontal distance does not represent elapsed time' : 'Agent lanes with addressed messages positioned at their send time');
  const marker = svg('marker',{id:'sequence-arrow',viewBox:'0 0 10 10',refX:9,refY:5,markerWidth:8,markerHeight:8,orient:'auto',markerUnits:'userSpaceOnUse'});
  marker.append(svg('path',{d:'M 1 1 L 9 5 L 1 9 z',fill:'#486ad3'}));
  const defs = svg('defs'); defs.append(marker); canvas.append(defs);
  if(ordered) for(const route of layout.routes) {
    const x=routeX.get(route);
    canvas.append(svg('line',{x1:x,x2:x,y1:45,y2:height-25,'class':'sequence-grid'}),svg('text',{x,y:23,'text-anchor':'middle','class':'sequence-name'},`#${route.message.Sequence}`),svg('text',{x,y:40,'text-anchor':'middle','class':'sequence-time'},timeLabel(route.time)));
  }
  else for(let i=0;i<=4;i++) {
    const x=left+(right-left)*i/4;
    canvas.append(svg('line',{x1:x,x2:x,y1:45,y2:height-25,'class':'sequence-grid'}),svg('text',{x,y:25,'text-anchor':'middle','class':'sequence-time'},timeLabel(layout.from+(layout.to-layout.from)*i/4)));
  }
  for (const lane of layout.visible) {
    const y=positions.get(lane.id);
    canvas.append(svg('line',{x1:left,x2:right,y1:y,y2:y,'class':'sequence-lane'}));
    canvas.append(svg('circle',{cx:15,cy:y-5,r:5,fill:agentColor(lane.id)}));
    const label=svg('text',{x:28,y:y-3,'class':'sequence-name'},lane.name.length>18?lane.name.slice(0,17)+'…':lane.name);
    label.append(svg('title',{},`${lane.name} · ${lane.id}`));
    canvas.append(label,svg('text',{x:28,y:y+14,'class':'sequence-id'},lane.id===BROADCAST?'Shared context':shortID(lane.id)));
  }
  if(hasOutside) canvas.append(svg('line',{x1:left,x2:right,y1:outsideY,y2:outsideY,'class':'sequence-external'}),svg('text',{x:15,y:outsideY-3,'class':'sequence-name'},'Off-page agents'),svg('text',{x:15,y:outsideY+14,'class':'sequence-id'},'Names in route list'));
  const related = selected ? threadIDs(messages,selected) : new Set();
  // Paint selection last so overlapping timestamps remain inspectable. The
  // companion route list always exposes every route, including exact ties.
  const routes=[...layout.routes].sort((a,b)=>Number(a.message.ID===selected)-Number(b.message.ID===selected));
  for (const route of routes) {
    const instance = route.message.agent_instance;
    const claim = instance ? ` · ${instance.label || instance.id} (self-reported instance: ${instance.id})` : '';
    const x=routeX.get(route),y1=positions.get(route.source)??outsideY,y2=positions.get(route.target)??outsideY;
    const {targetX,radius,d}=occurrenceLink(x,y1,y2);
    const isSelected=route.message.ID===selected;
    const routeID=`sequence-route-${encodeURIComponent(route.message.ID)}-${encodeURIComponent(route.target)}`;
    const group=svg('g',{id:routeID,'class':`sequence-message${isSelected?' selected':related.has(route.message.ID)?' related':''}`,role:'button',tabindex:0,'aria-pressed':String(isSelected),'aria-label':`Message ${route.message.Sequence}, ${route.source}${claim} to ${route.target===BROADCAST?'Everyone':route.target}, sent ${timeLabel(route.time)}`});
    group.append(svg('title',{},`#${route.message.Sequence} · ${route.source}${claim} → ${route.target}\n${route.message.Body || 'Structured message'}\nSent ${new Date(route.time).toISOString()} · Read status unknown`),svg('path',{d,'class':'sequence-hit'}),svg('path',{d,'class':'sequence-arrow','marker-end':'url(#sequence-arrow)'}));
    for(const [id,cx,cy,role] of [[route.source,x,y1,'Sender'],[route.target,targetX,y2,'Recipient']]) {
      const name=layout.lanes.find(lane=>lane.id===id)?.name || 'Session';
      const circle=svg('circle',{cx,cy,r:radius,fill:agentColor(id),'class':'sequence-occurrence'});
      circle.append(svg('title',{},`${role}: ${name} · ${id}`));
      group.append(circle,svg('text',{x:cx,y:cy+4,'text-anchor':'middle','class':'sequence-occurrence-label','aria-hidden':'true'},id===BROADCAST?'All':name.slice(0,2).toUpperCase()));
    }
    if(isSelected)group.append(svg('text',{x:x+8,y:(y1+y2)/2-7,'class':'sequence-selected-label'},`#${route.message.Sequence}`));
    const select=()=>{onSelect?.(route.message.ID);document.getElementById(routeID)?.focus({preventScroll:true});};
    group.addEventListener('click',select);
    group.addEventListener('keydown',event=>{if(event.key==='Enter'||event.key===' '){event.preventDefault();select();}});
    canvas.append(group);
  }
  if(!layout.routes.length)canvas.append(svg('text',{x:(left+right)/2,y:height-28,'text-anchor':'middle','class':'sequence-time'},'No routes in this time range and lane page.'));
  container.replaceChildren(canvas);
}
