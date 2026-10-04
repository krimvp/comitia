// Keep interactive nodes alive while updating their content and event closures.
const handlers = new WeakMap();
export function on(element, type, action) {
  let events = handlers.get(element);
  if (!events) { events = new Map(); handlers.set(element, events); }
  if (!events.has(type)) element.addEventListener(type, event => handlers.get(element)?.get(type)?.(event));
  events.set(type, action);
}
const key = node => node.nodeType === 1 ? node.id || node.getAttribute('data-key') || (node.nodeName === 'A' ? node.getAttribute('href') : '') || '' : '';
const sameKind = (a, b) => a.nodeType === b.nodeType && a.nodeName === b.nodeName;
function patch(current, next) {
  if (current.nodeType !== 1) {
    if (current.nodeValue !== next.nodeValue) current.nodeValue = next.nodeValue;
    return;
  }
  for (const {name} of [...current.attributes]) {
    if (name === 'open' && current.nodeName === 'DETAILS') continue;
    if (!next.hasAttribute(name)) current.removeAttribute(name);
  }
  for (const {name, value} of next.attributes) {
    if (name === 'open' && current.nodeName === 'DETAILS') continue;
    if (current.getAttribute(name) !== value) current.setAttribute(name, value);
  }
  const events = handlers.get(current);
  if (events) for (const type of events.keys()) events.set(type, null);
  for (const [type, action] of handlers.get(next) || []) on(current, type, action);
  children(current, [...next.childNodes]);
}
function children(target, next) {
  const old = [...target.childNodes], keyed = new Map();
  for (const node of old) {
    const id = key(node);
    if (!id) continue;
    if (!keyed.has(id)) keyed.set(id, []);
    keyed.get(id).push(node);
  }
  const used = new Set();
  let cursor = target.firstChild;
  for (const fresh of next) {
    const id = key(fresh);
    const current = id ? keyed.get(id)?.find(node => !used.has(node)) : old.find(node => !used.has(node) && !key(node) && sameKind(node, fresh));
    const node = current && sameKind(current, fresh) ? current : fresh;
    if (node === current) { used.add(node); patch(node, fresh); }
    if (node !== cursor) target.insertBefore(node, cursor);
    cursor = node.nextSibling;
  }
  for (const node of old) if (!used.has(node)) node.remove();
}
export function renderChildren(target, ...next) {
  const active = document.activeElement;
  children(target, next);
  // Moving keyed SVG routes to paint selection last can blur the retained node.
  if (active?.isConnected && document.activeElement !== active) active.focus({preventScroll:true});
}
