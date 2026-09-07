// Loading the canvas bundle, in one place because there is one bundle.
//
// Both diagram-js canvases Atlas draws with — the ArchiMate view (ADR-0189) and the
// UML class diagram (ADR-0237) — ship in `vendor/canvas/`, sharing the one copy of
// the library they used to carry a copy of each. Two views loading the same file is
// the consequence, and it is why the loading lives here rather than twice: whichever
// view is opened first fetches it, and the second gets what is already there instead
// of a second <script> and a second stylesheet link for the same two files.
//
// It is fetched on demand, the way the Modeler fetches bpmn-js: it is a hundred and
// twenty kilobytes that only these two views need, and the lists beside them should
// not pay for it.
let pending;

export function loadCanvasBundle() {
  if (globalThis.AtlasCanvas) return Promise.resolve(globalThis.AtlasCanvas);
  if (pending) return pending;

  const href = "vendor/canvas/diagram-js.css";
  if (!document.querySelector(`link[href="${href}"]`)) {
    const link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = href;
    document.head.appendChild(link);
  }

  pending = new Promise((resolve, reject) => {
    const script = document.createElement("script");
    script.src = "vendor/canvas/atlas-canvas.js";
    script.onload = () => resolve(globalThis.AtlasCanvas);
    script.onerror = () => {
      // A failed load must not be remembered as one that is still in flight: the next
      // view to ask would wait on a promise that will never settle.
      pending = undefined;
      reject(new Error("Could not load the diagram canvas"));
    };
    document.head.appendChild(script);
  });
  return pending;
}
