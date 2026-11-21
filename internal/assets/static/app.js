// Progressive enhancement. Every page already works as plain HTML: links, forms and
// redirects. This script only makes a click or a submit swap the new page in without a
// full reload, and shows times in the reader's time zone. If anything here fails, the
// browser falls back to the ordinary navigation, so the app keeps working.
(() => {
  "use strict";

  let inFlight = null;
  // The link check that is running, if any: { controller, done, broken }.
  let checkRun = null;
  // The inline form that is open, if any: { node, opener }.
  let inlinePanel = null;

  const sameOrigin = (url) => url.origin === location.origin;

  // Files and the JSON API are not pages.
  const isPagePath = (url) =>
    sameOrigin(url) && !url.pathname.startsWith("/api/") && !/^\/app\.(js|css)$/.test(url.pathname);

  const wantsBrowserDefault = (event) =>
    event.defaultPrevented || event.button > 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey;

  function localizeTimes(root) {
    for (const time of root.querySelectorAll("time[datetime]")) {
      const when = new Date(time.dateTime);
      if (!Number.isNaN(when.getTime())) time.title = when.toLocaleString();
    }
  }

  function updateSelectionCount() {
    const label = document.getElementById("bulk-count");
    if (!label) return;
    label.textContent = `${document.querySelectorAll(".pick-box input:checked").length} selected`;
  }

  // The new page's first `autofocus` field, else the page itself, so a screen reader
  // starts reading at the new content.
  function focusNewPage() {
    const target = document.querySelector("main [autofocus]") || document.getElementById("main");
    if (target) target.focus({ preventScroll: true });
  }

  // Replaces the page's content with the one in `doc`. The signed-in frame keeps its
  // menu toggle and top bar, so only the sidebar and `main` change.
  function swapIn(doc) {
    inlinePanel = null;
    document.title = doc.title;
    const framed = document.querySelector(".app") && doc.querySelector(".app");
    if (framed) {
      document.querySelector("main").replaceWith(document.importNode(doc.querySelector("main"), true));
      document.querySelector(".side").replaceWith(document.importNode(doc.querySelector(".side"), true));
      const toggle = document.getElementById("menu-toggle");
      if (toggle) toggle.checked = false;
    } else {
      const nodes = Array.from(doc.body.childNodes, (node) => document.importNode(node, true));
      document.body.replaceChildren(...nodes);
    }
    localizeTimes(document);
    updateSelectionCount();
    focusNewPage();
  }

  function abortCheck() {
    if (!checkRun) return;
    const run = checkRun;
    checkRun = null;
    run.controller.abort();
  }

  // `history`: "push" for a new page, "replace" for the same address, "none" for back/forward.
  // Answers whether the page was swapped in (false when a newer load took over).
  // `keepInline(doc)` gets an answer that is not a redirect and answers true when it
  // showed that answer itself, so the page is not swapped.
  async function load(url, init, history, keepInline) {
    abortCheck();
    if (inFlight) inFlight.abort();
    const controller = new AbortController();
    inFlight = controller;
    const response = await fetch(url, { ...init, signal: controller.signal, credentials: "same-origin" });
    const type = response.headers.get("content-type") || "";
    if (!type.startsWith("text/html")) throw new Error("not a page");
    const doc = new DOMParser().parseFromString(await response.text(), "text/html");
    if (inFlight !== controller) return false;
    inFlight = null;
    if (keepInline && !response.redirected && keepInline(doc)) return true;
    const finalUrl = new URL(response.url);
    if (history === "push") {
      // A POST answered without a redirect has no address of its own to come back to.
      const isNewAddress = !init || init.method !== "POST" || response.redirected;
      if (isNewAddress) window.history.pushState(null, "", finalUrl);
    }
    swapIn(doc);
    if (history !== "none") window.scrollTo(0, 0);
    return true;
  }

  async function enhance(action, fallback) {
    try {
      return await action();
    } catch (error) {
      if (error && error.name === "AbortError") return false;
      fallback(error);
      return false;
    }
  }

  // ---- Link check: live progress over the JSON API ----

  const CHECK_URL = "/api/bookmarks/check";
  const CHECK_ATTEMPTS = 3;
  const CHECK_RETRY_MS = 1000;
  const SVG_NS = "http://www.w3.org/2000/svg";
  const LINK_GLYPHS = {
    ok: "M3 8.5l3.2 3.2L13 4.5",
    redirected: "M2.5 8h10.5M9 4l4 4-4 4",
    broken: "M4 4l8 8M12 4l-8 8",
    unknown: "M5.6 6a2.4 2.4 0 1 1 3.6 2.1c-.8.5-1.2 1-1.2 1.9M8 12.6v.2",
  };
  const LINK_LABELS = {
    ok: "Link OK",
    redirected: "Link moved",
    broken: "Broken link",
    unknown: "Link not confirmed",
  };
  const CHECK_REFUSALS = {
    404: "Some of those bookmarks no longer exist.",
    429: "The last link check is still finishing. Try again in a moment.",
  };

  // A failure whose message is fit to show to the reader.
  class CheckFailed extends Error {}

  const wait = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

  // A check that Stop just ended may still hold the user's one slot for a moment, so a
  // 429 is tried again a few times before it is shown.
  async function openCheck(ids, signal) {
    const request = {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ids }),
      credentials: "same-origin",
      signal,
    };
    for (let attempt = 1; ; attempt++) {
      const response = await fetch(CHECK_URL, request);
      if (response.status !== 429 || attempt === CHECK_ATTEMPTS) return response;
      await wait(CHECK_RETRY_MS);
    }
  }

  function parseServerEvent(block) {
    const event = { name: "message", data: "" };
    for (const line of block.split(/\r?\n/)) {
      if (line.startsWith("event:")) event.name = line.slice(6).trim();
      if (line.startsWith("data:")) event.data += line.slice(5).trim();
    }
    return event;
  }

  async function* serverEvents(response) {
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    for (;;) {
      const { done, value } = await reader.read();
      if (done) return;
      buffer += decoder.decode(value, { stream: true });
      const blocks = buffer.split(/\r?\n\r?\n/);
      buffer = blocks.pop();
      for (const block of blocks) yield parseServerEvent(block);
    }
  }

  function linkGlyph(status) {
    const svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("viewBox", "0 0 16 16");
    svg.setAttribute("aria-hidden", "true");
    const path = document.createElementNS(SVG_NS, "path");
    path.setAttribute("d", LINK_GLYPHS[status]);
    svg.append(path);
    return svg;
  }

  // The words after the bold label; the server renders the same sentence.
  function linkNoteText(link) {
    if (link.status === "ok") return ", checked just now";
    if (link.status === "redirected") return `, now at ${link.detail}, checked just now`;
    if (!link.detail) return ", checked just now";
    return `: ${link.detail}, checked just now`;
  }

  function linkNote(link) {
    const checkedAt = new Date(link.checked_at * 1000).toLocaleString();
    const label = document.createElement("b");
    label.textContent = LINK_LABELS[link.status];
    const words = document.createElement("span");
    words.append(label, linkNoteText(link));
    const note = document.createElement("p");
    note.className = `link-note ${link.status}`;
    note.title = `Checked ${checkedAt}`;
    note.append(linkGlyph(link.status), words);
    return note;
  }

  function showLinkResult(id, link) {
    const row = document.querySelector(`li[data-id="${id}"]`);
    if (!row || !LINK_LABELS[link.status]) return;
    const note = linkNote(link);
    const old = row.querySelector(".link-note");
    if (old) return old.replaceWith(note);
    row.querySelector(".body").append(note);
  }

  function showCheckProgress(form, total, onStop) {
    const box = document.createElement("p");
    box.className = "check-progress";
    box.setAttribute("role", "status");
    const text = document.createElement("span");
    const stop = document.createElement("button");
    stop.type = "button";
    stop.className = "btn";
    stop.textContent = "Stop";
    stop.addEventListener("click", onStop);
    box.append(text, " ", stop);
    form.querySelector(".bulk").append(box);
    const update = (done) => (text.textContent = `Checking links: ${done} of ${total}`);
    update(0);
    return { update, remove: () => box.remove() };
  }

  const SKIP_REASONS = {
    slow_down: "asked us to slow down",
    not_answering: "is not answering",
  };

  function takeSkipped(run, event, progress) {
    const { reason, host } = JSON.parse(event.data);
    run.skipped += 1;
    run.skips.set(host, SKIP_REASONS[reason] || "was left alone");
    progress.update(run.done + run.skipped);
  }

  function takeResult(run, event, progress) {
    const { id, link } = JSON.parse(event.data);
    run.done += 1;
    if (link.status === "broken") run.broken += 1;
    showLinkResult(id, link);
    progress.update(run.done);
  }

  async function streamCheck(run, ids, progress) {
    const response = await openCheck(ids, run.controller.signal);
    if (response.status === 401) {
      checkRun = null;
      location.reload();
      return;
    }
    if (!response.ok) throw new CheckFailed(CHECK_REFUSALS[response.status] || "The link check could not run.");
    for await (const event of serverEvents(response)) {
      if (event.name === "done") return;
      if (event.name === "error") throw new CheckFailed("The link check stopped early.");
      if (event.name === "result") takeResult(run, event, progress);
      if (event.name === "skipped") takeSkipped(run, event, progress);
    }
    throw new CheckFailed("The link check was interrupted.");
  }

  function checkSummary(run, total) {
    const count = run.done === total ? `${total}` : `${run.done} of ${total}`;
    const broken = run.broken > 0 ? `: ${run.broken} broken` : "";
    const summary = `Checked ${count} bookmark${total === 1 ? "" : "s"}${broken}`;
    if (run.skipped === 0) return summary;
    const sites = Array.from(run.skips, ([host, why]) => `${host} ${why}`).join("; ");
    return `${summary}. ${sites}, so some links were left unchecked: try again later.`;
  }

  function showNotice(text) {
    const main = document.getElementById("main");
    if (!main) return;
    const notice = document.createElement("p");
    notice.className = "notice";
    notice.setAttribute("role", "status");
    notice.textContent = text;
    const old = main.querySelector(".notice");
    if (old) old.remove();
    main.prepend(notice);
  }

  function tickAgain(ids) {
    for (const id of ids) {
      const box = document.querySelector(`.pick-box input[name="id-${id}"]`);
      if (box) box.checked = true;
    }
    updateSelectionCount();
  }

  // Shows the server's own state of the page once the check ends, however it ended.
  async function showCheckEnd(ids, message) {
    const swapped = await enhance(
      () => load(location.href, undefined, "none"),
      () => location.reload(),
    );
    if (!swapped) return;
    tickAgain(ids);
    showNotice(message);
  }

  async function runLinkCheck(form, ids) {
    const run = {
      controller: new AbortController(),
      done: 0,
      broken: 0,
      skipped: 0,
      skips: new Map(),
    };
    checkRun = run;
    const progress = showCheckProgress(form, ids.length, () => run.controller.abort());
    let failure = null;
    try {
      await streamCheck(run, ids, progress);
    } catch (error) {
      if (!error || error.name !== "AbortError") {
        failure = error instanceof CheckFailed ? error.message : "The link check was interrupted.";
      }
    }
    // Another page took over while the check ran: it is not this page's to finish.
    if (checkRun !== run) return;
    checkRun = null;
    progress.remove();
    await showCheckEnd(ids, failure || checkSummary(run, ids.length));
  }

  // ---- Inline forms: edit and delete open in their row, a bulk delete at the foot of the bulk bar ----

  const ROW_ACTION = /^\/bookmarks\/\d+\/(edit|delete)$/;
  // The form of an edit page or of a delete question, whichever the page has.
  const INLINE_FORM = "main form.edit-form, main form:has(.danger-button)";

  function closeInlinePanel({ refocus }) {
    if (!inlinePanel) return;
    const { node, opener } = inlinePanel;
    inlinePanel = null;
    const item = node.closest(".item");
    if (item) item.classList.remove("editing", "confirming");
    node.remove();
    if (refocus && opener.isConnected) opener.focus();
  }

  function focusPanel(node) {
    const target = node.querySelector("[autofocus]") || node.querySelector("input, textarea, button");
    if (target) target.focus();
  }

  // The panel that holds the form of a full page: its heading, summary and form.
  // Answers null when the page has no such form (a refusal, an error page).
  function panelFrom(doc) {
    const form = doc.querySelector(INLINE_FORM);
    if (!form) return null;
    const node = document.createElement("div");
    node.className = "inline-form";
    const title = document.createElement("p");
    title.className = "inline-title";
    title.textContent = doc.querySelector("main h1").textContent;
    node.append(title);
    const summary = doc.querySelector("main .summary");
    if (summary) node.append(document.importNode(summary, true));
    node.append(document.importNode(form, true));
    return node;
  }

  function showPanel(node, opener, place) {
    closeInlinePanel({ refocus: false });
    place(node);
    inlinePanel = { node, opener };
    focusPanel(node);
  }

  // Counts the row panels asked for, so only the newest answer is shown.
  let rowPanelRequest = 0;

  async function openRowPanel(link, url) {
    // A form cannot sit inside the select-mode form, so those rows keep the page jump.
    if (link.closest("form")) throw new Error("inside a form");
    const request = ++rowPanelRequest;
    const response = await fetch(url, { credentials: "same-origin" });
    const type = response.headers.get("content-type") || "";
    if (!response.ok || !type.startsWith("text/html")) throw new Error("not a form");
    const node = panelFrom(new DOMParser().parseFromString(await response.text(), "text/html"));
    if (!node) throw new Error("not a form");
    const item = link.closest(".item");
    if (request !== rowPanelRequest || !item.isConnected) return false;
    const state = url.pathname.endsWith("/edit") ? "editing" : "confirming";
    // The row already says what is edited, and the question already names what is deleted.
    node.querySelector(state === "editing" ? ".inline-title" : ".summary").remove();
    showPanel(node, link, (panel) => {
      item.classList.add(state);
      item.append(panel);
    });
    return true;
  }

  // A refused edit shows its reason in the open panel, with what was typed kept.
  function keepRefusalInPanel(panel) {
    return (doc) => {
      const node = panelFrom(doc);
      if (!node) return false;
      panel.replaceWith(node);
      inlinePanel = { node, opener: inlinePanel ? inlinePanel.opener : node };
      focusPanel(node);
      return true;
    };
  }

  function submitInline({ form, url, data }) {
    const panel = form.closest(".inline-form");
    const scrolled = window.scrollY;
    // The row the user worked on is likely far down the list, so the page stays where it is.
    const loadKeepingScroll = async () => {
      const swapped = await load(url, { method: "POST", body: new URLSearchParams(data) }, "push", keepRefusalInPanel(panel));
      if (swapped) window.scrollTo(0, scrolled);
      return swapped;
    };
    return enhance(
      loadKeepingScroll,
      (error) => (error instanceof TypeError ? HTMLFormElement.prototype.submit.call(form) : location.reload()),
    );
  }

  const isBulkDelete = (form, data) => form.classList.contains("bulk-form") && data.get("action") === "delete";

  // The server answers a bulk delete with a question page; it goes at the foot of the bulk bar.
  function askBulkDelete({ form, submitter, url, data }) {
    const showQuestion = (doc) => {
      const node = panelFrom(doc);
      if (!node) return false;
      showPanel(node, submitter, (panel) => form.querySelector(".bulk").append(panel));
      return true;
    };
    return enhance(
      () => load(url, { method: "POST", body: new URLSearchParams(data) }, "push", showQuestion),
      () => submitNatively(form, submitter),
    );
  }

  // The question holds the ticks it was asked with, so a change to the ticks withdraws it.
  document.addEventListener("change", (event) => {
    const changed = event.target instanceof Element && event.target.closest(".pick-box");
    if (changed && inlinePanel && !inlinePanel.node.closest(".item")) closeInlinePanel({ refocus: false });
  });

  // ---- Forms ----

  function setBusy(form, submitter, busy) {
    for (const node of [form, document.getElementById("main")]) {
      if (!node) continue;
      if (busy) node.setAttribute("aria-busy", "true");
      else node.removeAttribute("aria-busy");
    }
    if (submitter) submitter.disabled = busy;
  }

  const tickedIds = (data) =>
    Array.from(data.keys())
      .filter((name) => /^id-\d+$/.test(name))
      .map((name) => Number(name.slice(3)));

  // The server may have handled a POST whose answer was not a page: submitting it
  // again could repeat it, so only reload. A request that never got through is
  // sent the ordinary way, with the pressed button's name and value kept.
  // A native submit() leaves out the submitter's name and value, so they go in a hidden field.
  function submitNatively(form, submitter) {
    if (submitter && submitter.name) {
      const hidden = document.createElement("input");
      hidden.type = "hidden";
      hidden.name = submitter.name;
      hidden.value = submitter.value;
      form.append(hidden);
    }
    HTMLFormElement.prototype.submit.call(form);
  }

  function postForm({ form, submitter, url, data }) {
    const body = form.enctype === "multipart/form-data" ? data : new URLSearchParams(data);
    return enhance(
      () => load(url, { method: "POST", body }, "push"),
      (error) => (error instanceof TypeError ? submitNatively(form, submitter) : location.reload()),
    );
  }

  // Shift-click ticks or clears every row between the last box clicked and this one.
  let lastTicked = null;
  document.addEventListener("click", (event) => {
    const box = event.target instanceof Element ? event.target.closest(".pick-box input") : null;
    if (!box) return;
    const boxes = Array.from(document.querySelectorAll(".pick-box input"));
    if (event.shiftKey && lastTicked && boxes.includes(lastTicked)) {
      const [from, to] = [boxes.indexOf(lastTicked), boxes.indexOf(box)].sort((a, b) => a - b);
      for (const between of boxes.slice(from, to + 1)) between.checked = box.checked;
    }
    lastTicked = box;
    updateSelectionCount();
  });

  document.addEventListener("click", (event) => {
    if (wantsBrowserDefault(event)) return;
    const link = event.target instanceof Element ? event.target.closest("a[href]") : null;
    if (!link) return;
    if (link.closest(".inline-form") && link.classList.contains("quiet-link")) {
      event.preventDefault();
      closeInlinePanel({ refocus: true });
      return;
    }
    const url = new URL(link.href);
    if (!link.closest(".item .actions") || !sameOrigin(url) || !ROW_ACTION.test(url.pathname)) return;
    event.preventDefault();
    enhance(
      () => openRowPanel(link, url),
      () => location.assign(url),
    );
  });

  document.addEventListener("click", (event) => {
    if (wantsBrowserDefault(event)) return;
    const link = event.target instanceof Element ? event.target.closest("a[href]") : null;
    if (!link || link.target || link.hasAttribute("download")) return;
    const url = new URL(link.href);
    if (!isPagePath(url)) return;
    event.preventDefault();
    enhance(
      () => load(url, undefined, "push"),
      () => location.assign(url),
    );
  });

  function finished(form, submitter, ids, url, data) {
    if (ids.length > 0) return runLinkCheck(form, ids);
    if (form.closest(".inline-form")) return submitInline({ form, url, data });
    if (isBulkDelete(form, data)) return askBulkDelete({ form, submitter, url, data });
    return postForm({ form, submitter, url, data });
  }

  document.addEventListener("submit", (event) => {
    if (event.defaultPrevented) return;
    const form = event.target;
    const submitter = event.submitter;
    // `formAction` is the page's own address when the button sets none, so ask the attribute.
    const own = (name) => submitter && submitter.hasAttribute(name);
    // `form.action` and `form.method` name a field instead when one is called that, as the
    // bulk form's `action` buttons are, so read the attributes.
    const url = new URL(own("formaction") ? submitter.formAction : form.getAttribute("action") || "", location.href);
    if (!isPagePath(url)) return;
    event.preventDefault();
    const data = new FormData(form, submitter);
    const method = own("formmethod") ? submitter.formMethod : form.getAttribute("method") || "get";
    if (method.toLowerCase() === "get") {
      url.search = new URLSearchParams(data).toString();
      enhance(
        () => load(url, undefined, "push"),
        () => location.assign(url),
      );
      return;
    }
    // A second Enter or click while the first request runs would repeat the action.
    if (form.getAttribute("aria-busy") === "true") return;
    const ids = data.get("action") === "check" ? tickedIds(data) : [];
    setBusy(form, submitter, true);
    finished(form, submitter, ids, url, data).finally(() => setBusy(form, submitter, false));
  });

  // Escape in a text field belongs to the field (clearing a search, closing suggestions);
  // leaving select mode from there would throw the ticks away.
  const isTextEntry = (target) =>
    target instanceof Element &&
    (target.matches("input:not([type=checkbox]), textarea, select") || target.isContentEditable);

  // Escape closes the menu on a narrow screen; in select mode it leaves select mode.
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !event.defaultPrevented && inlinePanel && inlinePanel.node.contains(event.target)) {
      closeInlinePanel({ refocus: true });
      return;
    }
    if (event.key !== "Escape" || event.defaultPrevented || isTextEntry(event.target)) return;
    const toggle = document.getElementById("menu-toggle");
    if (toggle && toggle.checked) {
      toggle.checked = false;
      return;
    }
    const done = document.querySelector(".bar a.on[href]");
    if (done) done.click();
  });

  window.addEventListener("popstate", () => {
    enhance(
      () => load(location.href, undefined, "none"),
      () => location.reload(),
    );
  });

  localizeTimes(document);
  updateSelectionCount();
})();
