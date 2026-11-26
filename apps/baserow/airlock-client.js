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
