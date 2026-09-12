"""Render the completed experiment, retaining environmental and calibration data."""
import collections,json,statistics
from pathlib import Path
H=Path(__file__).resolve().parent;ROOT=H.parents[1];PROJECT=ROOT.parent
analysis=json.loads((H/'analysis.json').read_text());rows=analysis['trials'];assert len(rows)==18
modes=['single-agent','multi-agent','comitia'];labels={'single-agent':'One engineer','multi-agent':'Five roles + files','comitia':'Five roles + Comitia'}
summary=analysis['matched_without_host_affected_pairs'];assert set(summary)==set(modes)
mix=collections.Counter(case.replace('order-system-','') for case,rep in analysis['matched_pairs'])
control=json.loads((H/'awake-control-results.json').read_text());controls=control['runs'];assert len(controls)==3
control_analysis=json.loads((H/'awake-control-analysis.json').read_text());control_by_mode={r['mode']:r for r in control_analysis['trials']}
calibration=[json.loads(p.read_text()) for p in (ROOT/'evaluation/recovery/observations').glob('*.json')]
link=lambda label,p:f'[{label}]({p})'
lines=['# Comitia: larger engineering tasks and recovery','',
'**Finding:** Comitia successfully preserved completed work across controlled restarts and rejected stale completions. Its code score tied the durable file-based team (97.7%); its overall score was 2.1 percentage points higher because of one handoff. It cost 2.1% more and took 2.4% longer than that team in matched main runs. These small differences do not demonstrate that Comitia improves model reasoning. Compared with the sequential engineer, Comitia was 7.5% faster and 8.4% more expensive, with a 4.2-point higher overall score. The separate clean control reversed the quality ordering (sequential 12/12, files 11/12, Comitia 10/12), further limiting any claim of a stable advantage.','',
'This extension executes one multi-file engineering workload under three fault profiles: clean operation, process death after publishing a handoff, and lease takeover with a delayed stale result. Five roles produce an architecture, two modules, integration, and final review. Two implementers run concurrently in team modes; the sequential engineer uses the same stages with accumulated context.','',
'All modes use `claude-haiku-4-5-20251001` through Claude Code 2.1.263. The larger budget is 600 seconds per model call and 4,000 per trial. The main series contains 18 trials (two repetitions per mode/condition); a separately labeled three-mode clean control follows. Two earlier 225-second calibration trials are preserved and not pooled.','',
'## Comparable main-series results','',
f"{sum(r['host_sleep_affected'] for r in rows)} trials were affected by closed-lid sleep. Ordinary-performance comparisons below exclude entire affected condition/repetition triples, leaving {len(analysis['matched_pairs'])} matched conditions per mode. All 18 original rows remain in the evidence. These are small-sample pilot results, not broad significance claims.",'',
'| Workflow | Checks | Code checks | Full passes | Completed | Captured cost | Input / output tokens | Mean elapsed | Mean coordination |',
'|---|---:|---:|---:|---:|---:|---:|---:|---:|']
for m in modes:
 d=summary[m];lines.append(f"| {labels[m]} | {d['mean_quality']:.1%} | {d['mean_code_quality']:.1%} | {d['passes']}/{d['n']} | {d['completed']}/{d['n']} | ${d['cost_usd']:.4f} | {d['input_tokens']:,} / {d['output_tokens']:,} | {d['mean_wall_seconds']:.1f}s | {d['mean_storage_seconds']:.3f}s |")
lines+=['','Matched runs per mode: '+', '.join(f'{name}: {n}' for name,n in sorted(mix.items()))+'. Metrics weight these runs equally. Costs and tokens are totals for the matched runs; elapsed and coordination times are per-trial means. Checks are 12 equally weighted groups: 11 code-behavior groups and one handoff length/topic group. A full pass requires all 12. Communication correctness is also inspected manually; passing the automated rubric is not proof of flawless writing. Costs are recorded list-price equivalents, not subscription charges. Cost completeness for these matched rows: '+', '.join(f"{labels[m]} {summary[m]['cost_complete']}/{summary[m]['n']}" for m in modes)+'.','',
'## Separate clean control','',
'This repeats all three modes after the initial clean triple encountered host sleep. It is not a selective retry of only a failed mode and is not pooled into the main series.','',
'| Workflow | Checks | Code checks | Status | Captured cost | Input / output tokens | Elapsed | Host sleep |','|---|---:|---:|---|---:|---:|---:|---|']
for r in controls:
 q=sum(c['earned'] for c in r['quality'])/12;d=control_by_mode[r['mode']]
 lines.append(f"| {labels[r['mode']]} | {q:.1%} | {d['code_quality']:.1%} | {r['status']} | ${r['metadata']['reported_cost_usd']:.4f} | {d['input_tokens']:,} / {d['output_tokens']:,} | {r['wall_seconds']:.1f}s | {'yes' if d['host_sleep_affected'] else 'no'} |")
