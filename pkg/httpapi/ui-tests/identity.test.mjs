import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const ui = new URL('../ui/', import.meta.url);
const css = readFileSync(new URL('style.css', ui), 'utf8');
const html = readFileSync(new URL('index.html', ui), 'utf8');
const site = new URL('../../../site/', import.meta.url);
const siteCSS = readFileSync(new URL('style.css', site), 'utf8');
const tokens = block => Object.fromEntries([...block.matchAll(/--([a-z-]+):\s*(#[0-9a-f]{6})/gi)].map(m => [m[1], m[2]]));
function themes(css) {
  const start = css.indexOf('@media (prefers-color-scheme: dark)');
  const light = tokens(css.slice(css.indexOf(':root'), start));
  return {light, dark:{...light, ...tokens(css.slice(start, css.indexOf('\n}', start)))}};
}
function contrast(a, b) {
  const lum = hex => {
    const [r,g,b] = [1,3,5].map(i => {
      const c = parseInt(hex.slice(i,i+2),16)/255;
      return c <= .04045 ? c/12.92 : ((c+.055)/1.055)**2.4;
    });
    return .2126*r + .7152*g + .0722*b;
  };
  const [hi,lo] = [lum(a),lum(b)].sort((x,y)=>y-x);
  return (hi+.05)/(lo+.05);
}

test('inspector shares the site drafting palette in both themes', () => {
  for (const [theme,t] of Object.entries(themes(css))) {
    const reference = themes(siteCSS)[theme];
    for (const name of ['paper','panel','ink','muted','code-bg','live','live-fill']) assert.equal(t[name], reference[name], `${theme} ${name}`);
    assert.equal(t.blue,reference.accent);
  }
  assert.match(html, /name="color-scheme" content="light dark"/);
  assert.match(html, /<svg class="brandmark" aria-hidden="true"/);
  assert.match(css, /\.connect-form::before,\.connect-form::after/);
});

test('all text token pairs meet WCAG AA in light and dark', () => {
  const pairs = [
    ...['ink','muted','blue'].flatMap(fg => ['paper','panel','selected','code-bg'].map(bg => [fg,bg])),
    ['on-accent','blue'], ['live','live-fill'], ['warning','warning-bg'], ['related','related-bg'],
    ['error','paper'], ['error','panel'],
  ];
  for (const [theme,t] of Object.entries(themes(css))) for (const [fg,bg] of pairs) {
    assert.ok(t[fg] && t[bg], `${theme}: missing ${fg}/${bg}`);
    const ratio = contrast(t[fg],t[bg]);
    assert.ok(ratio>=4.5, `${theme} ${fg}/${bg}: ${ratio.toFixed(2)}:1`);
  }
});

test('Plex fonts are local, unmodified site assets with their OFL license', () => {
  assert.doesNotMatch(css, /@import/);
  const urls = [...css.matchAll(/url\("([^"]+)"\)/g)].map(m=>m[1]);
  assert.equal(urls.length,4);
  for (const path of urls) {
    assert.match(path, /^IBMPlex(Sans|Mono)-(Regular|SemiBold)-Latin1\.woff2$/);
    assert.deepEqual(readFileSync(new URL(path,ui)),readFileSync(new URL(`assets/fonts/${path}`,site)));
  }
  assert.deepEqual(readFileSync(new URL('OFL.txt',ui)),readFileSync(new URL('assets/fonts/OFL.txt',site)));
  assert.equal((css.match(/font-display: swap/g)||[]).length,4);
});

test('control boundaries and focus accent meet non-text contrast', () => {
  for (const [theme,t] of Object.entries(themes(css))) for (const bg of ['paper','panel']) {
    for (const fg of ['control-line','blue']) assert.ok(contrast(t[fg],t[bg])>=3, `${theme} ${fg}/${bg}`);
  }
});

test('dark navigation tokens remain readable independently of the conversation theme', () => {
  const overrides = tokens([...css.matchAll(/\.sidebar\{([^}]+)\}/g)].map(m=>m[1]).join(';'));
  for (const [theme,base] of Object.entries(themes(css))) {
    const t={...base,...overrides};
    for (const [fg,bg] of [['ink','sidebar-bg'],['muted','sidebar-bg'],['blue','sidebar-bg'],['muted','selected'],['blue','selected'],['live','live-fill'],['warning','warning-bg'],['related','related-bg']]) {
      assert.ok(contrast(t[fg],t[bg])>=4.5,`${theme} sidebar ${fg}/${bg}`);
    }
  }
});
