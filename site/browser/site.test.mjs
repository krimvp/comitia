import test, { before, after } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { chromium } from "playwright";

let server, browser, enlargedBrowser, base;
before(async () => {
  const options = { headless: true, ...(process.env.COMITIA_CHROMIUM ? { executablePath: process.env.COMITIA_CHROMIUM } : {}) };
  browser = await chromium.launch(options);
  enlargedBrowser = await chromium.launch({ ...options, args: ["--blink-settings=defaultFontSize=32,defaultFixedFontSize=32"] });
  server = createServer(async (req, res) => {
    try {
      const path = req.url === "/comitia/" ? "index.html" : req.url.slice("/comitia/".length);
      if (!req.url.startsWith("/comitia/") || !/^[\w./-]+$/.test(path) || path.includes("..")) throw new Error("bad path");
      res.setHeader("Content-Type", path.endsWith(".css") ? "text/css" : path.endsWith(".html") ? "text/html" : path.endsWith(".js") ? "text/javascript" : "application/octet-stream");
      res.end(await readFile(new URL(`../${path}`, import.meta.url)));
    } catch { res.writeHead(404).end(); }
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  base = `http://127.0.0.1:${server.address().port}/comitia/`;
});
after(async () => {
  await browser?.close();
  await enlargedBrowser?.close();
  if (server) await new Promise(resolve => server.close(resolve));
});

async function pageFor(t, width, enlarged = false) {
  const page = await (enlarged ? enlargedBrowser : browser).newPage({ viewport: { width, height: 900 }, reducedMotion: "reduce" });
  t.after(() => page.close());
  await page.goto(base);
  await page.evaluate(() => document.fonts.ready);
  return page;
}

async function labelSizes(page) {
  return page.locator(".diagram:visible text").evaluateAll(nodes => nodes.map(node => ({
    text: node.textContent,
    size: parseFloat(getComputedStyle(node).fontSize) * node.getScreenCTM().a,
  })));
}

for (const width of [320, 390, 559, 560, 561, 999, 1000, 1001, 1440]) {
  test(`figure labels stay readable at ${width}px`, async t => {
    const page = await pageFor(t, width);
    const labels = await labelSizes(page);
    assert.ok(labels.length > 0);
    for (const label of labels) assert.ok(label.size >= 12, `${label.text}: ${label.size.toFixed(2)}px`);
    const legend = page.locator(".figure-legend");
    assert.equal(await legend.count(), 1, "explanations must wrap as HTML text");
    const sizes = await legend.evaluate(node => [node, ...node.querySelectorAll("li, strong, code")].map(part => parseFloat(getComputedStyle(part).fontSize)));
    assert.ok(sizes.every(size => size >= 13), `legend text sizes: ${sizes}`);
    assert.match(await legend.innerText(), /read through.*4.*7/s);
    assert.match(await legend.innerText(), /10/);
    assert.match(await legend.innerText(), /expires unless renewed/);
    assert.match(await legend.innerText(), /private/);
  });
}

test("a narrow figure in a wide viewport chooses the stacked drawing", async t => {
  const page = await pageFor(t, 1440);
  await page.locator(".hero-fig").evaluate(node => node.style.width = "300px");
  assert.equal(await page.locator(".diagram-narrow").isVisible(), true);
  assert.equal(await page.locator(".diagram-wide").isVisible(), false);
  for (const label of await labelSizes(page)) assert.ok(label.size >= 12, label.text);
});

test("figure selection follows the container boundary without shrinking labels", async t => {
  const page = await pageFor(t, 1440);
  const sizes = [];
  for (const width of [599, 600, 601]) {
    await page.locator(".hero-fig").evaluate((node, width) => node.style.width = `${width}px`, width);
    assert.equal(await page.locator(".diagram-wide").isVisible(), width >= 600);
    assert.equal(await page.locator(".diagram-narrow").isVisible(), width < 600);
    sizes.push(Math.min(...(await labelSizes(page)).map(label => label.size)));
  }
  assert.ok(Math.min(...sizes) / Math.max(...sizes) >= 0.9, `label sizes: ${sizes}`);
});

for (const width of [390, 1440]) {
  test(`practical benefit and setup link are on the first screen at ${width}px`, async t => {
    const page = await pageFor(t, width);
    for (const selector of [".lede", ".hero-links .button"]) {
      const paragraph = page.locator(selector);
      const rect = await paragraph.boundingBox();
      assert.ok(rect.y >= 0 && rect.y + rect.height <= 900, `copy below the first screen: ${selector}`);
    }
  });
}

for (const boundary of [560, 1000]) {
  test(`labels do not shrink abruptly across ${boundary}px`, async t => {
    const sizes = [];
    for (const width of [boundary - 1, boundary, boundary + 1]) {
      const page = await pageFor(t, width);
      sizes.push(Math.min(...(await labelSizes(page)).map(label => label.size)));
    }
    assert.ok(Math.min(...sizes) / Math.max(...sizes) >= 0.9, `label sizes: ${sizes}`);
  });
}

for (const width of [320, 390]) {
  test(`200% preferred text fits at ${width}px`, async t => {
    const page = await pageFor(t, width, true);
    assert.equal(await page.evaluate(() => parseFloat(getComputedStyle(document.documentElement).fontSize)), 32);
    const overflow = await page.evaluate(() => {
      const width = document.documentElement.clientWidth;
      return {
        page: document.documentElement.scrollWidth - width,
        elements: [...document.querySelectorAll("h1, h2, h3, p, a, button, li, .hero-text, .hero-fig, .sheet, .spec, pre")]
          .filter(node => !node.classList.contains("skip"))
          .filter(node => { const rect = node.getBoundingClientRect(); return rect.left < -1 || rect.right > width + 1 || (node.tagName !== "PRE" && node.scrollWidth > node.clientWidth + 1); })
          .map(node => node.outerHTML.slice(0, 100)),
      };
    });
    assert.ok(overflow.page <= 1, `page overflows by ${overflow.page}px`);
    assert.deepEqual(overflow.elements, []);
  });
}

const setupPrompt = "Set up Comitia from https://github.com/krimvp/comitia: follow skills/comitia/SKILL.md.";

for (const colorScheme of ["light", "dark"]) {
  test(`keyboard copy writes the exact prompt in ${colorScheme} theme`, async t => {
    const page = await browser.newPage({ colorScheme, viewport: { width: 390, height: 900 } });
    t.after(() => page.close());
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
    await page.goto(base);
    await page.locator("#copy-prompt").focus();
    await page.keyboard.press("Enter");
    await page.waitForFunction(() => document.getElementById("copy-status").textContent === "Setup prompt copied.");
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), setupPrompt);
    assert.deepEqual(errors, []);
  });
}

