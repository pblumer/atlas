// The symbols of atlas's modules — the apps the drawer offers. Each is a white glyph
// in a 24-unit box, drawn on the module's colour, so a module looks the same in the
// drawer, in the welcome illustration and in documents (docs/brand/README.md,
// "Module symbols").
//
// This table is the one drawing of them. It is plain JSON between the two markers so
// that the brand scripts read exactly what the Console paints: scripts/brand/modules.py
// writes the copies under docs/brand/icons/modules, scripts/brand/welcome.py puts the
// symbols on the rocket of the welcome illustration. Keep it JSON — double quotes,
// no comments, no trailing commas — and run both scripts after a change;
// api/modulesymbols_internal_test.go fails until the copies match.
//
// The glyphs are fixed literals, never anything from the server, so assigning them as
// markup introduces no injection surface.
export const MODULE_ICONS = /* JSON */ {
  "console": {
    "name": "Console",
    "colour": "#0b5cff",
    "glyph": "<path d=\"M5 16a7 7 0 0 1 14 0\" fill=\"none\" stroke=\"#fff\" stroke-width=\"2.2\" stroke-linecap=\"round\"/><path d=\"M12 16l3.6-4.4\" stroke=\"#fff\" stroke-width=\"2.2\" stroke-linecap=\"round\"/><circle cx=\"12\" cy=\"16\" r=\"1.9\" fill=\"#fff\"/><path d=\"M7 9.6l.9.9M12 7.6v1.3M17 9.6l-.9.9\" stroke=\"#fff\" stroke-width=\"1.6\" stroke-linecap=\"round\"/>"
  },
  "modeler": {
    "name": "Modeler",
    "colour": "#7c5cff",
    "glyph": "<circle cx=\"5\" cy=\"12\" r=\"2.3\" fill=\"none\" stroke=\"#fff\" stroke-width=\"1.8\"/><path d=\"M7.3 12H9\" stroke=\"#fff\" stroke-width=\"1.8\" stroke-linecap=\"round\"/><rect x=\"9\" y=\"8.5\" width=\"6.2\" height=\"7\" rx=\"1.8\" fill=\"#fff\"/><path d=\"M15.2 12H16.4\" stroke=\"#fff\" stroke-width=\"1.8\" stroke-linecap=\"round\"/><path d=\"M19.6 8.8 22.8 12 19.6 15.2 16.4 12Z\" fill=\"none\" stroke=\"#fff\" stroke-width=\"1.8\" stroke-linejoin=\"round\"/>"
  },
  "data": {
    "name": "Data",
    "colour": "#0ea5e9",
    "glyph": "<rect x=\"3.5\" y=\"4.5\" width=\"8\" height=\"7\" rx=\"1.6\" fill=\"#fff\"/><rect x=\"12.5\" y=\"12.5\" width=\"8\" height=\"7\" rx=\"1.6\" fill=\"#fff\"/><path d=\"M3.5 7.4h8M12.5 15.4h8\" stroke=\"#0ea5e9\" stroke-width=\"1.1\"/><path d=\"M11.5 8H16a.5.5 0 0 1 .5.5v4\" fill=\"none\" stroke=\"#fff\" stroke-width=\"1.6\" stroke-linecap=\"round\" stroke-linejoin=\"round\"/>"
  },
  "operations": {
    "name": "Operations",
    "colour": "#14b8a6",
    "glyph": "<path d=\"M3 12.5h4l2-5 3.4 10 2.6-7 1.6 2H21\" fill=\"none\" stroke=\"#fff\" stroke-width=\"2.1\" stroke-linecap=\"round\" stroke-linejoin=\"round\"/>"
  },
  "tasks": {
    "name": "Tasks",
    "colour": "#f59e0b",
    "glyph": "<rect x=\"4\" y=\"4\" width=\"16\" height=\"16\" rx=\"3.5\" fill=\"none\" stroke=\"#fff\" stroke-width=\"1.9\"/><path d=\"M7.6 11.6l2.6 2.6 6-6.2\" fill=\"none\" stroke=\"#fff\" stroke-width=\"2.2\" stroke-linecap=\"round\" stroke-linejoin=\"round\"/><path d=\"M8 17h8\" stroke=\"#fff\" stroke-width=\"1.6\" stroke-linecap=\"round\" opacity=\".7\"/>"
  },
  "panorama": {
    "name": "Panorama",
    "colour": "#ec4899",
    "glyph": "<path d=\"M12 12 5.5 6.5M12 12l7-4.5M12 12l-5.5 6.5M12 12l6.5 5.5\" stroke=\"#fff\" stroke-width=\"1.5\" stroke-linecap=\"round\"/><circle cx=\"12\" cy=\"12\" r=\"3\" fill=\"#fff\"/><circle cx=\"5.5\" cy=\"6.5\" r=\"2\" fill=\"#fff\"/><circle cx=\"19\" cy=\"7.5\" r=\"2\" fill=\"#fff\"/><circle cx=\"6.5\" cy=\"18.5\" r=\"2\" fill=\"#fff\"/><circle cx=\"18.5\" cy=\"17.5\" r=\"2\" fill=\"#fff\"/>"
  },
  "catalog": {
    "name": "Catalogue",
    "colour": "#4f46e5",
    "glyph": "<path d=\"M12 7c-2-1.6-4.6-2-8-1.6v12.2c3.4-.4 6 0 8 1.6 2-1.6 4.6-2 8-1.6V5.4C16.6 5 14 5.4 12 7Z\" fill=\"#fff\"/><path d=\"M12 7v12.2\" stroke=\"#4f46e5\" stroke-width=\"1.2\"/><path d=\"M6.2 9.4c1.4-.1 2.6.1 3.6.6M6.2 12.4c1.4-.1 2.6.1 3.6.6M14.2 10c1-.5 2.2-.7 3.6-.6\" stroke=\"#4f46e5\" stroke-width=\"1.1\" stroke-linecap=\"round\"/>"
  },
  "portal": {
    "name": "Shop",
    "colour": "#f43f5e",
    "glyph": "<path d=\"M5.5 9h13l-1 10.5a1.6 1.6 0 0 1-1.6 1.5H8.1a1.6 1.6 0 0 1-1.6-1.5Z\" fill=\"#fff\"/><path d=\"M9 9V7.5a3 3 0 0 1 6 0V9\" fill=\"none\" stroke=\"#fff\" stroke-width=\"1.9\" stroke-linecap=\"round\"/><circle cx=\"9.3\" cy=\"12.2\" r=\"1\" fill=\"#f43f5e\"/><circle cx=\"14.7\" cy=\"12.2\" r=\"1\" fill=\"#f43f5e\"/>"
  }
} /* /JSON */;

// moduleSymbol is the markup of a module's symbol: its colour as a rounded tile with
// the white glyph on it. Decorative wherever it is used — the module's name stands
// beside it — so it is hidden from assistive technology. An unknown id draws nothing.
export function moduleSymbol(id, size = 20) {
  const m = MODULE_ICONS[id];
  if (!m) return "";
  return `<svg class="module-symbol" viewBox="0 0 24 24" width="${size}" height="${size}" aria-hidden="true" focusable="false">` +
    `<rect width="24" height="24" rx="6.5" fill="${m.colour}"/>` +
    `<g transform="translate(3 3) scale(.75)">${m.glyph}</g></svg>`;
}
