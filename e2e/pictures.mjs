// Shared helpers for the specs that upload a picture (user-picture.spec.mjs,
// catalog-product-picture.spec.mjs): making one in the page, and reading back what
// was sent.

// makeImage draws a picture in the page and returns its bytes. Noise by default,
// because noise is what a JPEG cannot compress: it is how a few hundred kilobytes of
// pixels become a file the size a camera writes. `flat` draws a logo instead — two
// colours in blocks — which is what a PNG compresses to almost nothing. Optionally
// with an Exif segment carrying an orientation and a marker the stored picture must
// not carry on.
export async function makeImage(page, {
  width, height, type = "image/jpeg", transparent = false, flat = false, orientation = 0, marker = "",
}) {
  const b64 = await page.evaluate(async ({ width, height, type, transparent, flat, orientation, marker }) => {
    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    const ctx = canvas.getContext("2d");
    if (flat) {
      ctx.fillStyle = "#ffffff";
      ctx.fillRect(0, 0, width, height);
      ctx.fillStyle = "#0b6e4f";
      ctx.fillRect(width / 8, height / 8, width / 3, height * 3 / 4);
      ctx.fillRect(width / 2, height / 3, width * 3 / 8, height / 3);
    } else {
      const img = ctx.createImageData(width, height);
      let seed = 2463534242;
      for (let i = 0; i < img.data.length; i += 4) {
        seed ^= seed << 13; seed ^= seed >>> 17; seed ^= seed << 5;
        img.data[i] = seed & 255;
        img.data[i + 1] = (seed >>> 8) & 255;
        img.data[i + 2] = (seed >>> 16) & 255;
        // A transparent picture is see-through on its left half.
        img.data[i + 3] = transparent && ((i / 4) % width) < width / 2 ? 0 : 255;
      }
      ctx.putImageData(img, 0, 0);
    }
    const blob = await new Promise((resolve) => canvas.toBlob(resolve, type, 0.95));
    let bytes = Array.from(new Uint8Array(await blob.arrayBuffer()));
    if (orientation) {
      // TIFF, big-endian: one IFD entry, Orientation (0x0112) as a SHORT, then the
      // marker in bytes no entry points at.
      const enc = new TextEncoder();
      const tiff = [0x4d, 0x4d, 0x00, 0x2a, 0, 0, 0, 8, 0, 1,
        0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, orientation, 0, 0, 0, 0, 0, 0, ...enc.encode(marker)];
      const payload = [...enc.encode("Exif"), 0, 0, ...tiff];
      const len = payload.length + 2;
      bytes = [bytes[0], bytes[1], 0xff, 0xe1, len >> 8, len & 255, ...payload, ...bytes.slice(2)];
    }
    let s = "";
    for (const b of bytes) s += String.fromCharCode(b);
    return btoa(s);
  }, { width, height, type, transparent, flat, orientation, marker });
  return Buffer.from(b64, "base64");
}

// sizeOf reads a PNG's or a baseline/progressive JPEG's dimensions off its header.
export function sizeOf(buf) {
  if (buf[0] === 0x89 && buf.toString("ascii", 1, 4) === "PNG") {
    return { type: "png", width: buf.readUInt32BE(16), height: buf.readUInt32BE(20) };
  }
  let i = 2;
  while (i < buf.length) {
    const marker = buf[i + 1];
    const len = buf.readUInt16BE(i + 2);
    if (marker >= 0xc0 && marker <= 0xc2) {
      return { type: "jpeg", height: buf.readUInt16BE(i + 5), width: buf.readUInt16BE(i + 7) };
    }
    i += 2 + len;
  }
  throw new Error("no frame header");
}

// pngHasTransparency asks the page, which has the decoder.
export async function pngHasTransparency(page, buf) {
  return page.evaluate(async (b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    const bmp = await createImageBitmap(new Blob([bytes], { type: "image/png" }));
    const canvas = document.createElement("canvas");
    canvas.width = bmp.width;
    canvas.height = bmp.height;
    const ctx = canvas.getContext("2d");
    ctx.drawImage(bmp, 0, 0);
    const { data } = ctx.getImageData(0, 0, bmp.width, bmp.height);
    for (let i = 3; i < data.length; i += 4) if (data[i] < 255) return true;
    return false;
  }, buf.toString("base64"));
}