for (const failure of ["unavailable", "denied"]) {
  test(`clipboard ${failure} selects the prompt and explains manual copy`, async t => {
    const page = await pageFor(t, 320);
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    await page.evaluate(failure => {
      Object.defineProperty(navigator, "clipboard", { value: failure === "unavailable" ? undefined : {
        writeText: () => Promise.reject(new Error("denied")),
      } });
    }, failure);
    await page.locator("#copy-prompt").click();
    await page.waitForFunction(() => document.getElementById("copy-status").textContent.includes("Copy the selected prompt"));
    assert.equal(await page.evaluate(() => window.getSelection().toString()), setupPrompt);
    assert.equal(await page.locator("#setup-prompt").evaluate(node => document.activeElement === node), true);
    assert.deepEqual(errors, []);
  });
}

for (const failure of ["JavaScript disabled", "copy script blocked"]) {
  test(`setup stays usable with ${failure}`, async t => {
    const page = await browser.newPage({ javaScriptEnabled: failure !== "JavaScript disabled", viewport: { width: 320, height: 900 } });
    t.after(() => page.close());
    if (failure === "copy script blocked") await page.route("**/copy.js", route => route.abort());
    await page.goto(base);
    assert.equal(await page.locator("#copy-prompt").isVisible(), false);
    const prompt = page.locator("#setup-prompt");
    assert.equal(await prompt.innerText(), setupPrompt);
    await prompt.focus();
    assert.equal(await prompt.getAttribute("tabindex"), "0");
    await page.locator(".hero-links .button").click();
    assert.equal(new URL(page.url()).hash, "#start");
    assert.equal(await page.locator(".examples li").count(), 2);
    assert.equal(await page.locator(".doc").isVisible(), true);
  });
}

test("prompt wraps and scales once on mobile", async t => {
  const page = await pageFor(t, 320);
  const metrics = await page.locator("#setup-prompt").evaluate(node => ({
    pre: parseFloat(getComputedStyle(node).fontSize),
    code: parseFloat(getComputedStyle(node.querySelector("code")).fontSize),
    overflow: node.scrollWidth - node.clientWidth,
    whitespace: getComputedStyle(node).whiteSpace,
  }));
  assert.equal(metrics.code, metrics.pre);
  assert.equal(metrics.whitespace, "pre-wrap");
  assert.ok(metrics.overflow <= 1);
});
