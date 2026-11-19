// LND4SU service worker.
// Caches ONLY the static shell (CSS, icons, manifest). Every page and API
// call is network-only, so a stale problem list or a stale verdict can never
// be shown. Offline pages fail honestly with the browser's offline page.
const CACHE = "lnd4su-v1";
const ASSETS = [
  "/static/style.css",
  "/manifest.json",
  "/static/icons/icon-192.png",
  "/static/icons/icon-512.png",
  "/static/icons/icon-180.png",
  "/static/icons/icon.svg"
];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(CACHE)
      .then((c) => c.addAll(ASSETS))
      .then(() => self.skipWaiting())
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  // Never intercept POSTs or cross-origin requests.
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;

  // Only these paths are cache-first. Everything else falls through to the
  // network untouched: /, /p/*, /s/*, /s/*/status, /me, /admin/*, /healthz.
  const isStatic = url.pathname.startsWith("/static/") || url.pathname === "/manifest.json";
  if (!isStatic) return;

  event.respondWith(
    caches.match(req).then((hit) => {
      // Cache-first, but refresh in the background so a deploy that changes
      // style.css is picked up on the next page load instead of never.
      const net = fetch(req).then((res) => {
        if (res && res.ok) {
          const copy = res.clone();
          caches.open(CACHE).then((c) => c.put(req, copy)).catch(() => {});
        }
        return res;
      });
      if (hit) {
        event.waitUntil(net.catch(() => {}));
        return hit;
      }
      return net;
    })
  );
});
