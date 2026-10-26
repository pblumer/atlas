// Scaling a picture of a person down in the browser before it is uploaded
// (ADR-0368, amended 2026-10-26).
//
// The route behind it takes at most the asset budget — half a megabyte by default —
// and a photograph straight off a phone is five to fifteen. Refusing it told the
// person a byte count and left them to find an image editor. Raising the budget would
// have stored a camera's full resolution for a circle drawn at 48 pixels, and every
// page naming that person would download it. So the picture is scaled here, to what
// the circle can use, and the server's limit stays the gate it was.
//
// Here rather than on the server, because the browser already holds a decoder for
// what a person can pick and runs it in its own sandbox. The server decoding an
// arbitrary upload would be a new way to make it spend memory — a small PNG can
// claim a gigapixel canvas — in the process that also runs the engine.
//
// The picture is always redrawn, even one already small enough, and that is
// deliberate: a phone writes where a photo was taken into the file, and the stored
// picture is served to every colleague signed in. A redrawn picture carries only its
// pixels. Browsers apply the file's orientation when they draw it, so a portrait
// stays upright once the tag that said so is gone.

// PICTURE_EDGE is the shorter side of a stored picture, in pixels. The largest circle
// Atlas draws is 48 CSS pixels; 256 covers that at any device pixel ratio a screen
// has, at a few dozen kilobytes.
export const PICTURE_EDGE = 256;

// scalePicture returns the bytes to upload for a picked file: a JPEG, or a PNG where
// the original has transparency a JPEG would lose. It never enlarges.
//
// The shorter side decides, because the circle is cut from the middle of the picture
// (object-fit: cover) and only the shorter side is ever shown whole. The longer one
// is held to four times that, so a panorama does not stay one.
export async function scalePicture(file, { edge = PICTURE_EDGE, quality = 0.9 } = {}) {
  const url = URL.createObjectURL(file);
  try {
    const img = await loadImage(url);
    const w = img.naturalWidth;
    const h = img.naturalHeight;
    if (!w || !h) throw new Error("This file could not be read as a picture");
    const scale = Math.min(1, edge / Math.min(w, h), (4 * edge) / Math.max(w, h));
    const width = Math.max(1, Math.round(w * scale));
    const height = Math.max(1, Math.round(h * scale));
    const canvas = shrink(img, w, h, width, height);
    // A JPEG has no alpha to look for, and a JPEG's edge pixels must not talk a
    // photograph into a PNG three times its size.
    const type = file.type === "image/png" && hasTransparency(canvas) ? "image/png" : "image/jpeg";
    return await toBlob(canvas, type, quality);
  } finally {
    URL.revokeObjectURL(url);
  }
}

// shrink draws the image at the target size. A large reduction is taken in halves:
// one bilinear step from 4000 pixels to 256 reads a sixteenth of the pixels and
// draws the rest as noise, which on a face shows even in a small circle.
function shrink(img, w, h, width, height) {
  let src = img;
  let sw = w;
  let sh = h;
  while (sw >= 2 * width && sh >= 2 * height) {
    const half = canvasOf(Math.round(sw / 2), Math.round(sh / 2));
    draw(half, src);
    src = half;
    sw = half.width;
    sh = half.height;
  }
  const out = canvasOf(width, height);
  draw(out, src);
  return out;
}

function canvasOf(width, height) {
  const canvas = document.createElement("canvas");
  canvas.width = width;
  canvas.height = height;
  return canvas;
}

function draw(canvas, src) {
  const ctx = canvas.getContext("2d");
  ctx.imageSmoothingEnabled = true;
  ctx.imageSmoothingQuality = "high";
  ctx.drawImage(src, 0, 0, canvas.width, canvas.height);
}

function hasTransparency(canvas) {
  const { data } = canvas.getContext("2d").getImageData(0, 0, canvas.width, canvas.height);
  for (let i = 3; i < data.length; i += 4) {
    if (data[i] < 255) return true;
  }
  return false;
}

function loadImage(url) {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => resolve(img);
    img.onerror = () => reject(new Error("This file could not be read as a picture"));
    img.src = url;
  });
}

function toBlob(canvas, type, quality) {
  return new Promise((resolve, reject) => canvas.toBlob(
    (blob) => (blob ? resolve(blob) : reject(new Error("The picture could not be encoded"))),
    type, quality));
}
