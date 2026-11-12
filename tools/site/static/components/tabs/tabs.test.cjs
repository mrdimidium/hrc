// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

// Exercise the shipped bundle without adding a browser or DOM dependency.
// CSS layout, print rendering, and assistive-technology behavior still need browser review.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");
const bundle = fs.readFileSync(0, "utf8");
const labels = ["Windows", "macOS", "Linux / Systemd", "Linux / OpenRC"];

function fixture(options = {}) {
  let activeElement = null;
  class Element {
    constructor(tagName) {
      this.tagName = tagName;
      this.children = [];
      this.attributes = {};
      this.dataset = {};
      this.events = {};
      this.className = "";
      this.id = "";
      this.hidden = false;
      this.text = "";
      this.scrolls = 0;
      this.classList = { contains: (name) => this.className.split(" ").includes(name) };
    }
    get textContent() { return this.text + this.children.map((child) => child.textContent).join(""); }
    set textContent(value) { this.text = value; this.children = []; }
    append(child) { child.parentElement = this; this.children.push(child); }
    prepend(child) {
      child.parentElement = this;
      this.children.unshift(child);
      if (this.failPrepend) throw new Error("simulated DOM failure");
    }
    remove() {
      if (this.parentElement) {
        this.parentElement.children = this.parentElement.children.filter((child) => child !== this);
      }
    }
    setAttribute(name, value) { this.attributes[name] = value; }
    removeAttribute(name) { delete this.attributes[name]; }
    addEventListener(name, fn) { this.events[name] = fn; }
    focus() { activeElement = this; }
    scrollIntoView() { this.scrolls++; }
    contains(target) { return target === this || this.children.some((child) => child.contains(target)); }
    querySelector() { return this.children.find((child) => child.classList.contains("tabs__title")) || null; }
    querySelectorAll(selector) {
      assert.equal(selector, ".headerlink");
      return this.children.flatMap((child) => [
        ...(child.classList.contains("headerlink") ? [child] : []), ...child.querySelectorAll(selector),
      ]);
    }
    cloneNode() {
      const copy = new Element(this.tagName);
      copy.text = this.text;
      copy.className = this.className;
      for (const child of this.children) copy.append(child.cloneNode());
      return copy;
    }
  }

  const roots = ["a", "b", "c"].map((prefix) => {
    const root = new Element("div");
    root.className = "tabs";
    root.dataset.tabsGroup = prefix === "c" ? "other-group" : "host-platform";
    for (const [index, label] of labels.entries()) {
      const panel = new Element("section");
      panel.className = "tabs__panel";
      panel.id = `${prefix}-${index}`;
      const title = new Element("h3");
      title.className = "tabs__title";
      title.text = label;
      const permalink = new Element("a");
      permalink.className = "headerlink";
      permalink.text = "Permalink";
      title.append(permalink);
      const content = new Element("p");
      content.id = `${prefix}-${index}-content`;
      panel.append(title);
      panel.append(content);
      root.append(panel);
    }
    return root;
  });
  const documentEvents = {};
  const windowEvents = {};
  const location = { hash: options.hash || "" };
  const storage = options.storage || new Map();
  const errors = [];
  const document = {
    readyState: options.loading ? "loading" : "interactive",
    querySelectorAll: () => roots,
    createElement: (tag) => new Element(tag),
    getElementById(id) {
      function find(element) {
        if (element.id === id && id) return element;
        for (const child of element.children) { const found = find(child); if (found) return found; }
        return null;
      }
      for (const root of roots) { const found = find(root); if (found) return found; }
      return null;
    },
    addEventListener: (name, fn) => { documentEvents[name] = fn; },
  };
  const context = {
    HTMLElement: Element, document, location,
    window: { addEventListener: (name, fn) => { windowEvents[name] = fn; } },
    console: { warn: (...args) => errors.push(args) },
  };
  Object.defineProperty(context, "localStorage", { get() {
    if (options.noStorage) throw new Error("storage denied");
    return { getItem: (key) => storage.get(key) || null, setItem: (key, value) => storage.set(key, value) };
  }});
  if (options.breakFirst) roots[0].children[0].children = [];
  if (options.failFirst) roots[0].failPrepend = true;
  const panels = roots.map((root) => [...root.children]);
  const run = () => vm.runInNewContext(bundle, context);
  const buttons = (index = 0) => roots[index].children[0].children;
  const selected = (index = 0) => panels[index].findIndex((panel) => !panel.hidden);
  const key = (button, value) => {
    let prevented = false;
    button.events.keydown({ key: value, preventDefault: () => { prevented = true; } });
    return prevented;
  };
  return { roots, panels, run, buttons, selected, key, storage, errors, location, documentEvents, windowEvents,
    active: () => activeElement };
}

