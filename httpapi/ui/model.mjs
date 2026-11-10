import {taskEvent} from './task-outcomes.mjs';
export const PAGE_SIZE = 100;
export const WINDOW_LIMIT = 5000;
// Polls every 3 s, doubling after each consecutive failed sync up to one minute.
export const POLL_INTERVAL = 3000, POLL_MAX = 60000;
export const pollDelay = failures => Math.min(POLL_MAX, POLL_INTERVAL * 2 ** Math.min(Math.max(0, failures), 5));
export const shortID = value => String(value || '').slice(0, 8);
export const messageType = message => message.envelope?.schema || 'plain-text';
export const typeLabel = schema => schema === 'plain-text' ? 'Message' : schema.replace(/^comitia\./, '').replace(/\.v\d+$/, '').replaceAll('-', ' ');

// Every context transition gets a new generation, including disconnect/reconnect.
export class ConversationState {
  generation = 0;
  contextID = '';
  after = 0;
  messages = [];
  reset(contextID = '', after = 0) {
    this.generation++;
    this.contextID = contextID;
    this.after = after;
    this.messages = [];
    return this.generation;
  }
  append(generation, incoming) {
    if (generation !== this.generation) return false;
    if (!Array.isArray(incoming)) throw new Error('Invalid message response.');
    const byID = new Map(this.messages.map(m => [m.ID, m]));
    for (const message of incoming) {
      if (!message || typeof message.ID !== 'string' || message.ContextID !== this.contextID || !Number.isSafeInteger(message.Sequence) || message.Sequence < 1) {
        throw new Error('Invalid message response.');
      }
      byID.set(message.ID, message);
    }
    this.messages = [...byID.values()].sort((a, b) => a.Sequence - b.Sequence);
    this.after = Math.max(this.after, ...incoming.map(m => m.Sequence));
    return true;
  }
}

export function threadRoot(messages, id) {
  const byID = new Map(messages.map(m => [m.ID, m]));
  const seen = new Set();
  while (byID.has(id) && !seen.has(id)) {
    seen.add(id);
    const parent = byID.get(id).envelope?.reply_to;
    if (!parent || !byID.has(parent)) break;
    id = parent;
  }
  return id;
}

export function threadIDs(messages, id) {
  const ids = new Set([threadRoot(messages, id)]);
  // Replies are guaranteed to reference earlier messages; still tolerate cycles
  // and unordered input without recursion or unbounded traversal.
  let changed = true;
  while (changed) {
    changed = false;
    for (const message of messages) {
      if (ids.has(message.envelope?.reply_to) && !ids.has(message.ID)) {
        ids.add(message.ID);
        changed = true;
      }
    }
  }
  return ids;
}

export const BROADCAST = '@broadcast';
// Actors split a session by its self-reported instance claim. The claim is
// display/routing metadata only; the session remains the authenticated sender.
// Instance IDs cannot contain '#', so the key is unambiguous.
export const actorKey = (session, instance = '') => instance ? `${session}#${instance}` : session;
export function parseActor(key) {
  const i = String(key).indexOf('#');
  return i < 0 ? {session:key, instance:''} : {session:key.slice(0, i), instance:key.slice(i + 1)};
}
export const senderActor = message => actorKey(message.SessionID, message.agent_instance?.id);
// Instance ID -> sessions that claimed it, used to place instance-only recipients.
export function instanceClaims(messages) {
  const claims = new Map();
  for (const message of messages) {
    const id = message.agent_instance?.id;
    if (!id) continue;
    if (!claims.has(id)) claims.set(id, new Set());
    claims.get(id).add(message.SessionID);
  }
  return claims;
}
// Session recipients alone address the whole session. With instance recipients,
// consumers match both lists, so each named session/instance pair is an actor;
// instance-only recipients resolve through observed claims, else stay sessionless.
export function recipientActors(message, claims = new Map()) {
  const sessions = message.envelope?.recipient_session_ids || [], instances = message.envelope?.recipient_instances || [];
  if (!instances.length) return [...new Set(sessions.length ? sessions : [BROADCAST])];
  const keys = [];
  for (const instance of instances) {
    const owners = sessions.length ? sessions : [...(claims.get(instance) || [''])];
    for (const session of owners) keys.push(actorKey(session, instance));
  }
  return [...new Set(keys)];
}

export function filterMessages(messages, {query = '', agent = '', type = '', task = '', thread = '', from = '', to = ''} = {}) {
  const text = query.trim().toLocaleLowerCase();
  const ids = thread ? threadIDs(messages, thread) : null;
  const claims = instanceClaims(messages);
  // A plain session filter still covers every instance of that session.
  const involves = message => parseActor(agent).instance
    ? senderActor(message) === agent || recipientActors(message, claims).includes(agent)
    : message.SessionID === agent || (message.envelope?.recipient_session_ids || []).includes(agent);
  return messages.filter(message => {
    const envelope = message.envelope || {};
    return (!ids || ids.has(message.ID)) &&
      (!agent || involves(message)) &&
      (!type || messageType(message) === type) &&
      (!task || envelope.task_id === task) &&
      (!from || senderActor(message) === from) &&
      (!to || recipientActors(message, claims).includes(to)) &&
      (!text || [message.Body, message.ID, message.SessionID, JSON.stringify(message.agent_instance), JSON.stringify(envelope)].join(' ').toLocaleLowerCase().includes(text));
  });
}

