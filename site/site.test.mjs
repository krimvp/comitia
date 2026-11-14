import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(fileURLToPath(import.meta.url));
const html = readFileSync(join(root, "index.html"), "utf8");
const css = readFileSync(join(root, "style.css"), "utf8");
const allowedExternal = /^https:\/\/github\.com\/krimvp\/comitia(\/|$|#)/;

function attrs(name) {
  return [...html.matchAll(new RegExp(`\\s${name}="([^"]*)"`, "g"))].map((m) => m[1]);
}

const refs = [...attrs("src"), ...attrs("href")];

test("only GitHub links leave the page", () => {
  const external = refs.filter((r) => /^[a-z][a-z0-9+.-]*:|^\/\//i.test(r));
  assert.ok(external.length > 0);
  for (const r of external) assert.match(r, allowedExternal, r);
  assert.doesNotMatch(css, /@import/);
  for (const [, u] of css.matchAll(/url\("?([^")]+)"?\)/g)) {
    assert.doesNotMatch(u, /^[a-z][a-z0-9+.-]*:|^\/\//i, `style.css loads ${u}`);
    assert.ok(existsSync(join(root, u)), `missing file ${u}`);
  }
});

test("relative links resolve to files and fragments to ids", () => {
  const ids = new Set(attrs("id"));
  for (const r of refs.filter((r) => !/^[a-z][a-z0-9+.-]*:|^\/\//i.test(r))) {
    assert.ok(!r.startsWith("/"), `root-relative link breaks under a sub-path: ${r}`);
    const [path, fragment] = r.split("#");
    if (path === "" || path === "./") {
      if (fragment) assert.ok(ids.has(fragment), `missing id #${fragment}`);
      continue;
    }
    assert.ok(existsSync(join(root, path)), `missing file ${path}`);
  }
});

test("every img has alt and the page has one h1", () => {
  const imgs = html.match(/<img\b[^>]*>/g) ?? [];
  assert.ok(imgs.length > 0);
  for (const img of imgs) assert.match(img, /\salt="[^"]*"/, img);
  assert.equal((html.match(/<h1\b/g) ?? []).length, 1);
});

test("svg figures are labelled and decorative svg is hidden", () => {
  const svgs = html.match(/<svg\b[^>]*>/g) ?? [];
  assert.ok(svgs.length > 0);
  for (const svg of svgs) {
    if (/aria-hidden="true"/.test(svg)) continue;
    const ids = svg.match(/aria-labelledby="([^"]+)"/);
    assert.ok(ids, svg);
    for (const id of ids[1].split(/\s+/)) assert.match(html, new RegExp(`id="${id}"`));
  }
});

function tokens(block) {
  return Object.fromEntries([...block.matchAll(/--([a-z-]+):\s*(#[0-9a-f]{6})/gi)].map((m) => [m[1], m[2]]));
}

function contrast(a, b) {
  const lum = (hex) => {
    const [r, g, b] = [1, 3, 5].map((i) => {
      const c = parseInt(hex.slice(i, i + 2), 16) / 255;
      return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

test("text colour pairs meet WCAG AA in both themes", () => {
  const light = tokens(css.slice(css.indexOf(":root {"), css.indexOf("@media (prefers-color-scheme: dark)")));
  const darkStart = css.indexOf("@media (prefers-color-scheme: dark)");
  const dark = { ...light, ...tokens(css.slice(darkStart, css.indexOf("}\n}", darkStart))) };
  const pairs = [
    ["ink", "paper"], ["ink", "panel"], ["ink", "code-bg"], ["ink", "live-fill"],
    ["muted", "paper"], ["muted", "panel"], ["muted", "code-bg"],
    ["accent", "paper"], ["accent", "panel"], ["accent", "live-fill"],
    ["live", "live-fill"], ["live", "panel"],
  ];
  for (const [theme, t] of [["light", light], ["dark", dark]]) {
    for (const [fg, bg] of pairs) {
      assert.ok(t[fg] && t[bg], `${theme}: missing --${fg} or --${bg}`);
      const ratio = contrast(t[fg], t[bg]);
      assert.ok(ratio >= 4.5, `${theme} --${fg} on --${bg} is ${ratio.toFixed(2)}:1`);
    }
  }
  assert.match(css, /\.t-fig \{[^}]*fill: var\(--muted\)/, "fig. 1 label uses a text colour");
});

test("the home link opens the page itself", () => {
  const home = html.match(/<a class="brand" href="([^"]*)"/);
  assert.ok(home);
  assert.equal(home[1], "index.html");
});

test("hero has one short practical sentence", () => {
  const hero = html.slice(html.indexOf('<div class="hero-text">'), html.indexOf('<figure class="hero-fig">'));
  const lede = hero.match(/<p class="lede">([^<]+)<\/p>/)?.[1];
  assert.match(lede, /hand off/i);
  assert.match(lede, /agents you already run/i);
  assert.ok(lede.split(/\s+/).length <= 15);
  assert.equal((hero.match(/<p\b/g) ?? []).length, 2, "one sentence plus navigation");
  assert.doesNotMatch(hero, /SQLite|Go service|application API/);
});

test("capabilities and limits stay short", () => {
  for (const id of ["does", "not"]) {
    const section = html.match(new RegExp(`<section id="${id}"[\\s\\S]*?<\\/section>`))[0];
    const items = [...section.matchAll(/<dd>([\s\S]*?)<\/dd>/g)];
    assert.ok(items.length > 0 && items.length <= 3);
    for (const [, text] of items) assert.ok(text.split(/\s+/).length <= 20, text);
  }
  assert.match(html, /does not launch, execute or resume agents/);
  assert.match(html, /does not mean a recipient is running or reading/);
  assert.match(html, /including addressed ones/);
});

test("get started is the shared agent prompt and two examples", () => {
  const start = html.slice(html.indexOf('<section id="start"'), html.indexOf("</main>"));
  assert.match(start, /<pre id="setup-prompt" tabindex="0"><code>Set up Comitia from https:\/\/github\.com\/krimvp\/comitia: follow skills\/comitia\/SKILL\.md\.<\/code><\/pre>/);
  assert.match(start, /Use Comitia to hand this review to Codex and wait for its verdict\./);
  assert.match(start, /Orchestrate issues #12-#15 through Comitia: one Codex worker per issue, a reviewer for each, merge when CI is green\./);
  assert.equal((start.match(/<li>/g) ?? []).length, 2);
  assert.equal((start.match(/<a /g) ?? []).length, 1);
  assert.match(start, /href="https:\/\/github\.com\/krimvp\/comitia\/blob\/main\/docs\/local-service\.md">Manual setup docs/);
  assert.match(start, /<button[^>]+type="button" hidden>/);
  assert.match(start, /role="status"/);
  assert.doesNotMatch(html, /install_linux_service|install_macos_service|--[a-z]|comitia-client/);
});

test("Pages checks the checkout before staging and uploading the site", () => {
  const workflow = readFileSync(join(root, "../.github/workflows/pages.yml"), "utf8");
  const build = workflow.slice(workflow.indexOf("  build:"), workflow.indexOf("  deploy:"));
  const setup = build.indexOf("actions/setup-node@");
  const checks = build.indexOf("node --test site/*.test.mjs");
  const browser = build.indexOf("node --test site/browser/*.test.mjs");
  const stage = build.indexOf("rsync");
  const upload = build.indexOf("actions/upload-pages-artifact@");
  assert.ok(setup >= 0 && setup < checks && checks < browser && browser < stage && stage < upload);
  // Actions are pinned to commit SHAs, and only the deploy job may write to Pages.
  for (const use of workflow.matchAll(/uses: (\S+)/g)) assert.match(use[1], /@[0-9a-f]{40}$/);
  assert.doesNotMatch(workflow.slice(0, workflow.indexOf("  deploy:")), /pages: write|id-token: write/);
  assert.match(build, /node-version: 22/);
  assert.match(build, /npm ci --prefix site\/browser/);
  assert.match(build, /playwright install --with-deps chromium/);
  assert.doesNotMatch(build, /continue-on-error|if:.*always|\|\|\s*true/);
  assert.match(build, /--exclude 'browser\/'/);
  assert.match(workflow, /deploy:\s+needs: build/);
});

test("the page describes itself to link previews", () => {
  const description = html.match(/<meta name="description" content="([^"]+)">/)[1];
  for (const [attr, name, value] of [
    ["property", "og:type", "website"], ["property", "og:site_name", "Comitia"], ["property", "og:title", "Comitia"],
    ["property", "og:description", description], ["name", "twitter:card", "summary"],
    ["name", "twitter:title", "Comitia"], ["name", "twitter:description", description],
  ]) assert.match(html, new RegExp(`<meta ${attr}="${name}" content="${value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}">`), name);
});

test("the footer links the documentation", () => {
  const foot = html.slice(html.indexOf('<footer class="foot">'), html.indexOf("</footer>"));
  const nav = foot.match(/<nav class="foot-docs" aria-label="Documentation">([\s\S]*?)<\/nav>/)?.[1];
  assert.ok(nav, "documentation links in the footer");
  const links = [...nav.matchAll(/<a href="([^"]+)">([^<]+)<\/a>/g)].map((m) => [m[2], m[1]]);
  assert.deepEqual(links, [
    ["README", "https://github.com/krimvp/comitia#readme"],
    ["Documentation", "https://github.com/krimvp/comitia/blob/main/docs/README.md"],
    ["Architecture", "https://github.com/krimvp/comitia/blob/main/docs/architecture.md"],
    ["Contributing", "https://github.com/krimvp/comitia/blob/main/CONTRIBUTING.md"],
  ]);
  const prefix = "https://github.com/krimvp/comitia/blob/main/";
  for (const [, url] of links.filter(([, url]) => url.startsWith(prefix))) {
    assert.ok(existsSync(join(root, "..", url.slice(prefix.length))), `missing ${url}`);
  }
});
