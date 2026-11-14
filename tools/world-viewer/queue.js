// A queue of scene actions derived from explicit protocol events, not agent execution.
import {goToRoom, roomAt, react} from './life.js';
export function planMessage(message) {
 const schema=message.envelope?.schema,recipients=message.envelope?.recipient_session_ids||[];
 const plans={
  'comitia.assignment.v1':{title:'Assignment received',room:'workshop',gesture:'receive',actors:recipients},
  'comitia.review-request.v1':{title:'Review requested',room:'review',gesture:'review',actors:recipients},
  'comitia.work-submitted.v1':{title:'Work submitted',room:'commons',gesture:'send',actors:[message.SessionID]},
  'comitia.verification-result.v1':{title:'Verification reported',room:'commons',gesture:'result',actors:[message.SessionID]},
  'comitia.evaluation-result.v1':{title:'Evaluation reported',room:'commons',gesture:'result',actors:[message.SessionID]}
 };
 const plan=plans[schema];
 if(!plan)return [];
 return [...new Set(plan.actors)].map(actor=>({id:message.ID+':'+actor,actor,messageID:message.ID,sequence:message.Sequence,taskID:message.envelope.task_id||'',title:plan.title,room:plan.room,gesture:plan.gesture,state:'queued',phaseStart:0}));
}
export class SceneQueue {
 items=[];
 revision=0;
 omitted=0;
 enqueue(message,time){
  for(const item of planMessage(message)){
   if(this.items.some(x=>x.id===item.id))continue;
   if(this.items.filter(x=>!['shown','outside'].includes(x.state)).length>=120){this.omitted++;continue;}
   this.items.push({...item,readyAt:time+1.8});this.revision++;
  }
  const completed=this.items.filter(x=>['shown','outside'].includes(x.state));
  if(completed.length>40){const remove=new Set(completed.slice(0,-40).map(x=>x.id));this.items=this.items.filter(x=>!remove.has(x.id));}
 }
 clear(){this.items=[];this.omitted=0;this.revision++;}
 rebind(){for(const item of this.items)if(['moving','highlighting','outside'].includes(item.state))item.state='queued';this.revision++;}
 tick(nodes,time,{studio,reduced}){
  const busy=new Set();
  for(const item of this.items){
   if(['shown','outside'].includes(item.state))continue;
   const r=nodes.get(item.actor);
   if(!r){item.state='outside';this.revision++;continue;}
   if(busy.has(item.actor))continue;busy.add(item.actor);
   if(time<item.readyAt)continue;
   if(item.state==='queued'){
    r.queueID=item.id;
    if(studio&&roomAt(r.actor.position)!==item.room){
     goToRoom(r,item.room,time,[...nodes.keys()].indexOf(item.actor));
     if(reduced){const destination=r.path.at(-1);if(destination)r.actor.position.copy(destination);r.path=[];}
     item.state=r.path.length?'moving':'highlighting';
    }else item.state='highlighting';
    item.phaseStart=time;
    if(item.state==='highlighting')react(r,item.gesture,time);
    this.revision++;
   }
   if(item.state==='moving'&&!r.path.length){item.state='highlighting';item.phaseStart=time;react(r,item.gesture,time);this.revision++;}
   if(item.state==='highlighting'&&time-item.phaseStart>(reduced?.2:2.6)){item.state='shown';r.queueID='';this.revision++;}
  }
 }
 get pending(){return this.items.filter(x=>['queued','moving','highlighting'].includes(x.state));}
}