lines+=['','The clean control reversed the main-series quality ordering: the sequential engineer passed 12/12, the file-based team 11/12, and Comitia 10/12. Comitia finished in 404.4 seconds versus 439.6 and 442.0 seconds, respectively. Its reviewer corrected handoff length but removed explicit database-transaction guidance, and missed an empty-order-ID validation bug. This single control reinforces that the small main-series lead is not a stable quality benefit.','',
'## Recovery and review','']
for m in modes:
 eligible=[r for r in rows if r['mode']==m and r['recovery_verified'] is not None and not r['host_sleep_affected']]
 d=summary[m]
 lines.append(f"- {labels[m]}: {sum(r['recovery_verified'] is True for r in eligible)}/{len(eligible)} controlled recoveries verified outside host-sleep interruptions. In matched completed trials, review improved {d['reviewer_improved']}, worsened {d['reviewer_regressed']}, and tied {d['reviewer_tied']} aggregate check scores.")
leases=[r for r in rows if 'lease-takeover' in r['case'] and not r['host_sleep_affected']]
lines+=['',f"The {len(leases)} takeover trials spent {sum(r['discarded_draft_tokens'] for r in leases):,} tokens (${sum(r['discarded_draft_cost_usd'] for r in leases):.4f}) on deliberately unpublished drafts. That work is included in costs. Recovery preserved completed architecture/outbox work; a replacement recomputed the unpublished ledger. In verified takeovers, stale completion was rejected without changing the replacement result.",'',
'Comitia permits message publication independently of task completion. The adapter therefore accepts only the message associated with the completed lease generation; a rejected stale task completion alone is not a guarantee that no stale message was sent. The file baseline implements its own locks, durable writes, deduplication, leases, and generation checks.','',
'## Individual trials','',
'| Condition | Rep | Workflow | Checks | Status | Host sleep | Seconds | Captured cost | Recovery |','|---|---:|---|---:|---|---|---:|---:|---|']
for r in rows:
 lines.append(f"| {r['case'].replace('order-system-','')} | {r['replicate']} | {labels[r['mode']]} | {r['quality']:.1%} | {r['status']} | {'yes' if r['host_sleep_affected'] else 'no'} | {r['wall_seconds']:.1f} | ${r['cost_usd']:.4f} | {r['recovery_verified']} |")
lines+=['','macOS monotonic elapsed time excludes host sleep. The affected calls had much larger civil-time spans, captured separately. Returned usage is retained; missing timeout usage is incomplete rather than free. Unexpected long pauses exposed a real adapter limitation: expired claims stop the workflow rather than triggering automatic reclaim/recompute. Both stores protected ownership, but neither surrounding workflow self-healed from those unplanned pauses.','',
'## Failure inspection','']
lines+=['The code failures included accepting empty order IDs, calculating claim expiry from a later caller’s requested lease instead of the stored lease, and accepting completion after expiry. Review repaired some integration rollback mistakes but frequently left writing constraints unmet. These generated-application defects are separate from Comitia’s coordination-store behavior.','',
'A post-hoc fault-injection diagnostic replaces enqueue with a function that raises ValueError or RuntimeError, before or after adding an item. This extends the primary grader, which only exercises a normal conflicting-key ValueError. It is applied uniformly to available final outputs and never changes original scores or model prompts. Several implementations catch only ValueError or restore only the ledger, so high code scores do not establish robust rollback under broader failures. The reference implementation passes all seven rollback probes.','']
for prefix,label in [('', 'Main series'),('awake-control-', 'Separate clean control')]:
 diagnostics=json.loads((H/(prefix+'rollback-diagnostics.json')).read_text())
 failures=[r['trial'] for r in diagnostics if any(isinstance(v,dict) and not v.get('passed') for k,v in r.get('checks',{}).items() if k.startswith('injected_'))]
 lines.append(f"{label}: {len(failures)}/{len(diagnostics)} final solutions fail at least one injected enqueue-error probe. Affected trials: "+', '.join(failures)+'.')
