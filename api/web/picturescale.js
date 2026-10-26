// Scaling a picture down in the browser before it is uploaded: a person's
// (ADR-0368) and a product's (ADR-0391), both amended 2026-10-26.
//
// Both routes take at most the asset budget — half a megabyte by default — and a
// photograph straight off a phone is five to fifteen. Refusing it told the person a
// byte count and left them to find an image editor. Raising the budget would have
// stored a camera's full resolution for a picture drawn a few hundred pixels wide at
// most, and every page showing it would download it. So the picture is scaled here,
// to what the page can use, and the server's limit stays the gate it was.
//
// Here rather than on the server, because the browser already holds a decoder for
// what a person can pick and runs it in its own sandbox. The server decoding an
// arbitrary upload would be a new way to make it spend memory — a small PNG can
// claim a gigapixel canvas — in the process that also runs the engine.
//
// A raster picture is always redrawn, even one already small enough, and that is
// deliberate: a phone writes where a photo was taken into the file, and the stored
// picture is served to everybody who may see it. A redrawn picture carries only its
// pixels. Browsers apply the file's orientation when they draw it, so a portrait
// stays upright once the tag that said so is gone.

// PERSON is how far a picture of a person is scaled. The shorter side decides,
// because the circle is cut from the middle of the picture (object-fit: cover) and
// only the shorter side is ever shown whole; the largest circle Atlas draws is 48 CSS
// pixels, and 256 covers that at any device pixel ratio a screen has. The longer side
// is held to four times that, so a panorama does not stay one.
export const PERSON = { short: 256, long: 1024 };

// PRODUCT is how far a product's picture is scaled. The shop shows it whole
// (object-fit: contain) and at most 200 CSS pixels tall, and 600 is that at three
// times the pixel density, the most a phone has. The shorter side decides, so a
// landscape photograph — the usual one — is held to exactly that height; the longer
// side is held to twice that, so a banner does not stay one.
export const PRODUCT = { short: 600, long: 1200 };

// uploadPicture scales a picked file and hands it to send, which uploads it. When
// the server answers that it is still too large, the picture is drawn again at three
// quarters of the size, up to four times.
//
// The limit is the server's, it is configurable, and no number of it is copied into
// this page: a page that guessed it would refuse pictures the server takes, or send
// ones it refuses, and its refusal names the actual figure. Asking costs one upload
// per step, and a photograph never takes a second — at these sizes it is a fraction
// of the default budget. What can is a picture with transparency, which only a PNG
// keeps and which does not compress like a photograph.
export async function uploadPicture(file, send, bounds) {
  for (let factor = 1, tries = 0; ; factor *= 0.75, tries++) {
    const picture = await scalePicture(file, bounds, { factor });
    try {
      return await send(picture);
    } catch (err) {
      if (err.status !== 413 || picture === file || tries === 4) throw err;
    }
  }
}

// scalePicture returns the bytes to upload for a picked file. It never enlarges.
//
// An SVG is returned as it is: a vector has no pixels to scale, and drawing one would
// turn a vendor's mark into a raster of it. A JPEG stays a JPEG. A PNG stays a PNG
// where it has transparency a JPEG would lose, and otherwise stays one unless it
// costs more than twice its JPEG. The margin is there because the browser's PNG
// encoder compresses less tightly than an image editor's: a two-colour logo comes
// out about half as large again as its JPEG, and a JPEG would put fringes on its
// edges. A photograph saved as a PNG comes out several times larger, and is sent as
// the photograph it is.
export async function scalePicture(file, bounds = PERSON, { factor = 1, quality = 0.9 } = {}) {
  if (file.type === "image/svg+xml") return file;
  const url = URL.createObjectURL(file);
  try {
    const img = await loadImage(url);
    const w = img.naturalWidth;
    const h = img.naturalHeight;
    if (!w || !h) throw new Error("this file could not be read as a picture");
    const fit = Math.min(1,
      (bounds.short || Infinity) / Math.min(w, h),
      (bounds.long || Infinity) / Math.max(w, h));
    const width = Math.max(1, Math.round(w * fit * factor));
    const height = Math.max(1, Math.round(h * fit * factor));
    const canvas = shrink(img, w, h, width, height);
    // A JPEG has no alpha to look for, and a JPEG's edge pixels must not talk a
    // photograph into a PNG.
    if (file.type !== "image/png") return await toBlob(canvas, "image/jpeg", quality);
    const png = await toBlob(canvas, "image/png");
    if (hasTransparency(canvas)) return png;
    const jpeg = await toBlob(canvas, "image/jpeg", quality);
    return png.size <= 2 * jpeg.size ? png : jpeg;
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
    img.onerror = () => reject(new Error("this file could not be read as a picture"));
    img.src = url;
  });
}

function toBlob(canvas, type, quality) {
  return new Promise((resolve, reject) => canvas.toBlob(
    (blob) => (blob ? resolve(blob) : reject(new Error("the picture could not be encoded"))),
    type, quality));
}
