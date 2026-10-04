import {on, renderChildren} from './dom.mjs';
import {relativeSeen} from './presence.mjs';
import {ConversationState, PAGE_SIZE, WINDOW_LIMIT, shortID, messageType, typeLabel, filterMessages, interactionGraph, BROADCAST, parseActor, senderActor, recipientActors, instanceClaims, taskStatus, stateLabel} from './model.mjs';
import {renderInteractionMap} from './graph.mjs';
import {sequenceLayout, renderSequence, LANE_LIMIT, ARROW_LIMIT, timeLabel} from './sequence.mjs';
import {readableContexts, contextTree, taskBoard, renderContextTree, renderTaskBoard} from './tree.mjs';

const $ = id => document.getElementById(id);
const state = new ConversationState();
const apiBase = new URL('../v1/', location.href);
const credentialKey = `comitia.inspector.credential:${apiBase.href}`;
function savedCredential() {
  try { return sessionStorage.getItem(credentialKey) || ''; } catch { return ''; }
}
function rememberCredential(value) {
  try {
    if (value) sessionStorage.setItem(credentialKey, value);
    else sessionStorage.removeItem(credentialKey);
  } catch { /* Browsers that block storage can still connect manually. */ }
}
let token = '', connection = 0, controller = new AbortController(), timer;
let contexts = [], contextsAfter = '', contextsBusy = false;
// Parent ID -> child metadata from contexts/children (parents whose listing hit the cap are truncated),
// and task titles seen in readable task lists.
let children = new Map(), truncated = new Set(), taskTitles = new Map();
const CHILD_PAGE = 1000, CHILD_LIMIT = 5000;
let participants = [], tasks = [], instances = [], selected = '', thread = '', paused = false, busy = false, windowStart = 0, displayStart = null, route = null, connectionPage = 0;
let instancesUnavailable = false;
let detailReturn = null, detailScroll = 0;
let view = 'sequence', sequenceOptions = {}, sequenceFilterKey = '', sequenceCurrent;

function node(tag, text, className) {
  const element = document.createElement(tag);
  if (text !== undefined) element.textContent = String(text);
  if (className) element.className = className;
  return element;
}
function button(text, action, className = 'text-button', key = '') {
  const element = node('button', text, className);
  element.type = 'button'; on(element, 'click', action);
  if (key) element.dataset.key = key;
  return element;
}
function empty(target, title, description) {
  const box = node('div', undefined, 'empty');
  box.append(node('h3', title), node('p', description));
  renderChildren(target, box);
}
function date(value) {
  const d = new Date(value);
  return Number.isNaN(d.valueOf()) ? 'Unknown time' : d.toLocaleString(undefined, {month:'short', day:'numeric', hour:'2-digit', minute:'2-digit', second:'2-digit'});
}
function participantName(id) {
  return participants.find(p => p.SessionID === id)?.Name || 'Session';
}
function agentLabel(id) {
  const {session, instance} = parseActor(id);
  if (!instance) return `${participantName(id)} · ${shortID(id)}`;
  return `${instance} (self-reported) · ${session ? `${participantName(session)} · ${shortID(session)}` : 'unknown session'}`;
}
function filters() { return {query:$('search').value, agent:$('agent-filter').value, type:$('type-filter').value, task:$('task-filter').value, thread, from:route?.from || '', to:route?.to || ''}; }

async function request(path, body, signal = controller.signal) {
  const abort = new AbortController();
  const cancel = () => abort.abort();
  signal.addEventListener('abort', cancel, {once:true});
  if (signal.aborted) cancel();
  let timedOut = false;
  const deadline = setTimeout(() => { timedOut = true; abort.abort(); }, 15000);
  try {
  const response = await fetch(new URL(path, apiBase), {
    method:'POST', headers:{'Content-Type':'application/json', Authorization:`Bearer ${token}`},
    body:JSON.stringify(body), signal:abort.signal, cache:'no-store', credentials:'omit', redirect:'error'
  });
  if (!response.ok) {
    const error = new Error(response.status === 401 ? 'Session expired or invalid. Connect with a valid credential.' : response.status === 403 ? 'Read access is no longer available. Connect with an authorized credential.' : `Request failed (${response.status}). Retry to continue.`);
    error.status = response.status; throw error;
  }
  try { return await response.json(); } catch { throw new Error('Invalid server response. Retry to continue.'); }
  } catch (error) {
    if (timedOut) throw new Error('Request timed out. Retry to continue.');
    throw error;
  } finally { clearTimeout(deadline); signal.removeEventListener('abort', cancel); }
}
function instanceLabel(message) {
  const instance = message.agent_instance;
  if (!instance) return '';
  return ` · ${instance.label ? `${instance.label} (${instance.id})` : instance.id} (self-reported)`;
}