lines+=['']
for r in rows:
 if not r['host_sleep_affected'] and (r['failed_checks'] or r['error']):lines.append('- '+r['case']+f" / rep {r['replicate']} / {labels[r['mode']]}: "+('; '.join(f'{k}: {v}' for k,v in r['failed_checks'].items()) or str(r['error'])))
lines+=['',link('Manual audit with root causes and documentation examples',H/'manual-audit.md')+'. Reviewer fixes and newly introduced failures, per-role usage, retained/lost work, and actual implementation overlap are in '+link('the detailed analysis',H/'analysis.json')+'.','',
'## Interpretation','',
'Comitia helps when a workflow needs saved handoffs, exclusive ownership and safe worker replacement: those mechanisms worked in the controlled recovery trials. The comparison does not show an advantage over a baseline that already implements those mechanisms itself. Comitia packages coordination infrastructure; it does not eliminate the need for a recovery policy around unexpected lease expiry. Its measured storage overhead was less than one second per matched trial; model generation consumed minutes.','',
'The earlier 90 short-task trials also found no quality advantage over ordinary worker/reviewer transport. On simple factual tasks all modes were correct, while the team workflows roughly doubled time and captured model cost. Larger tasks allow useful overlap, but extra roles and review still consume resources and may preserve mistakes. The new sequential control includes the same five stages and self-review, addressing the earlier lack of a self-review comparison.','',
'## Scope and evidence','',
'Each stage is a fresh model session. The sequential engineer receives accumulated earlier outputs; restart/resume means restoring the workflow from durable results, not resuming a native model session. The model sessions have no tools and follow a fixed role graph. The adapter executes generated files and supplies four public smoke-check results to the reviewer; eight checks remain hidden. This tests larger, multi-step work with real process restarts, not autonomous tool choice, repository navigation, production deployment, or arbitrary crash points. Deliberate SIGKILL barriers occur after model responses have returned, so their discarded usage can be counted. Mid-token cancellation, network partitions, and multi-host operation are not measured.','',
link('Methodology and reproduction',H/'README.md')+' · '+link('Official harness aggregate',H/'results.md')+' · '+link('All original observations',H/'results.json')+' · '+link('Clean-control results',H/'awake-control-results.md')+' · '+link('Calibration evidence',ROOT/'evaluation/recovery/analysis.md')+' · '+link('Earlier six-category evaluation',PROJECT/'comitia-evaluation-report.md'),'',
f"The extension recorded {len(rows)+len(controls)+len(calibration)} trials: 18 main, 3 separate clean controls, and 2 calibration trials. Main plus control used {sum(r['model_calls_started'] for r in rows)+sum(r['model_calls_started'] for r in control_analysis['trials'])} model-client calls and captured ${sum(r['cost_usd'] for r in rows)+sum(r['cost_usd'] for r in control_analysis['trials']):.4f} in list-price usage; the host-affected timeout has missing usage, so this is a lower bound. Calibration is separate. Eight grader/recovery tests pass, and official summaries were regenerated from saved observations.",'',
'Production Comitia code was unchanged. Reusable evaluation definitions, adapters, grader checks, recovery tests, analysis, and documentation were added locally; nothing was committed or pushed.','']
comparison=analysis['matched_paired_comparisons'].get('comitia vs multi-agent')
if comparison:
 lo,hi=comparison['quality_difference_95_ci']
 text=f"Comitia minus the file-based team: {comparison['mean_quality_difference']*100:+.2f} percentage points in check score; paired bootstrap 95% interval [{lo*100:+.2f}, {hi*100:+.2f}]. This interval only resamples the small fixed set of matched runs; it is not a production-wide confidence claim."
 idx=lines.index('## Separate clean control');lines[idx:idx]=[text,'']
(PROJECT/'comitia-recovery-evaluation-report.md').write_text('\n'.join(lines))
print(PROJECT/'comitia-recovery-evaluation-report.md')