export function interactionGraph(messages, participants = []) {
  const names = new Map(participants.map(p => [p.SessionID, p.Name]));
  const sessions = new Map(participants.map(p => [p.SessionID, {id:p.SessionID,session:p.SessionID,instance:'',name:p.Name,sent:0,received:0,active:true}]));
  const links = new Map();
  const claims = instanceClaims(messages);
  const ensure = id => {
    if (!sessions.has(id)) {
      const {session, instance} = parseActor(id);
      sessions.set(id,{id,session,instance,name:id===BROADCAST?'Everyone':names.get(session)||'Session',sent:0,received:0,active:names.has(session)});
    }
    return sessions.get(id);
  };
  for (const message of messages) {
    const sender = ensure(senderActor(message)); sender.sent++;
    for (const target of recipientActors(message, claims)) {
      ensure(target).received++;
      const key = JSON.stringify([sender.id,target]);
      if (!links.has(key)) links.set(key,{source:sender.id,target,count:0,messageIDs:[]});
      const link = links.get(key);link.count++;link.messageIDs.push(message.ID);
    }
  }
  // A participant whose traffic is all under instance claims needs no empty node.
  const split = new Set([...sessions.values()].filter(n => n.instance).map(n => n.session));
  const nodes = [...sessions.values()].filter(n => n.instance || n.sent || n.received || !split.has(n.id));
  return {nodes,edges:[...links.values()],messageCount:messages.length,claims};
}

export const TASK_STATUS = 'comitia.task-status.v1';
export const TASK_STATES = ['planned', 'in_progress', 'blocked', 'review', 'done', 'failed', 'cancelled'];
export const stateLabel = state => TASK_STATES.includes(state) ? state.replace('_', ' ') : 'unknown state';

// A task-status view never trusts the payload shape; the child context links only
// when it is among the viewer's readable contexts, since readability is otherwise unknown.
export function taskStatus(message, tasks = [], contexts = []) {
  const envelope = message.envelope;
  if (envelope?.schema !== TASK_STATUS) return null;
  const payload = envelope.payload || {}, strings = value => Array.isArray(value) ? value.filter(v => typeof v === 'string') : [];
  const childContextID = typeof payload.child_context_id === 'string' ? payload.child_context_id : '';
  return {
    state: TASK_STATES.includes(payload.state) ? payload.state : 'unknown',
    summary: typeof payload.summary === 'string' ? payload.summary : '',
    taskID: envelope.task_id || '',
    taskTitle: tasks.find(t => t.ID === envelope.task_id)?.Title || '',
    childContextID,
    childContext: childContextID ? contexts.find(c => c.ID === childContextID && c.ParentContextID === message.ContextID) || null : null,
    blockers: strings(payload.blockers),
    links: strings(payload.links),
  };
}

// Keep cursor history, never old message arrays. Sequence jump remains available
// after the oldest of these bounded bookmarks is discarded.
export const HISTORY_LIMIT = 128;
export class HistoryWindows {
  frames = [{after:0, end:null}];
  index = 0;
  get current() { return this.frames[this.index]; }
  reset(after = 0) { this.frames = [{after, end:null}]; this.index = 0; }
  next(after) {
    if (this.index + 1 < this.frames.length) return this.frames[++this.index];
    this.current.end = after;
    this.frames.push({after, end:null});
    if (this.frames.length > HISTORY_LIMIT) this.frames.shift();
    this.index = this.frames.length - 1;
    return this.current;
  }
  previous() {
    if (this.index > 0) return this.frames[--this.index];
    // No bookmark before a direct jump or an evicted boundary: restart history.
    const end = this.current.after;
    this.reset(); this.current.end = end;
    return this.current;
  }
}

export function recipientLabel(envelope = {}, sessionLabel = id => id) {
  const sessions = (envelope.recipient_session_ids || []).map(sessionLabel);
  const instances = envelope.recipient_instances || [];
  if (!sessions.length && !instances.length) return 'Everyone in this context';
  return [sessions.length ? `Sessions: ${sessions.join(', ')}` : '', instances.length ? `Instances (self-reported): ${instances.join(', ')}` : ''].filter(Boolean).join(' · ');
}

// A local formatter seam for future schema descriptors. Only known schema fields
// become previews; raw envelopes remain available for unknown extensions.
const previewFields = new Map([
  ['comitia.assignment.v1', ['instructions']],
  ['comitia.finding.v1', ['severity', 'priority', 'summary']],
  ['comitia.stance.v1', ['stance', 'note']],
  ['comitia.blocker.v1', ['reason']],
  ['comitia.work-submitted.v1', ['summary']],
  ['comitia.verification-result.v1', ['status', 'reason']],
  ['comitia.task-status.v1', ['state', 'summary']],
]);
export function messagePreview(message) {
  const event = taskEvent(message);
  if (event) return event;
  if (typeof message.Body === 'string' && message.Body.trim()) return message.Body;
  const envelope = message.envelope || {}, payload = envelope.payload || {};
  const parts = (previewFields.get(envelope.schema) || []).map(field => typeof payload[field] === 'string' ? payload[field].trim() : '').filter(Boolean);
  return parts.join(' · ') || 'Structured message';
}
