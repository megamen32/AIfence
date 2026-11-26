(() => {
  // Airlock authenticates the same-origin API request, then the adapter adds
  // Baserow's JWT for that very user on the private server-to-server hop.
  function ownedAPI(value) {
    const url = new URL(value, document.baseURI);
    return url.origin === location.origin && url.pathname.startsWith("/api/");
  }
  const nativeJWT = (value) => typeof value === "string" && /^JWT\s+/i.test(value);
  const tracked = new WeakSet();
  const xhr = XMLHttpRequest.prototype;
  const open = xhr.open;
  const setHeader = xhr.setRequestHeader;
  xhr.open = function (method, url, ...rest) {
    if (ownedAPI(url)) tracked.add(this);
    else tracked.delete(this);
    return open.call(this, method, url, ...rest);
  };
  xhr.setRequestHeader = function (key, value) {
    if (tracked.has(this) && key.toLowerCase() === "authorization" && nativeJWT(value)) return;
    return setHeader.call(this, key, value);
  };
  const fetch = window.fetch;
  window.fetch = function (input, init) {
    const request = input instanceof Request ? input : null;
    if (ownedAPI(request ? request.url : input)) {
      const headers = new Headers(init?.headers ?? request?.headers);
      if (nativeJWT(headers.get("Authorization"))) {
        headers.delete("Authorization");
        init = { ...init, headers };
      }
    }
    return fetch.call(this, input, init);
  };
})();

(() => {
  if (window.parent === window) return;
  const narrow = matchMedia("(max-width: 800px)");
  const onTable = () => /^\/database\/\d+\/table\/\d+(?:\/|$)/.test(location.pathname);
  const inPane = () => narrow.matches && onTable() && document.documentElement.dataset.airlockEmbedded === "true";
  const stockToggle = ".sidebar__foot-link .baserow-icon-sidebar";
  let active = false;
  let autoCollapsed = false;
  let applied = false;
  let scheduled = false;

  const style = document.createElement("style");
  style.textContent = `
    html[data-airlock-narrow-grid] .guided-tour-step__container { display: none !important; }
    html[data-airlock-narrow-grid] .grid-view__description { padding-inline-start: 6px; gap: 0; }
    html[data-airlock-narrow-grid] .grid-view__description-icon-container { display: none; }
    html[data-airlock-narrow-grid] .grid-view__description-name { flex: 1; min-width: 0; }
    html[data-airlock-narrow-grid] .grid-view__description-options { flex: 0 0 auto; }
    html[data-airlock-narrow-grid] .grid-view__description-icon-trigger {
      padding: 0 3px 0 1px;
      font-size: 10px;
    }
    html[data-airlock-narrow-grid] .grid-view__description-options > :not(.grid-view__description-icon-trigger) {
      display: none;
    }
    html[data-airlock-narrow-grid] .grid-view__description:is(:hover, :focus-within) .grid-view__description-icon-trigger {
      color: #202128;
    }
  `;
  document.head.append(style);

  function update() {
    scheduled = false;
    const next = inPane();
    document.documentElement.toggleAttribute("data-airlock-narrow-grid", next);
    const sidebar = document.querySelector(".layout__col-1");
    const button = sidebar?.querySelector(stockToggle)?.closest("a");
    if (!next) {
      if (autoCollapsed && button && sidebar.offsetWidth < 170) button.click();
      active = false;
      autoCollapsed = false;
      applied = false;
      return;
    }
    active = true;
    // The layout is server-rendered, but its stock toggle handles events
    // only after Vue has mounted. Wait for the interactive grid.
    if (applied || !button || !document.querySelector(".grid-view__cell")) return;
    applied = true;
    if (sidebar.offsetWidth >= 170) {
      autoCollapsed = true;
      button.click();
    }
  }

  function schedule() {
    if (!scheduled) {
      scheduled = true;
      requestAnimationFrame(update);
    }
  }
  document.addEventListener("click", (event) => {
    if (active && event.isTrusted && event.target.closest?.(".sidebar__foot-link")?.querySelector(stockToggle)) {
      autoCollapsed = false;
      applied = true;
    }
  }, true);
  function watch() {
    new MutationObserver(() => {
      if ((!applied && inPane()) || active !== inPane()) schedule();
    }).observe(document.body, { childList: true, subtree: true });
    new MutationObserver(schedule).observe(document.documentElement, { attributes: true, attributeFilter: ["data-airlock-embedded"] });
    schedule();
  }
  document.addEventListener("DOMContentLoaded", watch, { once: true });
  narrow.addEventListener("change", schedule);
  window.addEventListener("popstate", schedule);
  window.addEventListener("load", schedule, { once: true });
})();