function disconnect(message = '', forget = true) {
  closeDetails(false); $('selection-status').textContent = '';
  if (forget) rememberCredential('');
  connection++; controller.abort(); controller = new AbortController(); clearTimeout(timer);
  token = ''; state.reset(); displayStart = null; contexts = []; contextsAfter = ''; contextsBusy = false; children = new Map(); truncated = new Set(); taskTitles = new Map();
  participants = []; tasks = []; instances = []; instancesUnavailable = false; selected = ''; thread = ''; route = null; connectionPage = 0; busy = false;
  $('credential').value = ''; $('connect-error').textContent = message;
  $('connect-panel').hidden = false; $('workspace').hidden = true; $('disconnect').hidden = true;
  for (const id of ['messages', 'contexts', 'tasks', 'task-board', 'instances', 'details', 'interaction-map', 'connections', 'sequence-canvas', 'sequence-routes']) $(id).replaceChildren();
  sequenceOptions = {}; sequenceCurrent = null; sequenceFilterKey = '';
  for (const id of ['sequence-summary','sequence-range','sequence-lanes','sequence-arrow-count','sequence-note','route-summary','map-summary','map-note','connection-page']) $(id).textContent = '';
  $('search').value = ''; for (const id of ['agent-filter','type-filter','task-filter']) $(id).replaceChildren();
  $('context-title').textContent = 'Choose a context'; $('context-meta').textContent = '';
  for (const id of ['workspace-label','tasks-note','instances-note','instance-count','contexts-note','timeline-error','history-note']) $(id).textContent = '';
  $('credential').focus();
}
function fail(error, generation, target = 'timeline-error') {
  if (error.name === 'AbortError' || generation !== state.generation) return;
  if (error.status === 401 || error.status === 403) { disconnect(error.message); return; }
  $(target).textContent = error.message || 'Unable to load data. Retry to continue.';
  $('sync-status').textContent = 'Connection interrupted';
}
function options(id, entries, placeholder) {
  const select = $(id), current = select.value;
  const choices = [[ '', placeholder ], ...entries];
  if (current && !entries.some(([value]) => value === current)) choices.push([current, shortID(current)]);
  renderChildren(select, ...choices.map(([value, text]) => {const option = new Option(text, value); option.dataset.key = `option:${value}`; return option;}));
  select.value = current;
}
function refreshFilters() {
  const ids = new Set(participants.map(p => p.SessionID));
  const taskIDs = new Map(tasks.map(t => [t.ID, t.Title]));
  const claims = instanceClaims(state.messages);
  for (const message of state.messages) {
    ids.add(message.SessionID); ids.add(senderActor(message));
    for (const id of message.envelope?.recipient_session_ids || []) ids.add(id);
    for (const id of recipientActors(message, claims)) if (id !== BROADCAST) ids.add(id);
    if (message.envelope?.task_id && !taskIDs.has(message.envelope.task_id)) taskIDs.set(message.envelope.task_id, `Task · ${shortID(message.envelope.task_id)}`);
  }
  options('agent-filter', [...ids].map(id => [id, agentLabel(id)]), 'All agents');
  options('type-filter', [...new Set(state.messages.map(messageType))].sort().map(t => [t, typeLabel(t)]), 'All types');
  options('task-filter', [...taskIDs], 'All tasks');
}
function readable() { return [...readableContexts(contexts, children).values()]; }
function renderContexts() {
  renderContextTree($('contexts'), contextTree(contexts, children), {active:state.contextID, taskTitle:id => taskTitles.get(id), creator:agentLabel, truncated, onOpen:openContext});
}
function renderBoard() {
  renderTaskBoard($('task-board'), state.contextID ? taskBoard(state.contextID, state.messages, tasks, contextTree(contexts, children)) : [], {
    onOpen:openContext,
    onTask:id => { $('task-filter').value = id; displayStart = null; renderTasks(); renderMessages(); }
  });
}
// Children metadata needs read on the parent only, so it is requested for opened contexts alone.
async function loadChildren(id, generation) {
  const list = [];
  while (list.length < CHILD_LIMIT) {
    const result = await request('contexts/children', {context_id:id, after_id:list.at(-1)?.ID || '', limit:CHILD_PAGE});
    if (generation !== state.generation) return;
    const page = result.contexts || [];
    if (!Array.isArray(page)) throw new Error('Invalid contexts response.');
    list.push(...page);
    if (page.length < CHILD_PAGE) break;
  }
  children.set(id, list);
  if (list.length >= CHILD_LIMIT) truncated.add(id); else truncated.delete(id);
  renderContexts(); renderBoard();
}
// Missing children metadata only hides locked entries; lost access still disconnects.
function childrenUnavailable(error) {
  if (error.status === 401 || error.status === 403 || error.name === 'AbortError') throw error;
  $('contexts-note').textContent = 'Child contexts are unavailable. Refresh to retry.';
}
async function loadContexts(refresh = false) {
  if (contextsBusy) return;
  contextsBusy = true;
  const conn = connection;
  // Context discovery has its own request lifetime so changing the selected
  // conversation does not cancel a sidebar page already being loaded.
  const credential = token;
  const discoveryController = new AbortController();
  
  try {
    if (refresh) { contextsAfter = ''; }
    const result = await request('contexts/list', {after_id:contextsAfter, limit:100}, discoveryController.signal);
    if (conn !== connection || credential !== token) return;
    if (refresh && state.contextID) { const generation = state.generation; loadChildren(state.contextID, generation).catch(childrenUnavailable).catch(error => fail(error, generation, 'contexts-note')); }
    const page = result.contexts || [];
    if (!Array.isArray(page)) throw new Error('Invalid contexts response.');
    const all = new Map((refresh ? [] : contexts).map(c => [c.ID, c]));
    page.forEach(c => all.set(c.ID, c)); contexts = [...all.values()];
    if (page.length) contextsAfter = page.at(-1).ID;
    $('more-contexts').hidden = page.length < 100;
    $('contexts-note').textContent = contexts.length ? `${contexts.length} contexts available${page.length === 100 ? ' · more available' : ''}` : 'No contexts with read access.';
    renderContexts();
    if (!state.contextID && contexts.length) await openContext(contexts[0].ID);
    // A refresh can remove a revoked context. Hide it immediately; access is
    // checked again for every metadata/replay request.
    else if (refresh && !readable().some(c => c.ID === state.contextID)) {
      if (contexts.length) await openContext(contexts[0].ID);
      else clearContext();
    }
  } catch (error) {
    if (conn === connection) fail(error, state.generation, 'contexts-note');
  } finally { if (conn === connection) contextsBusy = false; }
}
function clearContext() {
  closeDetails(false); $('selection-status').textContent = '';
  sequenceOptions = {}; sequenceFilterKey = '';
  controller.abort(); controller = new AbortController(); clearTimeout(timer); state.reset();
  participants = []; tasks = []; instances = []; instancesUnavailable = false; selected = ''; thread = ''; route = null; connectionPage = 0; busy = false;
  $('context-title').textContent = 'Choose a context'; $('context-meta').textContent = '';
  $('workspace-label').textContent = 'WORK CONTEXT'; $('sync-status').textContent = 'No context selected';
  $('load-more').hidden = true; $('next-window').hidden = true;
  $('search').value = ''; for (const id of ['agent-filter','type-filter','task-filter']) $(id).value = '';
  displayStart = null; windowStart = 0; refreshFilters();
  renderTasks(); renderInstances(); renderMessages(); renderDetails(); renderBoard();
}
async function openContext(id, after = 0) {
  closeDetails(false); $('selection-status').textContent = '';
  sequenceOptions = {}; sequenceFilterKey = '';
  controller.abort(); controller = new AbortController(); clearTimeout(timer);
  const generation = state.reset(id, after); windowStart = after; displayStart = null;
  participants = []; tasks = []; instances = []; instancesUnavailable = false; selected = ''; thread = ''; route = null; connectionPage = 0; busy = false; paused = false;
  $('pause').textContent = 'Pause'; $('timeline-error').textContent = '';
  $('search').value = ''; for (const id of ['agent-filter','type-filter','task-filter']) $(id).value = '';
  $('context-title').textContent = readable().find(c => c.ID === id)?.Name || 'Loading context…';
  $('context-meta').textContent = id; $('sync-status').textContent = 'Loading';
  renderContexts(); refreshFilters(); renderTasks(); renderInstances(); renderMessages(); renderDetails(); renderBoard();
  try {
    const [result] = await Promise.all([request('contexts/get', {context_id:id}), loadChildren(id, generation).catch(childrenUnavailable)]);
    if (generation !== state.generation) return;
    $('context-title').textContent = result.context.Name;
    if (result.context.Archived) $('context-meta').textContent = `${id} · Archived ${date(result.context.ArchivedAt)}`;
    $('workspace-label').textContent = `WORKSPACE ${shortID(result.context.WorkspaceID)}`;
    await sync(generation);
  } catch(error) { fail(error, generation); }
}
async function sync(generation = state.generation) {
  if (!token || !state.contextID || busy || generation !== state.generation) return;
  busy = true; $('load-more').disabled = true; clearTimeout(timer);
  const cid = state.contextID;
  try {
    const room = WINDOW_LIMIT - state.messages.length;
    const [messageResult, participantResult, taskResult, instanceResult] = await Promise.all([
      room > 0 ? request('messages/replay', {context_id:cid, after:state.after, limit:Math.min(PAGE_SIZE,room)}) : Promise.resolve({messages:[]}),
      request('participants/list', {context_id:cid, limit:1000}),
      request('tasks/list', {context_id:cid, limit:1000}),
      loadInstances(cid)
    ]);
    if (generation !== state.generation) return;
    const incoming = messageResult.messages || [];
    state.append(generation, incoming);
    participants = participantResult.participants || []; tasks = taskResult.tasks || []; instances = instanceResult.instances; instancesUnavailable = instanceResult.unavailable;
    if (!Array.isArray(participants) || !Array.isArray(tasks)) throw new Error('Invalid metadata response.');
    for (const task of tasks) taskTitles.set(task.ID, task.Title);
    $('timeline-error').textContent = '';
    $('sync-status').textContent = paused ? 'Paused' : `Updated ${new Date().toLocaleTimeString()}`;
    $('load-more').hidden = state.messages.length >= WINDOW_LIMIT;
    $('load-more').textContent = incoming.length === PAGE_SIZE ? 'Load next messages' : 'Refresh messages';
    $('next-window').hidden = state.messages.length < WINDOW_LIMIT;
    if (state.messages.length >= WINDOW_LIMIT) { paused = true; $('pause').textContent = 'Resume'; $('sync-status').textContent = 'Window full'; }
    refreshFilters(); renderTasks(); renderInstances(); renderMessages(); renderDetails(); renderBoard(); renderContexts();
  } catch(error) { fail(error, generation); }
  finally {
    if (generation === state.generation) {
      busy = false; $('load-more').disabled = false;
      if (!paused && token) timer = setTimeout(() => sync(generation), 3000);
    }
  }
}
async function loadInstances(cid) {
  try {
    const result = await request('instances/list', {context_id:cid});
    const rows = result.instances || [];
    if (!Array.isArray(rows) || rows.some(row => !row || typeof row.id !== 'string' || typeof row.session_id !== 'string')) throw new Error('Invalid instances response.');
    return {instances:rows, unavailable:false};
  } catch {
    return {instances:[], unavailable:true};
  }
}
function renderInstances() {
  $('instance-count').textContent = instances.length;
  renderChildren($('instances'), ...instances.map(instance => {
    const item = node('div', undefined, 'instance-item');
    item.dataset.key = JSON.stringify([instance.session_id, instance.id]);
    item.append(node('strong', instance.label ? `${instance.label} (${instance.id})` : instance.id), node('small', `${participantName(instance.session_id)} · ${shortID(instance.session_id)}`));
    if (instance.active === false) item.append(node('small', 'Session revoked or expired'));
    const seen = node('small', `Last seen ${relativeSeen(instance.last_seen)}`);
    seen.title = date(instance.last_seen); item.append(seen);
    if (instance.resume) {
      const details = node('details'); details.dataset.key = 'resume';
      details.append(node('summary', 'Resume metadata'), node('pre', JSON.stringify(instance.resume, null, 2)));
      item.append(details);
    }
    return item;
  }));
  $('instances-note').textContent = instancesUnavailable ? 'Instances unavailable. Retrying with the next refresh.' : instances.length ? 'Self-reported activity; last seen does not prove a process is running.' : 'No instance activity recorded.';
}
function renderMap() {
  const current = filters();
  const mapped = filterMessages(state.messages, {query:current.query,type:current.type,task:current.task});
  const graph = interactionGraph(mapped, participants);
  const result = renderInteractionMap($('interaction-map'), graph, {
    agent:current.agent,route,selectedMessage:state.messages.find(m=>m.ID===selected),
    onAgent:id=>{route=null;thread='';$('agent-filter').value=$('agent-filter').value===id?'':id;displayStart=null;selected=filterMessages(state.messages,filters())[0]?.ID||'';renderMessages();renderDetails();document.getElementById(`map-node-${id}`)?.focus();},
    onRoute:selectRoute
  });
  const sessionCount=graph.nodes.filter(n=>n.id!==BROADCAST).length;
  $('map-summary').textContent=`${sessionCount} agents · ${graph.messageCount} messages · ${graph.edges.length} directed connections`;
  $('map-note').textContent=result.total>result.shown?`Showing ${result.shown} of ${result.total} agents. Choose an agent to focus its connections.`:'Select an agent or an arrow to follow the exchange. Counts follow the filters below.';
  $('route-summary').textContent=route?`${route.from?agentLabel(route.from):'Any sender'} → ${route.to===BROADCAST?'Everyone':agentLabel(route.to)}`:current.agent?`Involving ${agentLabel(current.agent)}`:'All interactions';
  $('clear-route').hidden=!route&&!current.agent;
  const edges=[...graph.edges].sort((a,b)=>b.count-a.count||a.source.localeCompare(b.source)||a.target.localeCompare(b.target));
  connectionPage=Math.min(connectionPage,Math.max(0,Math.ceil(edges.length/12)-1));
  renderChildren($('connections'), ...edges.slice(connectionPage*12,connectionPage*12+12).map(edge=>{
    const item=button('',()=>selectRoute(edge.source,edge.target),'connection-item');
    item.append(node('span',`${agentLabel(edge.source)} → ${edge.target===BROADCAST?'Everyone':agentLabel(edge.target)}`),node('strong',edge.count));
    item.dataset.key=JSON.stringify([edge.source,edge.target]);
    item.setAttribute('aria-pressed',String(route?.from===edge.source&&route?.to===edge.target));
    return item;
  }));
  $('connection-page').textContent=edges.length?`${connectionPage*12+1}–${Math.min(edges.length,connectionPage*12+12)} of ${edges.length} connections`:'No connections yet';
  $('previous-connections').hidden=connectionPage===0;$('next-connections').hidden=(connectionPage+1)*12>=edges.length;
}
function selectRoute(from,to) {
  route=route?.from===from&&route?.to===to?null:{from,to};thread='';$('agent-filter').value='';displayStart=null;selected=filterMessages(state.messages,filters())[0]?.ID||'';renderMessages();renderDetails();
}

