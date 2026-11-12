// Progressive enhancement. Every page already works as plain HTML: links, forms and
// redirects. This script only makes a click or a submit swap the new page in without a
// full reload, and shows times in the reader's time zone. If anything here fails, the
// browser falls back to the ordinary navigation, so the app keeps working.
(() => {
  "use strict";

  let inFlight = null;

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

  // Replaces the page's content with the one in `doc`. The signed-in frame keeps its
  // menu toggle and top bar, so only the sidebar and `main` change.
  function swapIn(doc) {
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
    const main = document.getElementById("main");
    if (main) main.focus({ preventScroll: true });
  }

  // `history`: "push" for a new page, "replace" for the same address, "none" for back/forward.
  async function load(url, init, history) {
    if (inFlight) inFlight.abort();
    const controller = new AbortController();
    inFlight = controller;
    const response = await fetch(url, { ...init, signal: controller.signal, credentials: "same-origin" });
    const type = response.headers.get("content-type") || "";
    if (!type.startsWith("text/html")) throw new Error("not a page");
    const doc = new DOMParser().parseFromString(await response.text(), "text/html");
    if (inFlight !== controller) return;
    inFlight = null;
    const finalUrl = new URL(response.url);
    if (history === "push") {
      // A POST answered without a redirect has no address of its own to come back to.
      const isNewAddress = !init || init.method !== "POST" || response.redirected;
      if (isNewAddress) window.history.pushState(null, "", finalUrl);
    }
    swapIn(doc);
    if (history !== "none") window.scrollTo(0, 0);
  }

  async function enhance(action, fallback) {
    try {
      await action();
    } catch (error) {
      if (error && error.name === "AbortError") return;
      fallback(error);
    }
  }

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

  document.addEventListener("submit", (event) => {
    if (event.defaultPrevented) return;
    const form = event.target;
    const submitter = event.submitter;
    // `formAction` is the page's own address when the button sets none, so ask the attribute.
    const own = (name) => submitter && submitter.hasAttribute(name);
    const url = new URL(own("formaction") ? submitter.formAction : form.action, location.href);
    if (!isPagePath(url)) return;
    event.preventDefault();
    const data = new FormData(form, submitter);
    const method = own("formmethod") ? submitter.formMethod : form.method;
    const isGet = method.toLowerCase() === "get";
    const multipart = form.enctype === "multipart/form-data";
    if (isGet) {
      url.search = new URLSearchParams(data).toString();
      enhance(
        () => load(url, undefined, "push"),
        () => location.assign(url),
      );
      return;
    }
    // The server may have handled a POST whose answer was not a page: submitting it
    // again could repeat it, so only reload. A request that never got through is
    // sent the ordinary way, with the pressed button's name and value kept.
    const body = multipart ? data : new URLSearchParams(data);
    const notDelivered = () => {
      if (submitter && submitter.name) {
        const hidden = document.createElement("input");
        hidden.type = "hidden";
        hidden.name = submitter.name;
        hidden.value = submitter.value;
        form.append(hidden);
      }
      HTMLFormElement.prototype.submit.call(form);
    };
    enhance(
      () => load(url, { method: "POST", body }, "push"),
      (error) => (error instanceof TypeError ? notDelivered() : location.reload()),
    );
  });

  window.addEventListener("popstate", () => {
    enhance(
      () => load(location.href, undefined, "none"),
      () => location.reload(),
    );
  });

  localizeTimes(document);
})();
