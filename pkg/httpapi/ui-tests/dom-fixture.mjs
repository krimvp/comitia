// Minimal DOM seam for controller tests; real focus/layout checks live in browser/.
class Text {
  constructor(value) {this.nodeType=3;this.nodeName='#text';this.nodeValue=value;}
  get nextSibling() {return this.parentNode?.childNodes[this.parentNode.childNodes.indexOf(this)+1] || null;}
  remove() {if(this.parentNode) this.parentNode.childNodes.splice(this.parentNode.childNodes.indexOf(this),1);this.parentNode=null;}
}
const reflected = {class:'className',id:'id',title:'title',type:'type',href:'href',target:'target',rel:'rel',value:'value'};
export class Element {
  constructor(tag='div') {this.tag=tag;this.nodeType=1;this.nodeName=tag.toUpperCase();this.childNodes=[];this.events={};this.dataset={};this.value='';this.hidden=false;this._attrs=new Map();}
  get children() {return this.childNodes.filter(node=>node.nodeType===1);}
  get firstChild() {return this.childNodes[0] || null;}
  get nextSibling() {return this.parentNode?.childNodes[this.parentNode.childNodes.indexOf(this)+1] || null;}
  get textContent() {return this.childNodes.map(node=>node.nodeType===3?node.nodeValue:node.textContent).join('');}
  set textContent(value) {this.replaceChildren();if(value) {const node=new Text(String(value));node.parentNode=this;this.childNodes.push(node);}}
  get attributes() {
    const attrs=new Map(this._attrs);
    for(const [name,prop] of Object.entries(reflected)) if(this[prop]!==undefined) attrs.set(name,String(this[prop]));
    for(const [name,value] of Object.entries(this.dataset)) attrs.set(`data-${name}`,String(value));
    return [...attrs].map(([name,value])=>({name,value}));
  }
  getAttribute(name) {return this.attributes.find(attr=>attr.name===name)?.value ?? null;}
  hasAttribute(name) {return this.getAttribute(name)!==null;}
  removeAttribute(name) {this._attrs.delete(name);if(name.startsWith('data-'))delete this.dataset[name.slice(5)];else delete this[reflected[name] || name];}
  setAttribute(name,value) {this._attrs.set(name,String(value));if(name.startsWith('data-'))this.dataset[name.slice(5)]=String(value);else this[reflected[name] || name]=String(value);}
  append(...children) {for(const child of children)this.insertBefore(child,null);}
  insertBefore(child,before) {child.remove();const index=before?this.childNodes.indexOf(before):this.childNodes.length;this.childNodes.splice(index,0,child);child.parentNode=this;}
  remove() {if(this.parentNode)this.parentNode.childNodes.splice(this.parentNode.childNodes.indexOf(this),1);this.parentNode=null;}
  replaceChildren(...children) {for(const child of [...this.childNodes])child.remove();this.append(...children);}
  add(child) {this.append(child);}
  addEventListener(event,fn) {this.events[event]=fn;}
  contains(node) {return node===this || this.children.some(child=>child.contains(node));}
  focus() {}
  scrollIntoView() {}
  querySelector() {return this.children.find(c=>c.tag==='button');}
  async fire(event) {return this.events[event]?.({currentTarget:this,preventDefault(){}});}
}
