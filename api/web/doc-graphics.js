// Document graphics (ADR-0143): turning what an editor drew into something a PDF
// page can hold, and deciding how big a page that needs.
//
// It lives apart from the documents that use it because both of them do — the
// process document and the decision document rasterize a diagram the same way and
// turn the page for a wide one by the same rule. Keeping it here is what stops the
// two document modules from importing each other in a circle now that the process
// document also renders a decision's rule table
// (ADR-0328).

// svgToJpeg rasterizes the diagram bpmn-js drew. The SVG is drawn onto a canvas
// at a fixed scale and encoded as JPEG, whose bytes the PDF embeds untranscoded.
// A white backdrop is painted first: a BPMN diagram has a transparent background,
// and JPEG has no alpha, so without it the diagram would come out on black.
export async function svgToJpeg(svg, { scale = 2, quality = 0.92, maxPixels = 4000 } = {}) {
  const sized = sizeOfSvg(svg);
  let width = Math.max(1, Math.round(sized.width * scale));
  let height = Math.max(1, Math.round(sized.height * scale));
  const cap = Math.max(width, height);
  if (cap > maxPixels) {
    const shrink = maxPixels / cap;
    width = Math.max(1, Math.round(width * shrink));
    height = Math.max(1, Math.round(height * shrink));
  }

  const blob = new Blob([svg], { type: "image/svg+xml;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  try {
    const img = await loadImage(url);
    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    const ctx = canvas.getContext("2d");
    ctx.fillStyle = "#ffffff";
    ctx.fillRect(0, 0, width, height);
    ctx.drawImage(img, 0, 0, width, height);

    const trimmed = trimToContent(canvas);
    const out = await new Promise((resolve) => trimmed.toBlob(resolve, "image/jpeg", quality));
    return { bytes: new Uint8Array(await out.arrayBuffer()), width: trimmed.width, height: trimmed.height };
  } finally {
    URL.revokeObjectURL(url);
  }
}

// trimToContent crops the blank margin off a rasterized diagram. The SVG bpmn-js
// exports is boxed generously — its viewBox can start well above the topmost
// shape — and a document whose centrepiece floats in a band of white looks like
// a mistake. Returns the original canvas when there is nothing to trim.
export function trimToContent(canvas, { padding = 12 } = {}) {
  const ctx = canvas.getContext("2d");
  const { width, height } = canvas;
  let pixels;
  try {
    pixels = ctx.getImageData(0, 0, width, height).data;
  } catch {
    return canvas; // a tainted canvas cannot be read; ship it untrimmed
  }
  let top = height, left = width, right = -1, bottom = -1;
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const i = (y * width + x) * 4;
      // Near-white is background. The tolerance keeps JPEG-ish noise and
      // antialiased edges from counting as content.
      if (pixels[i] > 247 && pixels[i + 1] > 247 && pixels[i + 2] > 247) continue;
      if (y < top) top = y;
      if (y > bottom) bottom = y;
      if (x < left) left = x;
      if (x > right) right = x;
    }
  }
  if (right < 0) return canvas; // nothing but background — leave it alone

  const x0 = Math.max(0, left - padding);
  const y0 = Math.max(0, top - padding);
  const w = Math.min(width, right + padding + 1) - x0;
  const h = Math.min(height, bottom + padding + 1) - y0;
  if (w >= width && h >= height) return canvas;

  const out = document.createElement("canvas");
  out.width = w;
  out.height = h;
  const octx = out.getContext("2d");
  octx.fillStyle = "#ffffff";
  octx.fillRect(0, 0, w, h);
  octx.drawImage(canvas, x0, y0, w, h, 0, 0, w, h);
  return out;
}

function loadImage(url) {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => resolve(img);
    img.onerror = () => reject(new Error("the diagram image could not be rendered"));
    img.src = url;
  });
}

// sizeOfSvg reads the drawing's dimensions from the SVG bpmn-js produced,
// preferring the viewBox (which is always present and in diagram units) over the
// width/height attributes, which may carry percentages.
export function sizeOfSvg(svg) {
  const viewBox = /viewBox="([-\d.eE]+)\s+([-\d.eE]+)\s+([-\d.eE]+)\s+([-\d.eE]+)"/.exec(svg);
  if (viewBox) {
    const w = parseFloat(viewBox[3]);
    const h = parseFloat(viewBox[4]);
    if (w > 0 && h > 0) return { width: w, height: h };
  }
  const w = parseFloat((/\swidth="([\d.]+)/.exec(svg) || [])[1]);
  const h = parseFloat((/\sheight="([\d.]+)/.exec(svg) || [])[1]);
  if (w > 0 && h > 0) return { width: w, height: h };
  return { width: 800, height: 600 };
}

// wantsLandscape decides whether the diagram earns its own landscape page. A wide,
// busy diagram fitted to a portrait page's narrow content width shrinks into a
// thin, unreadable strip — turning the page to A4 landscape gives it the width it
// needs. The trigger is deliberately narrow so a small diagram, which reads fine
// in portrait, is left in the flow of the document: the diagram must be clearly
// wider than tall *and* the process busy enough that the shrink would cost
// legibility — or so extreme in aspect that portrait cannot serve it at any size.
export function wantsLandscape(diagram, elementCount = 0, { minAspect = 1.3, minElements = 10, extremeAspect = 2.5 } = {}) {
  if (!diagram || !diagram.width || !diagram.height) return false;
  const aspect = diagram.width / diagram.height;
  if (aspect >= extremeAspect) return true;
  return aspect >= minAspect && elementCount >= minElements;
}
