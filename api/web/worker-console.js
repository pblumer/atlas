// Worker terminology compatibility layer (ADR-0203).
//
// The server API and persisted sidecar records still use the pre-ADR "connector"
// vocabulary. This first vertical migration slice changes the operator-facing
// Console vocabulary without forking that compatibility contract: app.js continues
// to route and call /api/v1/connectors, while this small shell adapter makes Workers
// the canonical route and presents connector kinds/instances as Worker Types/Workers.
//
// Delete this adapter once app.js and the public API have completed the staged
// migration described by ADR-0203; until then it is deliberately presentation-only.
(function workerConsoleTerminology() {
  "use strict";

  const WORKERS = "#/console/workers";
  const LEGACY = "#/console/connectors";
  let workerRoute = false;
  let patchQueued = false;

  // app.js currently only knows the legacy hash. This listener is registered before
  // app.js, so a navigation to the canonical Workers URL is translated synchronously
  // before app.js' hashchange handler reads location.hash. Once setChrome has rendered
  // the legacy nav entry, patch() changes the visible URL back without another event.
  function normalizeRouteForLegacyRouter() {
    if (location.hash === WORKERS) {
      workerRoute = true;
      history.replaceState(null, "", LEGACY);
      return;
    }
    workerRoute = location.hash === LEGACY;
  }

  function replaceText(node, from, to) {
    if (node && node.textContent && node.textContent.trim() === from) node.textContent = to;
  }

  function patchNavigation() {
    for (const link of document.querySelectorAll(`a[href="${LEGACY}"]`)) {
      link.setAttribute("href", WORKERS);
      if (link.closest("#topnav")) link.textContent = "Workers";
      if (link.textContent.trim() === "Configure connector ↗") link.textContent = "Configure worker ↗";
    }
  }

  function patchWorkerPage() {
    const view = document.getElementById("view");
    if (!view || !workerRoute) return false;

    const heading = [...view.querySelectorAll("h1")]
      .find((h) => h.textContent.trim() === "Connectors" || h.textContent.trim() === "Workers");
    if (!heading) return false;
    heading.textContent = "Workers";

    const catalogCard = heading.closest(".card");
    if (catalogCard) {
      let catalogTitle = catalogCard.querySelector("[data-worker-catalog-title]");
      if (!catalogTitle) {
        catalogTitle = document.createElement("h2");
        catalogTitle.dataset.workerCatalogTitle = "1";
        catalogTitle.textContent = "Worker catalog";
        catalogTitle.style.cssText = "padding:0 18px;margin:12px 0 4px";
        const intro = catalogCard.querySelector("p.muted");
        if (intro) catalogCard.insertBefore(catalogTitle, intro);
      }
      const intro = catalogCard.querySelector("p.muted");
      if (intro) {
        intro.innerHTML = "Worker Types available to this Atlas instance. A Worker Type defines a capability; configured workers below bind that capability to a concrete target and identity.";
      }
    }

    for (const h2 of view.querySelectorAll("h2")) {
      replaceText(h2, "Configured connectors", "Configured workers");
    }

    const newWorker = document.getElementById("new-connector");
    if (newWorker) {
      newWorker.textContent = "New worker";
      newWorker.title = "Configure a new worker";
    }

    const configured = [...view.querySelectorAll("h2")]
      .find((h) => h.textContent.trim() === "Configured workers");
    const configuredCard = configured && configured.closest(".card");
    if (configuredCard) {
      const intro = configuredCard.querySelector("p.muted");
      if (intro) {
        intro.innerHTML = "Configured workers bind a <b>Worker Type</b> to a concrete endpoint and identity. The existing connector API remains the compatibility layer during the ADR-0203 migration; credentials stay referenced, never embedded in process models.";
      }
      const firstHeader = configuredCard.querySelector("thead th");
      replaceText(firstHeader, "Connector", "Worker");
    }

    const secrets = [...view.querySelectorAll("h2")].find((h) => h.textContent.trim() === "Secrets");
    const secretsCard = secrets && secrets.closest(".card");
    if (secretsCard) {
      const intro = secretsCard.querySelector("p.muted");
      if (intro && /connector/i.test(intro.textContent)) {
        intro.innerHTML = "Credentials referenced by configured workers, sealed at rest with AES-256-GCM. Values are never shown after they are set. During the compatibility window the existing <code>ATLAS_CONNECTOR_&lt;REF&gt;_TOKEN</code> fallback remains supported.";
      }
    }

    document.title = document.title.replace(/^Connectors · Console/, "Workers · Console");
    return true;
  }

  function patch() {
    patchQueued = false;
    patchNavigation();

    // Do not publish the Workers hash until app.js has actually entered its legacy
    // connector route. The nav entry is rendered by setChrome after route() captured
    // the legacy path, so seeing it is the safe hand-off point.
    const legacyNav = document.querySelector(`#topnav a[href="${WORKERS}"]`);
    if (workerRoute && legacyNav) {
      patchWorkerPage();
      if (location.hash === LEGACY) history.replaceState(null, "", WORKERS);
    }
  }

  function schedulePatch() {
    if (patchQueued) return;
    patchQueued = true;
    queueMicrotask(patch);
  }

  normalizeRouteForLegacyRouter();
  window.addEventListener("hashchange", () => {
    normalizeRouteForLegacyRouter();
    schedulePatch();
  });

  const observer = new MutationObserver(schedulePatch);
  observer.observe(document.documentElement, { subtree: true, childList: true, characterData: true });
  schedulePatch();
})();
