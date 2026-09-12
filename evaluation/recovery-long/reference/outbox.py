from copy import deepcopy
class Outbox:
 def __init__(self,snapshot=None):self.items=deepcopy(snapshot) if snapshot is not None else []
 def enqueue(self,key,payload):
  if not isinstance(key,str) or not key:raise ValueError('key')
  for i in self.items:
   if i['key']==key:
    if i['payload']!=payload:raise ValueError('conflict')
    return False
  self.items.append(dict(key=key,payload=deepcopy(payload),version=0,worker=None,expiry=0,done=False));return True
 def claim(self,worker,now,lease):
  if not isinstance(worker,str) or not worker or lease<=0:raise ValueError('claim')
  for i in self.items:
   if not i['done'] and (i['worker'] is None or now>=i['expiry']):
    i.update(worker=worker,expiry=now+lease,version=i['version']+1)
    return {k:deepcopy(i[k]) for k in ('key','payload','version')}
  return None
 def complete(self,key,worker,version,now):
  for i in self.items:
   if i['key']==key and not i['done'] and i['worker']==worker and i['version']==version and now<i['expiry']:
    i['done']=True;return True
  return False
 def snapshot(self):return deepcopy(self.items)
