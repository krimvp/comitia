// Safe text rendering: message bodies and payloads are never interpreted as HTML.
export function createReader({name,targets,typeOf,getMessages,onSelect}) {
 const $=id=>document.getElementById(id);
 let selectedID='';
 function element(tag,text,cls){const e=document.createElement(tag);if(text!==undefined)e.textContent=text;if(cls)e.className=cls;return e;}
 function humanize(key){return key.replaceAll('_',' ').replace(/\b\w/,s=>s.toUpperCase());}
 function valueView(value,depth=0){
  if(value===null)return element('span','None');
  if(Array.isArray(value)){const list=element('ul');for(const item of value){const li=element('li');li.append(valueView(item,depth+1));list.append(li);}if(!value.length)list.append(element('li','None'));return list;}
  if(typeof value==='object'){
   if(depth>3)return element('pre',JSON.stringify(value,null,2));
   const dl=element('dl');for(const [key,item] of Object.entries(value)){dl.append(element('dt',humanize(key)));const dd=element('dd');dd.append(valueView(item,depth+1));dl.append(dd);}return dl;
  }
  return element('span',String(value));
 }
 function renderBody(text){
  // Preserve original text, paragraph breaks and fenced code without third-party markup parsing.
  const fragment=document.createDocumentFragment();const chunks=text.split(/(```[\s\S]*?```)/g);
  for(const chunk of chunks){if(!chunk)continue;if(chunk.startsWith('```')&&chunk.endsWith('```')){const content=chunk.slice(3,-3).replace(/^[\w+-]*\n/,'');const pre=element('pre');pre.append(element('code',content));fragment.append(pre);}else{for(const paragraph of chunk.split(/\n\s*\n/)){if(paragraph.trim())fragment.append(element('p',paragraph));}}}
  return fragment;
 }
 function open(message,show=true){
  selectedID=message.ID;onSelect(message);
  $('reader-number').textContent=`MESSAGE ${message.Sequence}`;
  $('reader-from').textContent=name(message.SessionID);
  $('reader-avatar').textContent=name(message.SessionID).slice(0,1).toUpperCase();
  $('reader-to').textContent=targets(message).map(name).join(', ');
  $('reader-kind').textContent=typeOf(message);
  const stamp=new Date(message.CreatedAt);
  $('reader-time').textContent=Number.isNaN(stamp.valueOf())?'Timestamp unavailable':stamp.toLocaleString(undefined,{dateStyle:'medium',timeStyle:'medium'});
  $('reader-body').replaceChildren(renderBody(message.Body||'This message contains a structured payload.'));
  const payload=message.envelope?.payload;
  $('reader-payload-section').hidden=!payload||Object.keys(payload).length===0;
  $('reader-payload').replaceChildren(...(payload?[valueView(payload)]:[]));
  $('reader-metadata').replaceChildren();
  const metadata={ 'Message ID':message.ID,'Sender session':message.SessionID,'Recipient sessions':targets(message).join(', '),'Context':message.ContextID,'Schema':message.envelope?.schema||'Plain text'};
  if(message.envelope?.task_id)metadata.Task=message.envelope.task_id;
  if(message.envelope?.reply_to)metadata['In reply to']=message.envelope.reply_to;
  for(const [key,value]of Object.entries(metadata)){$('reader-metadata').append(element('dt',key),element('dd',value));}
  $('reader-envelope').textContent=JSON.stringify(message.envelope||{},null,2);
  $('reader-technical').open=false;
  const all=getMessages(),index=all.findIndex(m=>m.ID===selectedID);
  $('reader-prev').disabled=index<=0;$('reader-next').disabled=index<0||index>=all.length-1;
  $('reader-position').textContent=`${index+1} of ${all.length} loaded`;
  $('reader-content').scrollTop=0;
  if(show&&!$('message-reader').open)$('message-reader').showModal();
 }
 function navigate(direction){const all=getMessages(),i=all.findIndex(m=>m.ID===selectedID),next=all[i+direction];if(next)open(next);}
 function clear(){selectedID='';$('message-reader').close();for(const id of ['reader-body','reader-payload','reader-metadata','reader-envelope','reader-from','reader-to','reader-time','reader-number','reader-avatar','reader-kind','reader-position'])$(id).replaceChildren();}
 $('reader-close').onclick=()=>$('message-reader').close();
 $('reader-prev').onclick=()=>navigate(-1);$('reader-next').onclick=()=>navigate(1);
 $('message-reader').addEventListener('click',e=>{if(e.target===$('message-reader')){const r=e.target.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)e.target.close();}});
 $('message-reader').addEventListener('keydown',e=>{if(e.key==='ArrowLeft'||e.key==='ArrowRight'){e.preventDefault();e.stopPropagation();navigate(e.key==='ArrowLeft'?-1:1);}});
 return {open,clear};
}