{
  const page = fixture();
  assert.ok(page.panels.flat().every((panel) => !panel.hidden));
  page.run();
  assert.deepEqual(page.buttons().map((button) => button.textContent), labels);
  assert.equal(page.selected(), 0);
  assert.equal(page.roots[0].dataset.enhanced, "true");
  page.buttons().forEach((button, index) => {
    assert.equal(button.attributes.role, "tab");
    assert.equal(button.attributes["aria-controls"], page.panels[0][index].id);
    assert.equal(button.tabIndex, index === 0 ? 0 : -1);
    assert.equal(page.panels[0][index].attributes["aria-labelledby"], button.id);
  });
  page.buttons()[3].events.click();
  assert.equal(page.selected(), 3);
  assert.equal(page.selected(1), 3);
  assert.equal(page.selected(2), 0);
  assert.equal(page.storage.get("hrc:tabs:host-platform"), "Linux / OpenRC");
  assert.ok(page.key(page.buttons()[3], "ArrowRight"));
  assert.equal(page.selected(), 0);
  assert.equal(page.active(), page.buttons()[0]);
  page.key(page.buttons()[0], "ArrowLeft");
  assert.equal(page.selected(), 3);
  page.key(page.buttons()[3], "Home");
  assert.equal(page.selected(), 0);
  page.key(page.buttons()[0], "End");
  assert.equal(page.selected(), 3);
  assert.equal(page.key(page.buttons()[3], "Tab"), false);
  const nextPage = fixture({ storage: page.storage });
  nextPage.run();
  assert.equal(nextPage.selected(), 3);
}
{
  const page = fixture({ noStorage: true });
  page.run();
  page.buttons()[1].events.click();
  assert.equal(page.selected(), 1);
  assert.equal(page.selected(1), 1);
  assert.equal(page.errors.length, 0);
}
{
  const storage = new Map([["hrc:tabs:host-platform", "macOS"]]);
  const page = fixture({ hash: "#b-2-content", storage });
  page.run();
  assert.equal(page.selected(), 2);
  assert.equal(page.selected(1), 2);
  assert.equal(page.panels[1][2].children[1].scrolls, 1);
  page.location.hash = "#a-3";
  page.windowEvents.hashchange();
  assert.equal(page.selected(), 3);
  assert.equal(page.selected(1), 3);
}
{
  const page = fixture({ loading: true, hash: "#%invalid" });
  page.run();
  assert.equal(page.roots[0].dataset.enhanced, undefined);
  page.documentEvents.DOMContentLoaded();
  assert.equal(page.selected(), 0);
}
for (const option of ["breakFirst", "failFirst"]) {
  const page = fixture({ [option]: true });
  page.run();
  assert.equal(page.roots[0].dataset.enhanced, undefined);
  assert.equal(page.roots[0].children.length, 4);
  assert.ok(page.panels[0].every((panel) => !panel.hidden && !panel.attributes.role));
  assert.equal(page.roots[1].dataset.enhanced, "true");
}
console.log("Tabs runtime checks passed");
