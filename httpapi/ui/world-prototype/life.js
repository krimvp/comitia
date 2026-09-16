// Visual choreography only. These actions do not run agents or mutate Comitia.
import * as THREE from 'three';
export const rooms = {
 commons: {name:'Commons', x:-3.8, z:0, color:'#dce6c8'},
 workshop: {name:'Workshop', x:3.7, z:-3.2, color:'#d4e3df'},
 review: {name:'Review room', x:3.7, z:3.2, color:'#e5dce8'}
};
export function roomAt(p){return p.x<.4?'commons':p.z<.3?'workshop':'review';}
export function initLife(r,i){
 r.home=r.actor.position.clone();r.room=roomAt(r.home);r.path=[];r.targetRoom=r.room;
 r.nextWander=7+i*3.6;r.action='idle';r.actionUntil=0;r.activity='At their station';r.walkPhase=i;
}
export function react(r,kind,time){
 if(!r)return;
 r.action=kind;r.actionUntil=time+({send:2.1,receive:2.2,review:3.4,result:3.1}[kind]||2);
 r.pulse=1;
}
function door(id){return new THREE.Vector3(.4,0,id==='workshop'?-2.0:2.1);}
export function goToRoom(r,id,time,index=0){
 if(!r||!rooms[id])return;
 const from=roomAt(r.actor.position),target=rooms[id];
 const offset=(index%3-1)*.72;
 const goal=new THREE.Vector3(target.x+offset,0,target.z+(index%2?-.75:.75));
 const path=[];
 if(r.actor.position.distanceTo(r.home)<.8){const front=r.actor.position.clone();front.z+=.95;front.y=0;path.push(front);}
 // All inter-room travel passes through the commons and the doorway openings.
 if(from!==id){
  if(from!=='commons'){
   const exit=door(from);path.push(new THREE.Vector3(1.3,0,exit.z),new THREE.Vector3(-.6,0,exit.z));
  }
  if(id!=='commons'){
   const entrance=door(id);path.push(new THREE.Vector3(-.6,0,entrance.z),new THREE.Vector3(1.3,0,entrance.z));
  }
 }
 path.push(goal);r.path=path;r.targetRoom=id;r.activity='Going to '+target.name;r.nextWander=time+19+index*1.4;
}
export function updateLife(r,dt,time,{reduced,roaming,studio,index}){
 if(!studio){r.path=[];r.activity=r.actionUntil>time?r.action:'At their station';}
 if(reduced){r.path=[];r.action='idle';r.actionUntil=0;r.nextWander=time+8+index*2;}
 if(studio&&roaming&&!reduced&&!r.path.length&&time>r.nextWander&&time>r.actionUntil){
  const choices=['commons','workshop','review'];const current=roomAt(r.actor.position);
  const next=choices[(choices.indexOf(current)+1+(index%2))%3];goToRoom(r,next,time,index);
 }
 const moving=r.path.length>0&&!reduced;
 let walking=false;
 if(moving&&time>r.actionUntil){
  const goal=r.path[0],delta=goal.clone().sub(r.actor.position);delta.y=0;const distance=delta.length();
  if(distance<.08){r.actor.position.x=goal.x;r.actor.position.z=goal.z;r.path.shift();if(!r.path.length){r.room=r.targetRoom;r.activity='In '+rooms[r.room].name;react(r,'receive',time);}}
  else{
   walking=true;const speed=1.15;delta.normalize();r.actor.position.addScaledVector(delta,Math.min(distance,speed*dt));
   const targetAngle=Math.atan2(delta.x,delta.z);let angle=targetAngle-r.actor.rotation.y;angle=Math.atan2(Math.sin(angle),Math.cos(angle));r.actor.rotation.y+=angle*Math.min(dt*7,1);
   r.walkPhase+=dt*9;
  }
 }
 const active=!reduced&&time<r.actionUntil;const action=active?r.action:'idle';
 if(!walking){let angle=.38-r.actor.rotation.y;angle=Math.atan2(Math.sin(angle),Math.cos(angle));r.actor.rotation.y+=angle*Math.min(dt*3,1);}
 const breathe=reduced?0:Math.sin(time*2+r.phase)*.017;
 r.robot.position.y=r.base+breathe+(walking?Math.abs(Math.sin(r.walkPhase))*.07:0);
 r.robot.rotation.z=walking?Math.sin(r.walkPhase)*.045:0;
 r.head.rotation.x=0;r.head.rotation.z=0;
 r.arms.forEach((arm,i)=>{arm.rotation.x=walking?Math.sin(r.walkPhase+i*Math.PI)*.65:0;arm.rotation.z=(i===0?1:-1)*.15;});
 r.legs.forEach((leg,i)=>{leg.rotation.x=walking?Math.sin(r.walkPhase+i*Math.PI)*.42:0;});
 if(action==='send'){r.arms[1].rotation.z=-2.15+Math.sin(time*13)*.3;r.head.rotation.z=-.10;r.activity='Sending a message';}
 if(action==='receive'){r.head.rotation.x=Math.sin(time*9)*.18;r.arms[0].rotation.z=.65;r.activity='Message addressed here';}
 if(action==='review'){r.head.rotation.z=Math.sin(time*2)*.17;r.arms[0].rotation.x=-1.05;r.arms[0].rotation.z=.45;r.activity='Review message';}
 if(action==='result'){r.arms[0].rotation.z=2.4;r.arms[1].rotation.z=-2.4;r.robot.position.y+=Math.abs(Math.sin(time*9))*.24;r.activity='Result shared';}
 if(!active&&!walking&&!r.path.length){r.activity=studio?'In '+rooms[roomAt(r.actor.position)].name:'At their station';if(!reduced)r.head.rotation.y=Math.sin(time*.65+r.phase)*.12;}
 r.lastWalking=walking;return {walking,action};
}
