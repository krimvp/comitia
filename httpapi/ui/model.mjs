export const PAGE_SIZE = 100;
export const WINDOW_LIMIT = 5000;
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

export function filterMessages(messages, {query = '', agent = '', type = '', task = '', thread = '', from = '', to = ''} = {}) {
  const text = query.trim().toLocaleLowerCase();
  const ids = thread ? threadIDs(messages, thread) : null;
  return messages.filter(message => {
    const envelope = message.envelope || {};
    return (!ids || ids.has(message.ID)) &&
      (!agent || message.SessionID === agent || (envelope.recipient_session_ids || []).includes(agent)) &&
      (!type || messageType(message) === type) &&
      (!task || envelope.task_id === task) &&
      (!from || message.SessionID === from) &&
      (!to || (to === '@broadcast' ? !(envelope.recipient_session_ids?.length) : (envelope.recipient_session_ids || []).includes(to))) &&
      (!text || [message.Body, message.ID, message.SessionID, JSON.stringify(message.agent_instance), JSON.stringify(envelope)].join(' ').toLocaleLowerCase().includes(text));
  });
}

export const BROADCAST = '@broadcast';
export function interactionGraph(messages, participants = []) {
  const sessions = new Map(participants.map(p => [p.SessionID, {id:p.SessionID,name:p.Name,sent:0,received:0,active:true}]));
  const links = new Map();
  const ensure = id => {if (!sessions.has(id)) sessions.set(id,{id,name:id===BROADCAST?'Everyone':'Session',sent:0,received:0,active:false});return sessions.get(id);};
  for (const message of messages) {
    const sender = ensure(message.SessionID); sender.sent++;
    const recipients = [...new Set(message.envelope?.recipient_session_ids?.length ? message.envelope.recipient_session_ids : [BROADCAST])];
    for (const target of recipients) {
      ensure(target).received++;
      const key = JSON.stringify([sender.id,target]);
      if (!links.has(key)) links.set(key,{source:sender.id,target,count:0,messageIDs:[]});
      const link = links.get(key);link.count++;link.messageIDs.push(message.ID);
    }
  }
  return {nodes:[...sessions.values()],edges:[...links.values()],messageCount:messages.length};
}
