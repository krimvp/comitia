import {shortID, TASK_STATUS, taskStatus, stateLabel} from './model.mjs';

const byID = (a, b) => a.ID < b.ID ? -1 : a.ID > b.ID ? 1 : 0;

// Discovered contexts are readable. A child listed by contexts/children is readable too when it is
// parent_readable under a readable parent, mirroring the server's read inheritance; any other child
// is locked: metadata only, and the inspector never requests its messages, tasks or participants.
export function readableContexts(contexts, children = new Map()) {
  const readable = new Map(contexts.map(c => [c.ID, c]));
  for (let changed = true; changed;) {
    changed = false;
    for (const [parentID, list] of children) {
      if (!readable.has(parentID)) continue;
      for (const c of list) if (c.ParentContextID === parentID && c.ParentReadable && !readable.has(c.ID)) { readable.set(c.ID, c); changed = true; }
    }
  }
  return readable;
}

// The tree flattens to rows in display order: parents before their children, siblings by ID.
// Contexts whose parent is not readable start their own top-level branch.
export function contextTree(contexts, children = new Map()) {
  const readable = readableContexts(contexts, children), kids = new Map();
  const add = (parentID, context, locked) => {
    if (!kids.has(parentID)) kids.set(parentID, new Map());
    if (!kids.get(parentID).has(context.ID)) kids.get(parentID).set(context.ID, {context, locked});
  };
  for (const c of readable.values()) if (c.ParentContextID && readable.has(c.ParentContextID)) add(c.ParentContextID, c, false);
  for (const [parentID, list] of children) {
    if (readable.has(parentID)) for (const c of list) if (c.ParentContextID === parentID && !readable.has(c.ID)) add(parentID, c, true);
  }
  const rows = [], seen = new Set();
  const visit = (entry, depth) => {
    if (seen.has(entry.context.ID)) return;
    seen.add(entry.context.ID); rows.push({...entry, depth});
    for (const child of [...(kids.get(entry.context.ID)?.values() || [])].sort((a, b) => byID(a.context, b.context))) visit(child, depth + 1);
  };
  const roots = [...readable.values()].filter(c => !c.ParentContextID || !readable.has(c.ParentContextID)).sort(byID);
  for (const c of roots) visit({context:c, locked:false}, 0);
  // Parent cycles cannot be created; anything still unvisited shows at the top level anyway.
  for (const c of [...readable.values()].sort(byID)) visit({context:c, locked:false}, 0);
  return rows;
}

// One entry per task of the context with a task-status report or a child context, holding the latest
// loaded report and the linked child row (the report's child_context_id, else a child serving the task).
export function taskBoard(contextID, messages, tasks, rows) {
  const childRows = rows.filter(r => r.context.ParentContextID === contextID);
  const entries = new Map();
  const entry = taskID => {
    if (!entries.has(taskID)) entries.set(taskID, {taskID, title:tasks.find(t => t.ID === taskID)?.Title || '', latest:null, reports:0, child:null, childContextID:''});
    return entries.get(taskID);
  };
  for (const message of messages) {
    if (message.envelope?.schema !== TASK_STATUS || !message.envelope.task_id) continue;
    const status = taskStatus(message, tasks), item = entry(status.taskID);
    item.reports++;
    if (!item.latest || message.Sequence > item.latest.sequence) {
      item.latest = {...status, sequence:message.Sequence, messageID:message.ID};
      if (status.childContextID) item.childContextID = status.childContextID;
    }
  }
  for (const row of childRows) if (row.context.TaskID) entry(row.context.TaskID);
  for (const item of entries.values()) {
    item.child = childRows.find(r => r.context.ID === item.childContextID) || (!item.childContextID ? childRows.find(r => r.context.TaskID === item.taskID) : null) || null;
  }
  return [...entries.values()].sort((a, b) => (a.title || a.taskID).localeCompare(b.title || b.taskID));
}

