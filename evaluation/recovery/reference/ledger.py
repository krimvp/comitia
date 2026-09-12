from copy import deepcopy
class Ledger:
 def __init__(self,snapshot=None):
  self.state=deepcopy(snapshot) if snapshot is not None else {'orders':{},'events':{}}
 def apply(self,e):
  if not isinstance(e,dict) or set(e)!={'id','order_id','kind','amount'} or any(not isinstance(e[k],str) or not e[k] for k in ('id','order_id','kind')) or type(e['amount']) is not int or e['amount']<=0 or e['kind'] not in ('reserve','capture','refund'):raise ValueError('invalid event')
  if e['id'] in self.state['events']:
   if self.state['events'][e['id']]!=e:raise ValueError('conflicting event')
   return False
  orders=self.state['orders'];oid=e['order_id'];a=e['amount'];k=e['kind']
  if k=='reserve':
   if oid in orders:raise ValueError('existing order')
   orders[oid]={'reserved':a,'captured':0,'refunded':0}
  else:
   if oid not in orders:raise ValueError('unknown order')
   o=orders[oid]
   if k=='capture':
    if o['captured']+a>o['reserved']:raise ValueError('over capture')
    o['captured']+=a
   else:
    if o['refunded']+a>o['captured']:raise ValueError('over refund')
    o['refunded']+=a
  self.state['events'][e['id']]=deepcopy(e);return True
 def balances(self,oid):
  o=deepcopy(self.state['orders'][oid]);o.update(available=o['reserved']-o['captured'],net=o['captured']-o['refunded']);return o
 def snapshot(self):return deepcopy(self.state)
