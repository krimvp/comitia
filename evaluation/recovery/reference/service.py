from ledger import Ledger
from outbox import Outbox
class Service:
 def __init__(self,snapshot=None):
  self.ledger=Ledger(snapshot['ledger'] if snapshot is not None else None);self.outbox=Outbox(snapshot['outbox'] if snapshot is not None else None)
 def ingest(self,event):
  before=self.snapshot()
  try:
   if not self.ledger.apply(event):return False
   self.outbox.enqueue(event['id'],event);return True
  except Exception:
   self.ledger=Ledger(before['ledger']);self.outbox=Outbox(before['outbox']);raise
 def dispatch(self,worker,now,lease,sink):
  item=self.outbox.claim(worker,now,lease)
  if item is None:return False
  sink(item['key'],item['payload']);return self.outbox.complete(item['key'],worker,item['version'],now)
 def snapshot(self):return {'ledger':self.ledger.snapshot(),'outbox':self.outbox.snapshot()}
