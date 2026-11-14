// The tab stores only an expiring viewer session, never the Comitia credential.
export function takeLauncherKey(location,history){
 const key=new URLSearchParams(location.hash.slice(1)).get('viewer-key');
 if(key)history.replaceState(null,'',location.pathname+location.search);
 return key;
}
export async function viewerSession(key,{fetch,storage}){
 let session=storage.getItem('comitia-viewer-session')||'';
 if(key){
  const response=await fetch('/viewer/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({key}),credentials:'omit',redirect:'error',signal:AbortSignal.timeout(5000)});
  if(!response.ok)throw new Error('Local viewer sign-in failed. Open the private launcher again.');
  session=(await response.json()).session;
  storage.setItem('comitia-viewer-session',session);
 }
 const headers=session?{'X-Comitia-Viewer-Session':session}:{};
 const response=await fetch('/viewer/session',{headers,cache:'no-store',credentials:'omit',signal:AbortSignal.timeout(5000)});
 if(!response.ok||!(await response.json()).automatic){storage.removeItem('comitia-viewer-session');return '';}
 return session;
}
