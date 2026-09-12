"""Prevent new matrix cells during repeated short macOS dark wakes.

External experiment controller; does not alter frozen prompts, adapters or scores.
One already-running cell can still be affected by a lid-close sleep. Preserve and
flag it. Never silently retry a cell. SIGSTOP applies only to the outer scheduler;
current runner/model subprocesses are left to produce their normal observations.
"""
import json,os,signal,sys,time
from pathlib import Path
pid=int(sys.argv[1]);dest=Path(__file__).resolve().parent/'awake-guard-events.jsonl'
def record(**kw):
 with dest.open('a') as f:f.write(json.dumps(dict(at=time.time(),**kw))+'\n');f.flush()
prevwall=time.time();prevmono=time.monotonic();paused=False;stable_since=prevmono
record(action='started',matrix_pid=pid)
try:
 while True:
  try:os.kill(pid,0)
  except ProcessLookupError:break
  time.sleep(1);wall=time.time();mono=time.monotonic();gap=(wall-prevwall)-(mono-prevmono)
  if gap>10:
   stable_since=mono
   if not paused:os.kill(pid,signal.SIGSTOP);paused=True
   record(action='host_sleep_gap_scheduler_paused',sleep_gap_seconds=gap)
  if paused and mono-stable_since>=90:
   os.kill(pid,signal.SIGCONT);paused=False;record(action='scheduler_resumed_after_90s_awake')
  prevwall=wall;prevmono=mono
finally:
 if paused:
  try:os.kill(pid,signal.SIGCONT)
  except ProcessLookupError:pass
 record(action='finished')
