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
  assert.doesNotMatch(css, /url\(|@import/, "style.css must not load anything");
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

test("figures drawn as svg are labelled", () => {
  const svgs = html.match(/<svg\b[^>]*>/g) ?? [];
  assert.ok(svgs.length > 0);
  for (const svg of svgs) {
    const ids = svg.match(/aria-labelledby="([^"]+)"/);
    assert.ok(ids, svg);
    for (const id of ids[1].split(/\s+/)) assert.match(html, new RegExp(`id="${id}"`));
  }
});
