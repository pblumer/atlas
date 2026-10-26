# ADR-0391: A product is shown, and its picture is not part of what was promised

- **Status:** Accepted (amended 2026-10-26: the Console scales a raster picture down before it uploads it)
- **Implementation:** Landed
- **Date:** 2026-09-17
- **Deciders:** Atlas maintainers
- **Open question:** whether the portal's cascade should show a thumbnail per row, which would turn one request per opened product into one per row
- **Question checked:** 2026-09

## Context and problem statement

A catalogue row is a name and a price. Somebody choosing between two phones is
choosing between two names, and the thing that makes a catalogue read as a shop
rather than as a list — the picture of what is being ordered — had nowhere to live.

Two questions had to be answered together: where the bytes go, and whether a
release freezes them.

## Decision drivers

- A product record is read on every listing and frozen into every release; bytes
  that large do not belong in an answer nobody asked with them.
- A picture must be visible to the people a catalogue is *for*, who are not the
  people who maintain it.
- A product is referenced by catalogues rather than owned by one
  ([ADR-0315](0315-portal-roles-and-responsibilities.md)), so "may you read its home"
  is the wrong question to gate the picture with.

## Considered options

1. **A field on the item record**, base64 or a URL. Simple, and it puts half a
   megabyte into every product listing and every release.
2. **A file beside the stores, keyed by item id**, the way a catalogue's brand mark
   is stored ([ADR-0316](0316-portal-theme-per-catalogue.md)).
3. **An external URL on the record.** No bytes at all — and a catalogue whose
   pictures break when somebody else's server moves, plus a request from every
   viewer's browser to a third party.

## Decision outcome

Chosen option: **2**, with the brand mark's mechanics reused exactly:

```
GET    /api/v1/catalog-products/{id}/picture   role: user, gated on seeing the product
PUT    /api/v1/catalog-products/{id}/picture   role: productmanager, gated on its home
DELETE /api/v1/catalog-products/{id}/picture   role: productmanager, gated on its home
```

- **The file is the fact.** There is no flag on the record saying a picture exists,
  because a second copy of that fact is a second copy to be wrong after a restore
  that brought the JSON and not the image. A product without one answers 404, which
  is the ordinary case the caller falls back from.
- **PNG, JPEG and SVG.** The union of the two existing sets, and the reason is who
  uploads it: `brandimage.Photo` refuses SVG because a picture of a person arrives
  from a camera and the uploader there is every account; a product picture arrives
  from whoever maintains the catalogue, and half of what a service catalogue sells is
  software with a vector logo. The bytes are validated as the type they claim and
  served under the sandbox policy `brandimage.Serve` sets.
- **What was uploaded is what is served.** No resizing and no re-encoding: a server
  that re-encodes somebody's picture decides their product looks near enough. The
  page bounds it in CSS instead. (The server still serves exactly what it was given;
  since the amendment below, the Console scales a raster picture before it gives it.)

### Who may see it

Not "whoever may read its home catalogue", which is the gate on *changing* it. The
question is the one the portal actually asks: **does any catalogue this person may
read offer this product**. A customer of catalogue B legitimately orders a product
whose home is catalogue A, and a gate on the home alone shows that customer a name
and no picture — the one row on the page that looks broken.

A product nobody may see and a product with no picture answer the same 404, because
the two must be indistinguishable or the status code is the oracle the gate
withholds.

### A release does not freeze it

A release freezes what was **promised**: the product, its variants, the approval
rule, the ceiling, the price
([ADR-0312](0312-portal-catalogue-order-inventory.md)). A picture is how a thing is
shown and not what was agreed — a better photograph of the same laptop is not a
different laptop, and an order showing last month's photograph would be a record
nobody asked for. An order that must survive the product being withdrawn already
does: it carries the texts and the price it was placed under, and the picture is
simply absent once the product is gone.

## Amendment (2026-10-26): the Console scales a raster picture down before it uploads it

The route takes at most the asset budget — 512 KiB by default — and a photograph
of the product off a phone is five to fifteen megabytes. The editor passed the file
on unchanged, so the ordinary case ended in "The product was saved, but its picture
was not: the picture exceeds the 524288 byte limit".

**The product editor now draws a PNG or JPEG again in the browser before the
upload**, with the module a person's picture already used
([ADR-0368](0368-user-avatar.md), amended the same day; `api/web/picturescale.js`):

- **To what the shop can show.** The shop draws a picture whole and at most 200 CSS
  pixels tall. The shorter side is held to 600 pixels — that height at three times
  the pixel density, the most a phone has — and the longer side to 1200, so a banner
  does not stay one. Never enlarged. A 4:3 photograph comes out at 800×600 and a
  few hundred kilobytes at most.
- **An SVG is uploaded exactly as picked.** A vector has no pixels to scale, and
  drawing one would turn a vendor's mark into a raster of it.
- **A PNG stays a PNG** where it has transparency, and otherwise unless it costs
  more than twice its JPEG: a logo keeps its edges, and a photograph that happened to
  be saved as a PNG is sent as the JPEG it should have been.
- **The server's limit is learned, not copied.** When the server still answers 413 —
  a cut-out with transparency can only be a PNG and does not compress like a
  photograph — the picture is drawn again at three quarters of the size, up to four
  times, and the server's own refusal is shown if none fits.

This changes the bullet above in one respect, and the reason it was written still
holds. It refused **the server** deciding that a product looks near enough. What
the editor removes now is pixels the shop never draws and a phone's Exif data, which
includes where the photograph was taken; what it keeps is the shape, the
orientation, and a lossless PNG where that costs little. An author who needs exact
bytes served — a picture prepared to the pixel — uploads an SVG, or uses the route
directly, which is unchanged.

`e2e/catalog-product-picture.spec.mjs` holds the behaviour.

## Consequences

- One request per product whose panel is opened, and none for a product nobody
  opens. The portal renders the `<img>` and lets the 404 be the answer rather than
  asking first, which would be two requests to learn what one learns anyway.
- The picture is not in a backup that copies only the record stores. It lives in the
  design-time subtree beside the logos, which is what the backup route copies
  ([ADR-0107](0107-backup-and-restore.md)).
- Changing a picture changes it everywhere it is shown, including on orders already
  placed. That is the intended reading of "not part of what was promised", and it is
  the one consequence somebody could be surprised by.
