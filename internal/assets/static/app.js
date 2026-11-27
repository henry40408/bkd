// Progressive enhancement. Every page already works as plain HTML: links, forms and
// redirects. This script only makes a click or a submit swap the new page in without a
// full reload, and shows times in the reader's time zone. If anything here fails, the
// browser falls back to the ordinary navigation, so the app keeps working.
(() => {
  "use strict";

  let inFlight = null;
  // The link check or page read that is running, if any: { stream, controller, done, ... }.
  let checkRun = null;
  // The inline form that is open, if any: { node, opener, restore }. `restore` puts the
  // page back where it was when a row or the pick opened it; the bulk question has none.
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

  // The server hides what only works with this script.
  function showScriptOnly(root) {
    for (const part of root.querySelectorAll("[data-script-only]")) part.hidden = false;
    syncSubmitStates(root);
  }

  // A form marked `data-submit-when-valid` keeps its submit button off until the browser would accept it.
  function syncSubmitState(form) {
    const button = form.querySelector('button[type="submit"]');
    if (button) button.disabled = !form.checkValidity();
  }

  function syncSubmitStates(root) {
    for (const form of root.querySelectorAll("form[data-submit-when-valid]")) syncSubmitState(form);
  }

  document.addEventListener("input", (event) => {
    const form = event.target instanceof Element && event.target.closest("form[data-submit-when-valid]");
    if (form) syncSubmitState(form);
  });

  function updateSelectionCount() {
    const label = document.getElementById("bulk-count");
    if (!label) return;
    const ticked = document.querySelectorAll(".pick-box input:checked").length;
    label.textContent = `${ticked} selected`;
    // Without the script the buttons stay enabled and the server answers "nothing selected".
    for (const button of document.querySelectorAll('.bulk .line button[name="action"]')) button.disabled = ticked === 0;
  }

  // The new page's first `autofocus` field, else the page itself, so a screen reader
  // starts reading at the new content.
  function focusNewPage() {
    const target = document.querySelector("main [autofocus]") || document.getElementById("main");
    if (target) target.focus({ preventScroll: true });
  }

  // Replaces the page's content with the one in `doc`. The signed-in frame keeps its
  // menu toggle and top bar, so only the sidebar and `main` change. `refocus` false
  // leaves the focus alone, for a swap the reader did not move away from.
  function swapIn(doc, refocus) {
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
    showScriptOnly(document);
    addSearchHint();
    updateSelectionCount();
    if (refocus) focusNewPage();
  }

  function abortCheck() {
    if (!checkRun) return;
    const run = checkRun;
    checkRun = null;
    run.controller.abort();
  }

  // ---- The reader's place ----

  // The rows that begin on screen, below the sticky top bar of a narrow screen. The
  // ticked ones come first: they are what the reader is working on.
  function rowsInView() {
    const topEdge = document.querySelector(".topbar")?.getBoundingClientRect().bottom || 0;
    const shown = Array.from(document.querySelectorAll(".item")).filter((row) => {
      const { top } = row.getBoundingClientRect();
      return top >= topEdge && top < window.innerHeight;
    });
    return [...shown.filter((row) => row.querySelector(".pick-box input:checked")), ...shown];
  }

  // Swaps and panels move the content about. Answers a function that puts the first of
  // `rows` that is still in the page back where it is on screen now. With none left, or at
  // the top of the page, the scroll position is put back instead.
  function holdPlace(rows = rowsInView()) {
    const scrolled = window.scrollY;
    const marks = rows.filter((row) => row?.id).map((row) => ({ id: row.id, top: row.getBoundingClientRect().top }));
    return () => {
      const mark = scrolled > 0 && marks.find(({ id }) => document.getElementById(id));
      const by = mark
        ? document.getElementById(mark.id).getBoundingClientRect().top - mark.top
        : scrolled - window.scrollY;
      // Scrolling by nothing would still stop a swipe that is under way.
      if (Math.abs(by) >= 1) window.scrollBy(0, by);
    };
  }

  // Makes a change to the page without moving what the reader is looking at.
  function keepingPlace(change) {
    const restore = holdPlace();
    change();
    restore();
  }

  // ---- The reader's place in the history ----

  // The browser would scroll on back and forward before the page is fetched, cut short by
  // whatever page is still on screen. So the script scrolls instead, also when an entry is
  // loaded afresh (a reload, or two steps back past another page): each entry gets a name,
  // and the script remembers where the reader was when leaving it.
  const PLACES_KEY = "bkd-places";

  // Kept over a reload. Without storage, only what is left from now on is remembered.
  function storedPlaces() {
    try {
      return new Map(JSON.parse(sessionStorage.getItem(PLACES_KEY)));
    } catch {
      return new Map();
    }
  }

  const places = storedPlaces();

  // The name of the current history entry, which gets one if it has none.
  function currentEntry() {
    if (!window.history.state?.entry) {
      window.history.replaceState({ entry: Math.random().toString(36).slice(2) }, "");
    }
    return window.history.state.entry;
  }

  window.history.scrollRestoration = "manual";
  // The entry whose page is on screen: while back or forward is loading, not the current one.
  let shownEntry = currentEntry();

  const rememberPlace = () => places.set(shownEntry, window.scrollY);
  const scrollToPlace = () => window.scrollTo(0, places.get(shownEntry) ?? 0);

  function savePlaces() {
    rememberPlace();
    try {
      sessionStorage.setItem(PLACES_KEY, JSON.stringify([...places]));
    } catch {
      // No storage, or no room in it: a reload starts at the top.
    }
  }

  // A phone may drop a hidden page without another event.
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") savePlaces();
  });
  window.addEventListener("pagehide", savePlaces);

  // `history`: "push" for a new page, "replace" for the same page made longer (the scroll
  // position and the focus stay), "none" for back/forward.
  // Answers whether the page was swapped in (false when a newer load took over).
  // `keepInline(doc)` gets an answer that is not a redirect and answers true when it
  // showed that answer itself, so the page is not swapped.
  // `hold()` is called just before the swap; what it answers puts the reader's place back.
  // Without it a new page starts at the top.
  async function load(url, init, history, { keepInline, hold } = {}) {
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
    if (history === "replace") window.history.replaceState(window.history.state, "", finalUrl);
    if (history === "push") {
      // A POST answered without a redirect has no address of its own to come back to.
      const isNewAddress = !init || init.method !== "POST" || response.redirected;
      if (isNewAddress) {
        rememberPlace();
        window.history.pushState(null, "", finalUrl);
      }
    }
    const restore = hold?.();
    swapIn(doc, history !== "replace");
    shownEntry = currentEntry();
    if (history === "push") window.scrollTo(0, 0);
    restore?.();
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

  // ---- Link check and page read: live progress over the JSON API ----

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

  // A failure whose message is fit to show to the reader.
  class CheckFailed extends Error {}

  const wait = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

  // A check that Stop just ended may still hold the user's one slot for a moment, so a
  // 429 is tried again a few times before it is shown.
  async function openCheck(stream, ids, signal) {
    const request = {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ids }),
      credentials: "same-origin",
      signal,
    };
    for (let attempt = 1; ; attempt++) {
      const response = await fetch(stream.url, request);
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
    // Not every browser anchors the scroll position when a row above the reader grows.
    keepingPlace(() => (old ? old.replaceWith(note) : row.querySelector(".body").append(note)));
  }

  function showCheckProgress(form, { label, total }, onStop) {
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
    const update = (done) => (text.textContent = `${label}: ${done} of ${total}`);
    update(0);
    return { update, remove: () => box.remove() };
  }

  const SKIP_REASONS = {
    slow_down: "asked us to slow down",
    not_answering: "is not answering",
  };

  // How many targets the server has dealt with, however it went.
  const handled = (run) => run.done + run.failed + run.skipped;

  function takeSkipped(run, event, progress) {
    const { reason, host } = JSON.parse(event.data);
    run.skipped += 1;
    run.skips.set(host, SKIP_REASONS[reason] || "was left alone");
    progress.update(handled(run));
  }

  function takeLinkResult(run, event, progress) {
    const { id, link } = JSON.parse(event.data);
    run.done += 1;
    if (link.status === "broken") run.broken += 1;
    showLinkResult(id, link);
    progress.update(handled(run));
  }

  function takePageRead(run, event, progress) {
    const { note_filled: noteFilled } = JSON.parse(event.data);
    run.done += 1;
    if (noteFilled) run.filled += 1;
    progress.update(handled(run));
  }

  function takePageFailed(run, _event, progress) {
    run.failed += 1;
    progress.update(handled(run));
  }

  const plural = (count, word) => `${count} ${word}${count === 1 ? "" : "s"}`;

  // Names the sites that were left alone, after `summary`.
  function withSkips(run, summary, leftOut) {
    if (run.skipped === 0) return summary;
    const sites = Array.from(run.skips, ([host, why]) => `${host} ${why}`).join("; ");
    return `${summary}. ${sites}, so some ${leftOut}: try again later.`;
  }

  function checkSummary(run, total) {
    const count = run.done === total ? `${total}` : `${run.done} of ${total}`;
    const broken = run.broken > 0 ? `: ${run.broken} broken` : "";
    return withSkips(run, `Checked ${count} bookmark${total === 1 ? "" : "s"}${broken}`, "links were left unchecked");
  }

  function readSummary(run, total) {
    const parts = [`Read ${run.done} of ${plural(total, "page")}`];
    if (run.filled > 0) parts.push(`${plural(run.filled, "note")} filled`);
    if (run.failed > 0) parts.push(`${run.failed} could not be read`);
    return withSkips(run, parts.join(", "), "pages were left unread");
  }

  // What differs between the two long-running actions of the bulk bar.
  const STREAMS = new Map([
    [
      "check",
      {
        url: "/api/bookmarks/check",
        label: "Checking links",
        takers: { result: takeLinkResult },
        summary: checkSummary,
        refusals: {
          404: "Some of those bookmarks no longer exist.",
          429: "The last link check is still finishing. Try again in a moment.",
        },
        couldNotRun: "The link check could not run.",
        stoppedEarly: "The link check stopped early.",
        interrupted: "The link check was interrupted.",
      },
    ],
    [
      "read",
      {
        url: "/api/bookmarks/read",
        label: "Reading pages",
        takers: { result: takePageRead, failed: takePageFailed },
        summary: readSummary,
        refusals: {
          404: "Some of those bookmarks no longer exist.",
          429: "A link check or another read is still finishing. Try again in a moment.",
        },
        couldNotRun: "The pages could not be read.",
        stoppedEarly: "Reading the pages stopped early.",
        interrupted: "Reading the pages was interrupted.",
      },
    ],
  ]);

  async function streamCheck(run, ids, progress) {
    const { stream } = run;
    const response = await openCheck(stream, ids, run.controller.signal);
    if (response.status === 401) {
      checkRun = null;
      location.reload();
      return;
    }
    if (!response.ok) throw new CheckFailed(stream.refusals[response.status] || stream.couldNotRun);
    for await (const event of serverEvents(response)) {
      if (event.name === "done") return;
      if (event.name === "error") throw new CheckFailed(stream.stoppedEarly);
      if (event.name === "skipped") takeSkipped(run, event, progress);
      else if (stream.takers[event.name]) stream.takers[event.name](run, event, progress);
    }
    throw new CheckFailed(stream.interrupted);
  }

  function showNotice(text) {
    const main = document.getElementById("main");
    if (!main) return;
    const notice = document.createElement("p");
    notice.className = "notice";
    notice.setAttribute("role", "status");
    notice.textContent = text;
    keepingPlace(() => {
      main.querySelector(".notice")?.remove();
      main.prepend(notice);
    });
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
      () => load(location.href, undefined, "none", { hold: holdPlace }),
      () => location.reload(),
    );
    if (!swapped) return;
    tickAgain(ids);
    showNotice(message);
  }

  async function runStream(form, ids, stream) {
    const run = {
      stream,
      controller: new AbortController(),
      done: 0,
      broken: 0,
      filled: 0,
      failed: 0,
      skipped: 0,
      skips: new Map(),
    };
    checkRun = run;
    const progress = showCheckProgress(form, { label: stream.label, total: ids.length }, () => run.controller.abort());
    let failure = null;
    try {
      await streamCheck(run, ids, progress);
    } catch (error) {
      if (!error || error.name !== "AbortError") {
        failure = error instanceof CheckFailed ? error.message : stream.interrupted;
      }
    }
    // Another page took over while the run went on: it is not this page's to finish.
    if (checkRun !== run) return;
    checkRun = null;
    progress.remove();
    await showCheckEnd(ids, failure || stream.summary(run, ids.length));
  }

  // ---- Inline forms: edit and delete open in their row, a bulk delete at the foot of the bulk bar ----

  const ROW_ACTION = /^\/bookmarks\/\d+\/(edit|delete)$/;
  // A row of the list or the random pick holds the panel; its Edit and Delete links open it.
  const PANEL_HOST = ".item, section.pick";
  const PANEL_OPENER = ".item .actions, section.pick .btns";
  // The form of an edit page or of a delete question, whichever the page has.
  const INLINE_FORM = "main form.edit-form, main form:has(.danger-button)";

  function closeInlinePanel({ refocus }) {
    if (!inlinePanel) return;
    const { node, opener, restore } = inlinePanel;
    inlinePanel = null;
    const host = node.closest(PANEL_HOST);
    if (host) host.classList.remove("editing", "confirming");
    node.remove();
    if (!refocus) return;
    if (opener.isConnected) opener.focus({ preventScroll: true });
    restore?.();
  }

  // On a touch screen a focused field brings up the keyboard, which moves the page, so
  // there the panel takes the focus instead of a field.
  function focusPanel(node) {
    let target = node.querySelector("[autofocus]") || node.querySelector("input, textarea, button");
    if (matchMedia("(pointer: coarse)").matches && isTextEntry(target)) target = node;
    target?.focus({ preventScroll: true });
    // Scrolls only as far as it takes to show the panel.
    node.scrollIntoView({ block: "nearest" });
  }

  // The panel that holds the form of a full page: its heading, summary and form.
  // Answers null when the page has no such form (a refusal, an error page).
  function panelFrom(doc) {
    const form = doc.querySelector(INLINE_FORM);
    if (!form) return null;
    const node = document.createElement("div");
    node.className = "inline-form";
    node.tabIndex = -1;
    const title = document.createElement("p");
    title.className = "inline-title";
    title.textContent = doc.querySelector("main h1").textContent;
    node.append(title);
    for (const summary of doc.querySelectorAll("main .summary")) node.append(document.importNode(summary, true));
    node.append(document.importNode(form, true));
    showScriptOnly(node);
    return node;
  }

  function showPanel(panel, attach) {
    closeInlinePanel({ refocus: false });
    attach(panel.node);
    panel.restore?.();
    inlinePanel = panel;
    focusPanel(panel.node);
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
    const host = link.closest(PANEL_HOST);
    if (request !== rowPanelRequest || !host.isConnected) return false;
    const state = url.pathname.endsWith("/edit") ? "editing" : "confirming";
    // The row already says what is edited, and the question already names what is deleted.
    node.querySelector(state === "editing" ? ".inline-title" : ".summary").remove();
    // The row stays where it is; once it is deleted, the one before it does, so the next
    // row moves into its place. The pick is no row: it only keeps the scroll position.
    const row = link.closest(".item");
    const restore = holdPlace(row ? [row, row.previousElementSibling, ...rowsInView()] : []);
    showPanel({ node, opener: link, restore }, (panel) => {
      host.classList.add(state);
      host.append(panel);
    });
    return true;
  }

  // A refused edit shows its reason in the open panel, with what was typed kept.
  function keepRefusalInPanel(panel) {
    return (doc) => {
      const node = panelFrom(doc);
      if (!node) return false;
      panel.replaceWith(node);
      inlinePanel = inlinePanel ? { ...inlinePanel, node } : { node, opener: node };
      focusPanel(node);
      node.querySelector(".err")?.scrollIntoView({ block: "nearest" });
      return true;
    };
  }

  function submitInline({ form, url, data }) {
    const panel = form.closest(".inline-form");
    // The row the user worked on is likely far down the list, so the page stays where it
    // was. The bulk question's bar sticks while the reader scrolls on, so it has no place
    // of its own to go back to.
    const opened = inlinePanel?.restore;
    const options = { keepInline: keepRefusalInPanel(panel), hold: () => opened || holdPlace() };
    return enhance(
      () => load(url, { method: "POST", body: new URLSearchParams(data) }, "push", options),
      (error) => (error instanceof TypeError ? HTMLFormElement.prototype.submit.call(form) : location.reload()),
    );
  }

  const isBulkDelete = (form, data) => form.classList.contains("bulk-form") && data.get("action") === "delete";

  // The server answers a bulk delete with a question page; it goes at the foot of the bulk bar.
  function askBulkDelete({ form, submitter, url, data }) {
    const showQuestion = (doc) => {
      const node = panelFrom(doc);
      if (!node) return false;
      showPanel({ node, opener: submitter }, (panel) => form.querySelector(".bulk").append(panel));
      return true;
    };
    const options = { keepInline: showQuestion, hold: holdPlace };
    return enhance(
      () => load(url, { method: "POST", body: new URLSearchParams(data) }, "push", options),
      () => submitNatively(form, submitter),
    );
  }

  // The question holds the ticks it was asked with, so a change to the ticks withdraws it.
  document.addEventListener("change", (event) => {
    const changed = event.target instanceof Element && event.target.closest(".pick-box");
    if (changed && inlinePanel && !inlinePanel.node.closest(PANEL_HOST)) closeInlinePanel({ refocus: false });
  });

  // ---- Forms ----

  function setBusy(form, submitter, busy) {
    for (const node of [form, document.getElementById("main")]) {
      if (!node) continue;
      if (busy) node.setAttribute("aria-busy", "true");
      else node.removeAttribute("aria-busy");
    }
    if (submitter) submitter.disabled = busy;
    // Ending the busy state must not re-enable an action that has nothing ticked.
    if (!busy) {
      updateSelectionCount();
      if (form.matches("[data-submit-when-valid]")) syncSubmitState(form);
    }
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
    // A bulk action leaves the reader on the same list.
    const hold = form.classList.contains("bulk-form") ? holdPlace : undefined;
    return enhance(
      () => load(url, { method: "POST", body }, "push", { hold }),
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

  // ---- Load more: the list grows in place ----

  const tickedRowIds = () =>
    Array.from(document.querySelectorAll(".pick-box input:checked"), (box) => Number(box.name.slice(3)));

  // The row that follows `rowId` once the longer page is in, or null.
  const rowAfter = (rowId) => {
    const row = document.getElementById(rowId);
    return row ? row.nextElementSibling : null;
  };

  function focusNewRow(rowId) {
    const first = rowAfter(rowId);
    const target = first && first.querySelector(".pick-box input, h3 a");
    if (target) target.focus({ preventScroll: true });
  }

  // The page is fetched without the fragment: the swap keeps the scroll position instead.
  async function loadMoreRows(link) {
    const lastShownId = link.hash.slice(1);
    const hadFocus = document.activeElement === link;
    const ticked = tickedRowIds();
    const url = new URL(link.href);
    url.hash = "";
    link.setAttribute("aria-disabled", "true");
    link.textContent = "Loading…";
    const swapped = await load(url, undefined, "replace");
    if (!swapped) return;
    tickAgain(ticked);
    if (hadFocus) focusNewRow(lastShownId);
  }

  document.addEventListener("click", (event) => {
    if (wantsBrowserDefault(event)) return;
    const link = event.target instanceof Element ? event.target.closest("a#load-more") : null;
    if (!link) return;
    event.preventDefault();
    if (link.getAttribute("aria-disabled") === "true") return;
    enhance(
      () => loadMoreRows(link),
      () => location.assign(link.href),
    );
  });

  // The address comes from a remote server's answer, so only a web address is used.
  function isWebAddress(value) {
    try {
      const { protocol } = new URL(value);
      return protocol === "http:" || protocol === "https:";
    } catch {
      return false;
    }
  }

  // ---- Page details: the add and edit forms read what the page behind the address says ----
  // The title and the note show the page's title and description as placeholders; the
  // server saves them for a field left blank. Hidden fields carry them back with the form.

  const SITE_PREVIEW_URL = "/api/site-preview";
  const SITE_TYPING_PAUSE_MS = 500;
  // A form sent while its page is being read waits this long for the answer, at most.
  const SITE_SUBMIT_WAIT_MS = 5000;
  const SITE_READING = "Reading the page…";
  const SITE_NOTHING = "The page says nothing about itself.";
  const SITE_UNREACHABLE = "Could not read the page: bkd did not answer.";
  const SITE_NO_ADDRESS = "There is no web address to read.";
  const SITE_BUSY = "Could not read the page: a link check or another read is running. Try again in a moment.";

  // Per form:
  //   timer   the pause before a read, while the person is still typing
  //   run     the read in flight (a promise that never rejects), or null
  //   failed  { address, message } of a read that will fail again, so it is not repeated
  //   waiting a submit is waiting for the read; resume lets its second try through
  const siteStates = new WeakMap();

  // `retryable` is a failure that is not the page's: asking again may work.
  class SiteReadFailed extends Error {
    constructor(message, retryable) {
      super(message);
      this.retryable = retryable;
    }
  }

  const sentence = (text) => text.charAt(0).toUpperCase() + text.slice(1);

  function siteStateOf(form) {
    if (!siteStates.has(form)) siteStates.set(form, { timer: null, run: null, failed: null, waiting: false, resume: false });
    return siteStates.get(form);
  }

  const siteInput = (form, name) => form.querySelector(`[name="${name}"]`);

  function setSiteStatus(form, message) {
    const line = form.querySelector("[data-site-status]");
    if (!line) return;
    line.textContent = message;
    line.hidden = message === "";
  }

  // The details belong to one address; they are put away as soon as the address changes.
  function setSiteDetails(form, { url, title, description }) {
    siteInput(form, "site_url").value = url;
    siteInput(form, "site_title").value = title;
    siteInput(form, "site_description").value = description;
    const titleField = siteInput(form, "title");
    titleField.placeholder = title || titleField.dataset.defaultPlaceholder;
    const noteField = siteInput(form, "note");
    if (description) noteField.placeholder = description;
    else noteField.removeAttribute("placeholder");
  }

  const NO_SITE_DETAILS = { url: "", title: "", description: "" };

  // The address as typed, when it is one the server can read.
  function readableAddress(form) {
    const field = siteInput(form, "url");
    const address = field.value.trim();
    return field.validity.valid && isWebAddress(address) ? address : "";
  }

  async function fetchSiteDetails(address) {
    let response;
    try {
      response = await fetch(SITE_PREVIEW_URL, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ url: address }),
        credentials: "same-origin",
      });
    } catch {
      throw new SiteReadFailed(SITE_UNREACHABLE, true);
    }
    const body = await response.json().catch(() => ({}));
    if (response.status === 429) throw new SiteReadFailed(SITE_BUSY, true);
    if (!response.ok) {
      throw new SiteReadFailed(typeof body.error === "string" ? sentence(body.error) : SITE_UNREACHABLE, false);
    }
    return { url: address, title: body.title || "", description: body.description || "" };
  }

  // Starts reading the address in the field, unless it is being read, has no details to
  // read or already has them. Returns the read in flight, if any.
  function readSiteDetails(form) {
    const state = siteStateOf(form);
    const address = readableAddress(form);
    if (!form.isConnected || state.run || !address || address === siteInput(form, "site_url").value) return state.run;
    state.run = runSiteRead(form, address);
    return state.run;
  }

  // One request at a time per form. An answer for an address that was typed over is
  // dropped and the address now in the field is read; aborting the request instead
  // could leave the server's one slot for this user taken when the next one arrives.
  async function runSiteRead(form, address) {
    const state = siteStateOf(form);
    setSiteStatus(form, SITE_READING);
    let details = null;
    let failure = null;
    try {
      details = await fetchSiteDetails(address);
    } catch (error) {
      failure = error instanceof SiteReadFailed ? error : new SiteReadFailed(SITE_UNREACHABLE, true);
    }
    state.run = null;
    if (!form.isConnected) return;
    if (readableAddress(form) !== address) {
      setSiteStatus(form, "");
      return readSiteDetails(form);
    }
    if (failure) {
      state.failed = failure.retryable ? null : { address, message: failure.message };
      setSiteStatus(form, failure.message);
      return;
    }
    state.failed = null;
    setSiteDetails(form, details);
    setSiteStatus(form, details.title || details.description ? "" : SITE_NOTHING);
  }

  function addressChanged(form, pauseMs) {
    if (!form.matches("[data-site-preview]")) return;
    const state = siteStateOf(form);
    clearTimeout(state.timer);
    state.timer = null;
    const address = readableAddress(form);
    if (address === siteInput(form, "site_url").value) {
      // Nothing to read or to put away; an error left from an address that is gone goes.
      if (!address && !state.run) setSiteStatus(form, "");
      return;
    }
    setSiteDetails(form, NO_SITE_DETAILS);
    // A running read keeps its status line until it ends and is dropped or shown.
    if (!state.run) setSiteStatus(form, "");
    if (state.failed && state.failed.address === address) {
      // The page will not read any better a second time; say so again, and spare the site.
      if (!state.run) setSiteStatus(form, state.failed.message);
      return;
    }
    state.timer = setTimeout(() => {
      state.timer = null;
      readSiteDetails(form);
    }, pauseMs);
  }

  // Reads the address in the field again, also when its details are already there or
  // the last read failed: the page may have changed since, or the failure may be over.
  // Nothing is saved until the form is sent.
  function rereadSiteDetails(form) {
    const state = siteStateOf(form);
    if (state.run) return;
    clearTimeout(state.timer);
    state.timer = null;
    const address = readableAddress(form);
    if (!address) {
      setSiteStatus(form, SITE_NO_ADDRESS);
      return;
    }
    state.failed = null;
    state.run = runSiteRead(form, address);
  }

  document.addEventListener("click", (event) => {
    const button = event.target instanceof Element ? event.target.closest("button[data-site-reread]") : null;
    if (button && button.form) rereadSiteDetails(button.form);
  });

  const urlFieldOf = (event) =>
    event.target instanceof HTMLInputElement && event.target.name === "url" && event.target.form ? event.target : null;

  document.addEventListener("input", (event) => {
    const field = urlFieldOf(event);
    if (field) addressChanged(field.form, SITE_TYPING_PAUSE_MS);
  });

  document.addEventListener("change", (event) => {
    const field = urlFieldOf(event);
    if (field) addressChanged(field.form, 0);
  });

  // A form sent while its page is still being read would go without the page's text.
  // Returns true when the send was held back; it is tried again once the read ends.
  function holdSubmitForSiteDetails(form, submitter) {
    if (!form.matches("[data-site-preview]")) return false;
    const state = siteStateOf(form);
    if (state.resume) {
      state.resume = false;
      return false;
    }
    if (state.waiting) return true;
    if (state.timer) {
      clearTimeout(state.timer);
      state.timer = null;
      readSiteDetails(form);
    }
    if (!state.run) return false;
    state.waiting = true;
    Promise.race([state.run, wait(SITE_SUBMIT_WAIT_MS)]).then(() => {
      state.waiting = false;
      if (!form.isConnected) return;
      state.resume = true;
      form.requestSubmit(submitter);
    });
    return true;
  }

  // ---- Moved links: the edit form can take the address the link moved to ----

  const MOVED_FILLED = "New address filled in. Save to keep it; the link is checked again later.";

  function useMovedAddress(button) {
    const form = button.closest("form");
    const field = form && form.querySelector('input[name="url"]');
    const target = button.dataset.movedTo;
    if (!field || !isWebAddress(target)) return;
    field.value = target;
    syncSubmitState(form);
    addressChanged(form, 0);
    button.hidden = true;
    const hint = form.querySelector(".hint.moved");
    if (hint) hint.textContent = MOVED_FILLED;
    field.focus();
  }

  document.addEventListener("click", (event) => {
    const button = event.target instanceof Element ? event.target.closest("button[data-moved-to]") : null;
    if (button) useMovedAddress(button);
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
    if (!link.closest(PANEL_OPENER) || !sameOrigin(url) || !ROW_ACTION.test(url.pathname)) return;
    event.preventDefault();
    enhance(
      () => openRowPanel(link, url),
      () => location.assign(url),
    );
  });

  const SELECTION_PARAMS = ["select", "all"];

  // The parameters that say which bookmarks the list holds.
  function listQuery(url) {
    const kept = new URLSearchParams();
    for (const [name, value] of url.searchParams) {
      // The server's own links trim the search text.
      const text = name === "q" ? value.trim() : value;
      if (text !== "" && name !== "notice" && !SELECTION_PARAMS.includes(name)) kept.append(name, text);
    }
    kept.sort();
    return kept.toString();
  }

  // Select, Done, Select all and Clear selection show the list the reader is already on.
  function changesOnlySelection(to) {
    const from = new URL(location.href);
    if (from.pathname !== "/" || to.pathname !== "/" || listQuery(from) !== listQuery(to)) return false;
    return SELECTION_PARAMS.some((name) => from.searchParams.has(name) !== to.searchParams.has(name));
  }

  document.addEventListener("click", (event) => {
    if (wantsBrowserDefault(event)) return;
    const link = event.target instanceof Element ? event.target.closest("a[href]") : null;
    if (!link || link.target || link.hasAttribute("download")) return;
    const url = new URL(link.href);
    if (!isPagePath(url)) return;
    event.preventDefault();
    // Outside `main`, a link to the same list ("All bookmarks") asks for its top.
    const hold = link.closest("main") && changesOnlySelection(url) ? holdPlace : undefined;
    enhance(
      () => load(url, undefined, "push", { hold }),
      () => location.assign(url),
    );
  });

  function finished(form, submitter, ids, url, data) {
    if (ids.length > 0) return runStream(form, ids, STREAMS.get(data.get("action")));
    if (form.closest(".inline-form")) return submitInline({ form, url, data });
    if (isBulkDelete(form, data)) return askBulkDelete({ form, submitter, url, data });
    return postForm({ form, submitter, url, data });
  }

  function takeSubmit(event) {
    if (event.defaultPrevented) return;
    const form = event.target;
    const submitter = event.submitter;
    if (holdSubmitForSiteDetails(form, submitter)) {
      event.preventDefault();
      return;
    }
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
    const ids = STREAMS.has(data.get("action")) ? tickedIds(data) : [];
    setBusy(form, submitter, true);
    finished(form, submitter, ids, url, data).finally(() => setBusy(form, submitter, false));
  }

  // Heard on the way down: the bulk delete question is a form inside the bulk form, and
  // the browser stops such a submit event at the outer form on its way up.
  document.addEventListener("submit", takeSubmit, true);

  // Escape in a text field belongs to the field (clearing a search, closing suggestions);
  // leaving select mode from there would throw the ticks away.
  const isTextEntry = (target) =>
    target instanceof Element &&
    (target.matches("input:not([type=checkbox]), textarea, select") || target.isContentEditable);

  const searchField = () => document.querySelector('main [role="search"] input[type="search"]');

  // The hint is added here, not by the server: without the script the key does nothing.
  function addSearchHint() {
    const field = searchField();
    if (!field || field.parentElement.querySelector("kbd")) return;
    const hint = document.createElement("kbd");
    hint.setAttribute("aria-hidden", "true");
    hint.textContent = "/";
    field.after(hint);
  }

  // "/" outside a field jumps to the search field.
  document.addEventListener("keydown", (event) => {
    if (event.key !== "/" || event.defaultPrevented || event.ctrlKey || event.metaKey || event.altKey) return;
    if (isTextEntry(event.target)) return;
    const field = searchField();
    if (!field) return;
    event.preventDefault();
    field.focus();
  });

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

  async function showHistoryEntry() {
    if (await load(location.href, undefined, "none")) scrollToPlace();
  }

  window.addEventListener("popstate", () => {
    // The page has not moved yet: this is where the reader leaves the page on screen.
    rememberPlace();
    enhance(showHistoryEntry, () => location.reload());
  });

  // ---- Copy buttons: a button with `data-copy-from` copies the value of the field whose id it names ----
  const COPY_FEEDBACK_MS = 2000;
  const copyFeedbackTimers = new WeakMap();

  // The Clipboard API exists only on a secure origin, and bkd is often served over plain
  // http on a home network, so selecting the text and copying it is the fallback.
  async function copyFieldValue({ field, button }) {
    if (navigator.clipboard && window.isSecureContext) {
      try {
        await navigator.clipboard.writeText(field.value);
        return true;
      } catch {
        // Refused (no permission or focus): try the older way.
      }
    }
    field.select();
    let copied = false;
    try {
      copied = document.execCommand("copy");
    } catch {
      // Not supported: the text stays selected for the person to copy.
    }
    // select() moved the focus into the field. After a good copy it goes back to the button
    // that was pressed; after a refused one it stays, so Ctrl+C or Cmd+C still works.
    if (copied) button.focus();
    return copied;
  }

  // The status span is the one thing read out; clearing it later is not announced.
  function showCopyResult(button, result) {
    const label = button.querySelector(".lbl");
    if (!label) return;
    const status = button.parentElement.querySelector("[data-copy-status]");
    if (!button.dataset.copyLabel) button.dataset.copyLabel = label.textContent;
    label.textContent = result;
    if (status) status.textContent = result;
    clearTimeout(copyFeedbackTimers.get(button));
    copyFeedbackTimers.set(
      button,
      setTimeout(() => {
        label.textContent = button.dataset.copyLabel;
        if (status) status.textContent = "";
      }, COPY_FEEDBACK_MS),
    );
  }

  document.addEventListener("click", async (event) => {
    const button = event.target instanceof Element ? event.target.closest("button[data-copy-from]") : null;
    const field = button ? document.getElementById(button.dataset.copyFrom) : null;
    if (!(field instanceof HTMLInputElement)) return;
    const copied = await copyFieldValue({ field, button });
    showCopyResult(button, copied ? "Copied" : "Copy failed");
  });

  // Dropping a file on the import box fills its file field, as choosing it would.
  const dropBoxOf = (event) => (event.target instanceof Element ? event.target.closest(".drop") : null);
  document.addEventListener("dragover", (event) => {
    const box = dropBoxOf(event);
    if (!box) return;
    event.preventDefault();
    box.classList.add("over");
  });
  document.addEventListener("dragleave", (event) => dropBoxOf(event)?.classList.remove("over"));
  document.addEventListener("drop", (event) => {
    const box = dropBoxOf(event);
    if (!box) return;
    event.preventDefault();
    box.classList.remove("over");
    const input = box.querySelector("input[type=file]");
    if (input && event.dataTransfer.files.length > 0) input.files = event.dataTransfer.files;
  });

  localizeTimes(document);
  showScriptOnly(document);
  addSearchHint();
  updateSelectionCount();
  // Only an entry that was left before has a place; a new one keeps the browser's (a fragment).
  if (places.has(shownEntry)) scrollToPlace();
})();