function node(tag, text, className) {
  const element = document.createElement(tag);
  if (text !== undefined) element.textContent = String(text);
  if (className) element.className = className;
  return element;
}
function button(text, action, className) {
  const element = node('button', text, className);
  element.type = 'button'; element.addEventListener('click', action);
  return element;
}

// Polling re-renders often; rebuilding unchanged rows would drop keyboard focus. A render is skipped
// while the target still holds the nodes of the last render with the same key.
const rendered = new WeakMap();
function unchanged(target, key) {
  const last = rendered.get(target);
  return last?.key === key && last.first === target.children[0];
}
function remember(target, key) { rendered.set(target, {key, first:target.children[0]}); }

// Rows render as a flat, indented list; data is set only through text nodes.
export function renderContextTree(target, rows, {active = '', taskTitle = () => '', creator = id => shortID(id), truncated = new Set(), onOpen}) {
  const key = JSON.stringify([active, rows.map(({context:c, locked, depth}) => [c.ID, c.Name, locked, depth, c.Archived, c.TaskID, c.TaskID && taskTitle(c.TaskID), c.CreatorSessionID && creator(c.CreatorSessionID), truncated.has(c.ID)])]);
  if (unchanged(target, key)) return;
  target.replaceChildren(...rows.map(({context, locked, depth}) => {
    const classes = `context-item${locked ? ' locked' : ''}${context.Archived ? ' archived' : ''}${context.ID === active ? ' active' : ''}`;
    const item = locked ? node('div', context.Name, classes) : button(context.Name, () => onOpen(context.ID), classes);
    item.dataset.depth = String(Math.min(depth, 6)); item.title = context.ID;
    item.setAttribute('aria-level', String(depth + 1));
    if (context.ID === active) item.setAttribute('aria-current', 'page');
    item.append(node('small', shortID(context.ID)));
    if (context.TaskID) item.append(node('small', `Task: ${taskTitle(context.TaskID) || shortID(context.TaskID)}`, 'context-task'));
    if (context.Archived) item.append(node('span', 'Archived', 'badge context-archived'));
    if (locked) {
      item.setAttribute('aria-disabled', 'true');
      item.append(node('span', 'Locked · metadata only', 'badge context-locked'));
      if (context.CreatorSessionID) item.append(node('small', `Created by ${creator(context.CreatorSessionID)}`, 'context-task'));
    }
    if (truncated.has(context.ID)) item.append(node('small', 'More child contexts not shown', 'context-task'));
    return item;
  }));
  remember(target, key);
}

export function renderTaskBoard(target, entries, {onOpen, onTask}) {
  target.hidden = !entries.length;
  const key = JSON.stringify(entries);
  if (unchanged(target, key)) return;
  if (!entries.length) { target.replaceChildren(); remember(target, key); return; }
  target.replaceChildren(node('h2', 'Task progress'), ...entries.map(item => {
    const row = node('div', undefined, 'task-board-item');
    const task = button('', () => onTask(item.taskID), 'task-board-task');
    task.append(node('span', item.title || `Task ${shortID(item.taskID)}`));
    task.append(item.latest ? node('span', stateLabel(item.latest.state), `badge task-state ${item.latest.state}`) : node('span', 'No status reported', 'badge'));
    row.append(task);
    if (item.latest?.summary) row.append(node('p', item.latest.summary, 'task-board-summary'));
    if (item.reports) row.append(node('small', `${item.reports} status report${item.reports === 1 ? '' : 's'} loaded`));
    if (item.child && !item.child.locked) row.append(button(`Open child context ${item.child.context.Name}`, () => onOpen(item.child.context.ID), 'text-button'));
    else if (item.child) row.append(node('small', `Child context ${item.child.context.Name} · locked`, 'context-locked-note'));
    else if (item.childContextID) row.append(node('small', `Child context ${shortID(item.childContextID)} · not visible`, 'context-locked-note'));
    return row;
  }));
  remember(target, key);
}
