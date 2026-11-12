/*!
 * SPDX-FileCopyrightText: 2026 Nikolay Govorov
 * SPDX-License-Identifier: BSD-3-Clause
 */

interface TabSet {
  root: HTMLElement;
  group: string;
  panels: HTMLElement[];
  buttons: HTMLButtonElement[];
  labels: string[];
}

const tabSets: TabSet[] = [];
const choices = new Map<string, string>();

function savedChoice(group: string): string | null {
  if (!group) return null;
  if (choices.has(group)) return choices.get(group)!;
  try {
    return localStorage.getItem(`hrc:tabs:${group}`);
  } catch {
    // Storage can be disabled; the component still works within this page.
    return null;
  }
}

function activate(tabs: TabSet, index: number): void {
  tabs.panels.forEach((panel, position) => {
    const selected = position === index;
    panel.hidden = !selected;
    tabs.buttons[position].setAttribute("aria-selected", String(selected));
    tabs.buttons[position].tabIndex = selected ? 0 : -1;
  });
}

function choose(tabs: TabSet, index: number): void {
  activate(tabs, index);
  if (!tabs.group) return;
  const label = tabs.labels[index];
  choices.set(tabs.group, label);
  for (const other of tabSets) {
    const match = other.labels.indexOf(label);
    if (other !== tabs && other.group === tabs.group && match !== -1) {
      activate(other, match);
    }
  }
  try {
    localStorage.setItem(`hrc:tabs:${tabs.group}`, label);
  } catch {
    // Remembering a preference must never be required to switch tabs.
  }
}

function hashTarget(): HTMLElement | null {
  try {
    return document.getElementById(decodeURIComponent(location.hash.slice(1)));
  } catch {
    return null;
  }
}

function revealTarget(): void {
  const target = hashTarget();
  if (!target) return;
  // An inner tab set can itself be inside an outer tab panel.
  for (const tabs of tabSets) {
    const index = tabs.panels.findIndex((panel) => panel.contains(target));
    if (index !== -1) choose(tabs, index);
  }
  target.scrollIntoView({ block: "start" });
}

function enhance(root: HTMLElement): void {
  const panels = Array.from(root.children).filter(
    (child): child is HTMLElement => child instanceof HTMLElement && child.classList.contains("tabs__panel"),
  );
  const headings = panels.map((panel) => panel.querySelector<HTMLElement>(":scope > .tabs__title"));
  if (panels.length < 2 || headings.some((heading) => !heading) || panels.some((panel) => !panel.id)) return;

  const labels = headings.map((heading) => {
    const copy = heading!.cloneNode(true) as HTMLElement;
    copy.querySelectorAll(".headerlink").forEach((link) => link.remove());
    return copy.textContent!.trim();
  });
  if (labels.some((label) => !label) || new Set(labels).size !== labels.length) return;

  const list = document.createElement("div");
  list.className = "tabs__list";
  list.setAttribute("role", "tablist");
  list.setAttribute("aria-label", root.dataset.tabsLabel || "Variants");
  list.setAttribute("aria-orientation", "horizontal");
  const tabs: TabSet = { root, group: root.dataset.tabsGroup || "", panels, labels, buttons: [] };

  // Build the controls off-DOM. Until the final commit all original content stays visible.
  tabs.buttons = panels.map((panel, index) => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "tabs__tab";
    button.id = `${panel.id}-tab`;
    button.textContent = labels[index];
    button.setAttribute("role", "tab");
    button.setAttribute("aria-controls", panel.id);
    button.addEventListener("click", () => choose(tabs, index));
    button.addEventListener("keydown", (event) => {
      let next: number;
      switch (event.key) {
        case "ArrowRight": next = (index + 1) % panels.length; break;
        case "ArrowLeft": next = (index + panels.length - 1) % panels.length; break;
        case "Home": next = 0; break;
        case "End": next = panels.length - 1; break;
        default: return;
      }
      event.preventDefault();
      choose(tabs, next);
      tabs.buttons[next].focus();
    });
    list.append(button);
    return button;
  });

  const target = hashTarget();
  const linked = target ? panels.findIndex((panel) => panel.contains(target)) : -1;
  const saved = labels.indexOf(savedChoice(tabs.group) || "");
  const initial = linked !== -1 ? linked : saved !== -1 ? saved : 0;
  try {
    panels.forEach((panel, index) => {
      panel.setAttribute("role", "tabpanel");
      panel.setAttribute("aria-labelledby", tabs.buttons[index].id);
      panel.tabIndex = 0;
    });
    root.prepend(list);
    activate(tabs, initial);
    root.dataset.enhanced = "true";
    tabSets.push(tabs);
  } catch (error) {
    list.remove();
    delete root.dataset.enhanced;
    panels.forEach((panel) => {
      panel.hidden = false;
      panel.removeAttribute("role");
      panel.removeAttribute("aria-labelledby");
      panel.removeAttribute("tabindex");
    });
    throw error;
  }
}

function initialize(): void {
  document.querySelectorAll<HTMLElement>(".tabs").forEach((root) => {
    try {
      enhance(root);
    } catch (error) {
      console.warn("Tabs unavailable; showing all variants.", error);
    }
  });
  revealTarget();
  window.addEventListener("hashchange", revealTarget);
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", initialize, { once: true });
} else {
  initialize();
}