function renderTasks() {
  $('task-count').textContent = tasks.length;
  $('tasks-note').textContent = !tasks.length ? 'No tasks in this context.' : tasks.length === 1000 ? 'First 1,000 tasks. Other referenced tasks can be opened from messages.' : '';
  renderChildren($('tasks'), ...tasks.map(task => {
    const item = button('', () => { $('task-filter').value = task.ID; displayStart = null; renderTasks(); renderMessages(); }, `task-item${$('task-filter').value === task.ID ? ' active' : ''}`);
    item.dataset.key = task.ID;
    item.append(node('span', task.Status === 'completed' ? '✓' : task.Status === 'leased' ? '◷' : '□', `task-marker ${task.Status}`), node('span', task.Title));
    return item;
  }));
}
function clearFilters() {
  $('search').value = ''; for (const id of ['agent-filter','type-filter','task-filter']) $(id).value = '';
  thread = ''; route = null; displayStart = null; renderTasks(); renderMessages();
}
function selectedTrigger() {
  if (view === 'sequence') {
    const route = sequenceCurrent?.routes.find(item => item.message.ID === selected);
    const trigger = route && document.getElementById(`sequence-route-${encodeURIComponent(selected)}-${encodeURIComponent(route.target)}`);
    if (trigger) return trigger;
    setView('timeline');
  }
  return document.getElementById(`message-${selected}`)?.querySelector('button');
}
function closeDetails(restore = true) {
  const dialog = $('details-dialog');
  if (!dialog.open) return;
  dialog.close(); $('details-home').append($('details'));
  if (restore) {
    const origin = detailReturn?.isConnected;
    const target = origin ? detailReturn : selectedTrigger() || $('conversation');
    target.focus({preventScroll:true});
    if (origin) window.scrollTo(0, detailScroll);
    else target.scrollIntoView({block:'center'});
  }
  detailReturn = null;
}
function showDetails(source = document.activeElement) {
  $('selection-status').textContent = `Message #${state.messages.find(m => m.ID === selected)?.Sequence} selected. Details available.`;
  if (!(window.innerWidth <= 960) || !selected) return;
  const dialog = $('details-dialog');
  if (!dialog.open) {
    detailReturn = source; detailScroll = window.scrollY;
    $('dialog-details').append($('details')); dialog.showModal();
  }
  $('details-heading').textContent = `Message #${state.messages.find(m => m.ID === selected)?.Sequence} details`;
  $('details-heading').focus({preventScroll:true});
}
function selectMessage(id, source) {
  selected = id; renderMessages(); renderDetails(); showDetails(source);
}
function jumpMessage(id) {
  if (!state.messages.some(m => m.ID === id)) return;
  clearFilters(); selected = id; sequenceOptions.seekMessage=id; displayStart = Math.floor(state.messages.findIndex(m => m.ID === id) / 200) * 200; renderMessages(); renderDetails();
  const card = document.getElementById(`message-${id}`);
  if (window.innerWidth <= 960) {showDetails(); return;}
  card?.scrollIntoView({block:'center', behavior:'smooth'});
  card?.querySelector('button')?.focus({preventScroll:true});
}
function engaged(id) {
  const target = $(id);
  return target.contains(document.activeElement) || ($('details-dialog').open && target.contains(detailReturn));
}
function renderMessages() {
  renderMap();
  const filtered = filterMessages(state.messages, filters());
  renderFlow(filtered);
  // Keep DOM work bounded while every loaded message remains searchable.
  const previous = engaged('messages') ? filtered.findIndex(m => `message-${m.ID}` === $('messages').children[0]?.id) : -1;
  const start = Math.min(displayStart ?? (previous >= 0 ? previous : Math.max(0, filtered.length - 200)), Math.max(0, filtered.length - 1));
  const visible = filtered.slice(start, start + 200);
  $('older-matches').hidden = view==='sequence' || start === 0;
  $('newer-matches').hidden = view==='sequence' || start + visible.length >= filtered.length;
  $('message-count').textContent = `${filtered.length} matching · ${state.messages.length} loaded`;
  $('clear-thread').hidden = !thread;
  $('history-note').textContent = `Filters cover loaded messages after #${windowStart}. ${filtered.length > visible.length ? `Showing matches ${start + 1}–${start + visible.length} of ${filtered.length}. ` : ''}${state.messages.length >= WINDOW_LIMIT ? '5,000-message window reached. Continue to load the next window. ' : ''}Browsing never acknowledges agent messages.`;
  if (!state.contextID) { empty($('messages'),'Choose a work context','Conversations you can read will appear here.'); return; }
  if (!filtered.length) { empty($('messages'), state.messages.length ? 'No matching messages' : 'No messages loaded', state.messages.length ? 'Try another filter or load more of the conversation.' : 'New agent messages will appear here as they arrive.'); return; }
  const byID = new Map(state.messages.map(m => [m.ID,m])), known = readable();
  renderChildren($('messages'), ...visible.map(message => {
    const envelope = message.envelope || {}, type = messageType(message);
    const card = node('article', undefined, `message-card${selected === message.ID ? ' selected' : ''}`); card.id = `message-${message.ID}`;
    let depth = 0, ancestor = message, seen = new Set();
    while (thread && ancestor.envelope?.reply_to && byID.has(ancestor.envelope.reply_to) && depth < 3 && !seen.has(ancestor.ID)) {seen.add(ancestor.ID);depth++;ancestor=byID.get(ancestor.envelope.reply_to);}
    card.dataset.depth = depth;
    const main = button('', event => selectMessage(message.ID, event.currentTarget), 'message-select', `inspect:${message.ID}`); main.setAttribute('aria-label', `Inspect message ${message.Sequence} from ${agentLabel(message.SessionID)}${instanceLabel(message)}`);
    const top = node('div', undefined, 'message-top');
    top.append(node('span', participantName(message.SessionID).slice(0,2).toUpperCase(), 'avatar'));
    const sender = node('span', participantName(message.SessionID) + instanceLabel(message), 'sender'); sender.append(node('span', ` · ${shortID(message.SessionID)}`, 'session-short'));
    top.append(sender, node('span', typeLabel(type), `badge ${type.replace(/^comitia\./,'').replace(/\.v\d+$/,'')}`), node('span', date(message.CreatedAt), 'message-time'));
    const status = taskStatus(message, tasks, known);
    if (status) top.append(node('span', stateLabel(status.state), `badge task-state ${status.state}`));
    main.append(top, node('p', message.Body || envelope.payload?.summary || envelope.payload?.instructions || 'Structured message', 'message-body'));
    main.append(node('p', envelope.recipient_session_ids?.length ? `To ${envelope.recipient_session_ids.map(agentLabel).join(', ')}` : 'To everyone in this context', 'message-routing'));
    card.append(main);
    if (envelope.recipient_instances?.length) main.append(node('p', `Recipient instances (self-reported): ${envelope.recipient_instances.join(', ')}`, 'message-routing'));
    const actions = node('div', undefined, 'message-actions');
    if (envelope.reply_to) actions.append(byID.has(envelope.reply_to) ? button(`Reply to #${byID.get(envelope.reply_to).Sequence}`, () => jumpMessage(envelope.reply_to), 'text-button', `parent:${envelope.reply_to}`) : node('span', `Reply to ${shortID(envelope.reply_to)} · outside loaded window`));
    actions.append(button('View thread', event => { thread = message.ID; selected = message.ID; displayStart = 0; renderMessages(); renderDetails(); showDetails(event.currentTarget); }, 'text-button', `thread:${message.ID}`));
    if (envelope.task_id) actions.append(button(tasks.find(t => t.ID === envelope.task_id)?.Title || `Task ${shortID(envelope.task_id)}`, () => {$('task-filter').value = envelope.task_id;displayStart=null;renderTasks();renderMessages();}, 'text-button', `filter-task:${envelope.task_id}`));
    if (status?.childContext) actions.append(button(`Child context ${status.childContext.Name}`, () => openContext(status.childContext.ID), 'text-button', `open-context:${status.childContext.ID}`));
    actions.append(node('span', `#${message.Sequence}`, 'sequence')); card.append(actions);
    return card;
  }));
}
function renderTaskDetail(target, task) {
  target.append(node('h3','Related task'), node('p',task.Title), node('span', task.Status === 'leased' && new Date(task.LeaseExpiresAt) <= new Date() ? 'Lease expired' : task.Status, 'badge'));
  if (task.Body) target.append(node('p',task.Body));
  if (task.LeaseOwnerSessionID) target.append(node('p',`Owner: ${agentLabel(task.LeaseOwnerSessionID)}`, 'hint'));
  if (task.LeaseExpiresAt) target.append(node('p',`Lease ends: ${date(task.LeaseExpiresAt)}`, 'hint'));
}
function renderTaskStatus(target, status) {
  target.append(node('h3','Task status'), node('span', stateLabel(status.state), `badge task-state ${status.state}`), node('p', `${status.taskTitle || `Task ${shortID(status.taskID)}`} · reported by the sender`));
  if (status.childContext) target.append(button(`Open child context ${status.childContext.Name}`, () => openContext(status.childContext.ID), 'text-button', `open-context:${status.childContext.ID}`));
  else if (status.childContextID) target.append(node('p', `Child context ${status.childContextID} is not in your loaded readable contexts.`, 'hint'));
  if (status.blockers.length) target.append(node('h3','Blockers'), ...status.blockers.map(b => node('p', b)));
}
function renderDetails() {
  const target = node('div'), message = state.messages.find(m => m.ID === selected);
  if (!message) {renderChildren($('details'), node('div', 'Select a message to inspect its recipients, task, replies, and structured payload.', 'empty-details'));return;}
  const envelope = message.envelope || {};
  target.append(node('span', typeLabel(messageType(message)), 'badge'), node('h3', `Message #${message.Sequence}`), node('p',message.Body || 'Structured message'));
  const dl = node('dl');
  for (const [key,value] of [['Message ID',message.ID],['Sender',`${participantName(message.SessionID)}\n${message.SessionID}`],['Created',date(message.CreatedAt)],['Recipients',envelope.recipient_session_ids?.map(id=>`${participantName(id)} · ${id}`).join('\n') || 'Everyone in this context'],['Context',message.ContextID]]) dl.append(node('dt',key),node('dd',value));
  target.append(dl);
  if (message.agent_instance) {
    dl.append(node('dt','Agent instance (self-reported)'),node('dd',`${message.agent_instance.id}${message.agent_instance.label ? ` · ${message.agent_instance.label}` : ''}`));
    target.append(node('p','The instance is a caller claim. The authenticated sender is the session above.', 'hint'));
  }
  if (envelope.recipient_instances?.length) dl.append(node('dt','Recipient instances (self-reported)'),node('dd',envelope.recipient_instances.join(', ')));
  target.append(node('p','Addressed recipients are shown above. Read status is unknown; browsing this inspector does not acknowledge messages.', 'hint'));
  if (envelope.task_id) {
    const task = tasks.find(t=>t.ID === envelope.task_id);
    if (task) renderTaskDetail(target,task);
    else target.append(button(`Load task ${shortID(envelope.task_id)}`, async () => {
      const generation = state.generation, id = selected;
      try {const result = await request('tasks/get',{task_id:envelope.task_id});if(generation !== state.generation || selected !== id) return;if(result.task.ContextID !== state.contextID) throw new Error('Task does not belong to this context.');tasks.push(result.task);taskTitles.set(result.task.ID,result.task.Title);refreshFilters();renderDetails();}
      catch(error){fail(error,generation);}
    }, 'text-button', `load-task:${envelope.task_id}`));
  }
  const status = taskStatus(message, tasks, readable());
  if (status) renderTaskStatus(target, status);
  const replies = state.messages.filter(m => m.envelope?.reply_to === message.ID);
  target.append(node('h3',`Replies · ${replies.length} loaded`));
  if (envelope.reply_to) target.append(state.messages.some(m=>m.ID === envelope.reply_to) ? button('Open parent message',()=>jumpMessage(envelope.reply_to), 'text-button', `parent:${envelope.reply_to}`) : node('p',`Parent ${envelope.reply_to} is outside the loaded window.`, 'hint'));
  replies.forEach(reply=>target.append(button(`#${reply.Sequence} · ${agentLabel(reply.SessionID)}`,()=>jumpMessage(reply.ID), 'text-button', `reply:${reply.ID}`)));
  if (envelope.schema) {
    target.append(node('h3','Structured payload'),node('pre',JSON.stringify(envelope,null,2)),node('p','Reported claims and evidence references are supplied by agents. They do not certify a result or change task status.', 'hint'));
    const refs = [envelope.payload?.artifact_refs, envelope.payload?.evidence_refs, status?.links].flatMap(value => Array.isArray(value) ? value : []);
    if (refs.length) target.append(node('h3','References'));
    for (const ref of refs) {
      if (typeof ref !== 'string') continue;
      let url; try {url = new URL(ref);} catch {}
      if (url && ['https:','http:'].includes(url.protocol) && !url.username && !url.password) {const a = node('a',ref,'reference');a.href=url.href;a.target='_blank';a.rel='noopener noreferrer';target.append(a);}
      else target.append(node('span',ref,'reference'));
    }
  } else target.append(node('p','Plain-text message · no structured envelope.', 'hint'));
  renderChildren($('details'), ...target.childNodes);
}
async function connect(value) {
  if (!value) return;
  disconnect(); token = value; connection++; const conn = connection;
  rememberCredential(value);
  $('connect').disabled = true; $('connect-error').textContent = '';
  $('workspace').hidden = false; $('connect-panel').hidden = true; $('disconnect').hidden = false;
  clearContext(); await loadContexts(true);
  if (conn === connection && !contexts.length) renderMessages();
  $('connect').disabled = false;
}
$('return-message').addEventListener('click',()=>closeDetails());
$('details-dialog').addEventListener('cancel',event=>{event.preventDefault();closeDetails();});
$('connect-form').addEventListener('submit', event => {
  event.preventDefault(); return connect($('credential').value.trim());
});
$('disconnect').addEventListener('click',()=>disconnect());
$('refresh-contexts').addEventListener('click',()=>loadContexts(true));
$('more-contexts').addEventListener('click',()=>loadContexts());
$('pause').addEventListener('click',()=>{paused=!paused;$('pause').textContent=paused?'Resume':'Pause';$('sync-status').textContent=paused?'Paused':'Updating';if(paused)clearTimeout(timer);else sync();});
$('load-more').addEventListener('click',()=>sync());
$('next-window').addEventListener('click',()=>openContext(state.contextID,state.after));
$('clear-filters').addEventListener('click',clearFilters);
$('clear-thread').addEventListener('click',()=>{thread='';displayStart=null;renderMessages();});
$('older-matches').addEventListener('click',()=>{const count=filterMessages(state.messages,filters()).length;displayStart=Math.max(0,(displayStart ?? Math.max(0,count-200))-200);renderMessages();});
$('newer-matches').addEventListener('click',()=>{const count=filterMessages(state.messages,filters()).length;displayStart=Math.min(Math.max(0,count-200),(displayStart ?? 0)+200);renderMessages();});
for (const id of ['search','agent-filter','type-filter','task-filter']) $(id).addEventListener(id === 'search' ? 'input' : 'change',()=>{route=null;displayStart=null;renderTasks();renderMessages();});
window.addEventListener('pagehide',()=>disconnect('', false));
window.addEventListener('pageshow',event=>{if(event.persisted)connect(savedCredential());});

$('show-map').addEventListener('click',()=>setView('map'));
$('show-timeline').addEventListener('click',()=>setView('timeline'));
$('show-sequence').addEventListener('click',()=>setView('sequence'));
$('clear-route').addEventListener('click',()=>{route=null;$('agent-filter').value='';displayStart=null;renderMessages();});
function setView(next) {
  view=next;
  $('interaction-panel').hidden=view!=='map';
  $('sequence-panel').hidden=view!=='sequence';
  $('messages').hidden=view==='sequence';
  for(const name of ['map','timeline','sequence']) {
    $(`show-${name}`).className=view===name?'active':'';
    $(`show-${name}`).setAttribute('aria-pressed',String(view===name));
  }
  renderMessages();
}

$('previous-connections').addEventListener('click',()=>{connectionPage=Math.max(0,connectionPage-1);renderMap();});
$('next-connections').addEventListener('click',()=>{connectionPage++;renderMap();});

function renderFlow(filtered) {
  const key=JSON.stringify([state.contextID,windowStart,filters()]);
  if(key!==sequenceFilterKey) {sequenceOptions={};sequenceFilterKey=key;}
  // Keep the page being inspected visible as new messages advance the latest page.
  const followRoutes=sequenceOptions.arrowPage==null, followTime=sequenceOptions.start==null;
  const inspecting=sequenceCurrent && (engaged('sequence-canvas') || engaged('sequence-routes'));
  sequenceCurrent=sequenceLayout(filtered,participants,{...sequenceOptions,...(inspecting?{arrowPage:sequenceCurrent.arrowPage,start:sequenceCurrent.from}:{}),span:Number($('sequence-span').value),spacing:$('sequence-spacing').value || 'order'});
  // Stay on the newest routes/time while viewing them, so polling follows new messages.
  const latestRoutes=(sequenceCurrent.arrowPage+1)*ARROW_LIMIT>=sequenceCurrent.routeCount;
  sequenceOptions={...sequenceOptions,lanePage:sequenceCurrent.lanePage,arrowPage:followRoutes||latestRoutes?null:sequenceCurrent.arrowPage,start:followTime||sequenceCurrent.to>=sequenceCurrent.max?null:sequenceCurrent.from,seekMessage:''};
  const flow=sequenceCurrent;
  renderSequence($('sequence-canvas'),flow,{selected,messages:state.messages,onSelect:selectMessage});
  $('sequence-summary').textContent=`${flow.lanes.filter(l=>l.id!==BROADCAST).length} agents · ${flow.inRange} messages in time range · ${filtered.length} matching loaded messages`;
  $('sequence-range').textContent=flow.inRange?`${date(flow.from)} – ${date(flow.to)}`:'No messages in time range';
  $('sequence-earlier').disabled=!Number($('sequence-span').value)||flow.from<=flow.min;
  $('sequence-later').disabled=!Number($('sequence-span').value)||flow.to>=flow.max;
  $('sequence-lanes').textContent=flow.lanes.length?`Lanes ${flow.lanePage*LANE_LIMIT+1}–${flow.lanePage*LANE_LIMIT+flow.visible.length} of ${flow.lanes.length}`:'No agent lanes';
  $('sequence-previous-lanes').disabled=flow.lanePage===0;
  $('sequence-next-lanes').disabled=(flow.lanePage+1)*LANE_LIMIT>=flow.lanes.length;
  const routeName=id=>id===BROADCAST?'Everyone':agentLabel(id);
  renderChildren($('sequence-routes'), ...flow.routes.map(item=>{
    const control=button('',event=>selectMessage(item.message.ID,event.currentTarget),'sequence-route');
    control.dataset.key=JSON.stringify([item.message.ID,item.target]);
    control.setAttribute('aria-pressed',String(selected===item.message.ID));
    control.append(node('span',`#${item.message.Sequence} · ${timeLabel(item.time)}`,'sequence-route-time'),node('span',`${routeName(item.source)}${instanceLabel(item.message)} → ${routeName(item.target)}${item.message.envelope?.recipient_instances?.length ? ` · instances: ${item.message.envelope.recipient_instances.join(", ")} (self-reported)` : ""}`),node('span',item.message.Body || 'Structured message','sequence-route-preview'));
    return control;
  }));
  $('sequence-arrow-count').textContent=flow.routeCount?`Routes ${flow.arrowPage*ARROW_LIMIT+1}–${flow.arrowPage*ARROW_LIMIT+flow.routes.length} of ${flow.routeCount}`:'No routes on these lanes';
  $('sequence-previous-arrows').disabled=flow.arrowPage===0;
  $('sequence-next-arrows').disabled=(flow.arrowPage+1)*ARROW_LIMIT>=flow.routeCount;
  $('sequence-note').textContent=`Only the loaded message window is shown. Other lane pages may contain more messages. ${flow.routeCount>ARROW_LIMIT?'Use route pages to see all arrows on these lanes. ':''}${flow.unknownTimes?`${flow.unknownTimes} messages have no valid timestamp; open Message list to inspect them. `:''}Coincident arrows are individually selectable in the route list. Selected messages and their loaded reply thread are highlighted.`;
}
$('sequence-span').addEventListener('change',()=>{sequenceOptions.start=null;sequenceOptions.arrowPage=null;renderMessages();});
$('sequence-spacing').addEventListener('change',()=>{sequenceOptions.arrowPage=null;renderMessages();});
for(const [id,change] of [['sequence-earlier',-1],['sequence-later',1]]) $(id).addEventListener('click',()=>{if(!sequenceCurrent)return;sequenceOptions.start=sequenceCurrent.from+change*Number($('sequence-span').value);sequenceOptions.arrowPage=null;renderMessages();});
for(const [id,change] of [['sequence-previous-lanes',-1],['sequence-next-lanes',1]]) $(id).addEventListener('click',()=>{sequenceOptions.lanePage=(sequenceOptions.lanePage||0)+change;sequenceOptions.arrowPage=null;renderMessages();});
for(const [id,change] of [['sequence-previous-arrows',-1],['sequence-next-arrows',1]]) $(id).addEventListener('click',()=>{if(!sequenceCurrent)return;sequenceOptions.arrowPage=sequenceCurrent.arrowPage+change;renderMessages();});
setView('sequence');
window.addEventListener('resize',()=>{if(window.innerWidth>960)closeDetails();if(view==='sequence'&&state.contextID)renderFlow(filterMessages(state.messages,filters()));});
await connect(savedCredential());
