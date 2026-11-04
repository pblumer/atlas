// Product icons for the shop's tile layout (ADR-0525).
//
// Drawn from Tabler Icons 3.19.0 (outline), MIT licence, Copyright (c) 2020-2024
// Pawel Kuna; see THIRD_PARTY_NOTICES.md. Each entry is the list of path outlines of
// the icon's 24-unit SVG, stroked in currentColor, so a tile paints it in its own
// colour. The keys are the names a product's `icon` field may carry; the server
// holds the same list (api/catalog/icons.go) and a test keeps the two equal.
//
// Data and not markup, on purpose: the shop never parses markup into the page
// (TestPortalMarkCascadeStopsAtTheOperator), so an icon is built element by element
// and a path's outline can only ever be an attribute value.
export const PRODUCT_ICONS = {
  "user-cog": ["M8 7a4 4 0 1 0 8 0a4 4 0 0 0 -8 0", "M6 21v-2a4 4 0 0 1 4 -4h2.5", "M19.001 19m-2 0a2 2 0 1 0 4 0a2 2 0 1 0 -4 0", "M19.001 15.5v1.5", "M19.001 21v1.5", "M22.032 17.25l-1.299 .75", "M17.27 20l-1.3 .75", "M15.97 17.25l1.3 .75", "M20.733 20l1.3 .75"],
  "users-group": ["M10 13a2 2 0 1 0 4 0a2 2 0 0 0 -4 0", "M8 21v-1a2 2 0 0 1 2 -2h4a2 2 0 0 1 2 2v1", "M15 5a2 2 0 1 0 4 0a2 2 0 0 0 -4 0", "M17 10h2a2 2 0 0 1 2 2v1", "M5 5a2 2 0 1 0 4 0a2 2 0 0 0 -4 0", "M3 13v-1a2 2 0 0 1 2 -2h2"],
  "users": ["M9 7m-4 0a4 4 0 1 0 8 0a4 4 0 1 0 -8 0", "M3 21v-2a4 4 0 0 1 4 -4h4a4 4 0 0 1 4 4v2", "M16 3.13a4 4 0 0 1 0 7.75", "M21 21v-2a4 4 0 0 0 -3 -3.85"],
  "user-shield": ["M6 21v-2a4 4 0 0 1 4 -4h2", "M22 16c0 4 -2.5 6 -3.5 6s-3.5 -2 -3.5 -6c1 0 2.5 -.5 3.5 -1.5c1 1 2.5 1.5 3.5 1.5z", "M8 7a4 4 0 1 0 8 0a4 4 0 0 0 -8 0"],
  "user-search": ["M8 7a4 4 0 1 0 8 0a4 4 0 0 0 -8 0", "M6 21v-2a4 4 0 0 1 4 -4h1.5", "M18 18m-3 0a3 3 0 1 0 6 0a3 3 0 1 0 -6 0", "M20.2 20.2l1.8 1.8"],
  "user-circle": ["M12 12m-9 0a9 9 0 1 0 18 0a9 9 0 1 0 -18 0", "M12 10m-3 0a3 3 0 1 0 6 0a3 3 0 1 0 -6 0", "M6.168 18.849a4 4 0 0 1 3.832 -2.849h4a4 4 0 0 1 3.834 2.855"],
  "user-plus": ["M8 7a4 4 0 1 0 8 0a4 4 0 0 0 -8 0", "M16 19h6", "M19 16v6", "M6 21v-2a4 4 0 0 1 4 -4h4"],
  "door-exit": ["M13 12v.01", "M3 21h18", "M5 21v-16a2 2 0 0 1 2 -2h7.5m2.5 10.5v7.5", "M14 7h7m-3 -3l3 3l-3 3"],
  "sitemap": ["M3 15m0 2a2 2 0 0 1 2 -2h2a2 2 0 0 1 2 2v2a2 2 0 0 1 -2 2h-2a2 2 0 0 1 -2 -2z", "M15 15m0 2a2 2 0 0 1 2 -2h2a2 2 0 0 1 2 2v2a2 2 0 0 1 -2 2h-2a2 2 0 0 1 -2 -2z", "M9 3m0 2a2 2 0 0 1 2 -2h2a2 2 0 0 1 2 2v2a2 2 0 0 1 -2 2h-2a2 2 0 0 1 -2 -2z", "M6 15v-1a2 2 0 0 1 2 -2h8a2 2 0 0 1 2 2v1", "M12 9l0 3"],
  "affiliate": ["M5.931 6.936l1.275 4.249m5.607 5.609l4.251 1.275", "M11.683 12.317l5.759 -5.759", "M5.5 5.5m-1.5 0a1.5 1.5 0 1 0 3 0a1.5 1.5 0 1 0 -3 0", "M18.5 5.5m-1.5 0a1.5 1.5 0 1 0 3 0a1.5 1.5 0 1 0 -3 0", "M18.5 18.5m-1.5 0a1.5 1.5 0 1 0 3 0a1.5 1.5 0 1 0 -3 0", "M8.5 15.5m-4.5 0a4.5 4.5 0 1 0 9 0a4.5 4.5 0 1 0 -9 0"],
  "network": ["M6 9a6 6 0 1 0 12 0a6 6 0 0 0 -12 0", "M12 3c1.333 .333 2 2.333 2 6s-.667 5.667 -2 6", "M12 3c-1.333 .333 -2 2.333 -2 6s.667 5.667 2 6", "M6 9h12", "M3 20h7", "M14 20h7", "M10 20a2 2 0 1 0 4 0a2 2 0 0 0 -4 0", "M12 15v3"],
  "server-2": ["M3 4m0 3a3 3 0 0 1 3 -3h12a3 3 0 0 1 3 3v2a3 3 0 0 1 -3 3h-12a3 3 0 0 1 -3 -3z", "M3 12m0 3a3 3 0 0 1 3 -3h12a3 3 0 0 1 3 3v2a3 3 0 0 1 -3 3h-12a3 3 0 0 1 -3 -3z", "M7 8l0 .01", "M7 16l0 .01", "M11 8h6", "M11 16h6"],
  "database": ["M12 6m-8 0a8 3 0 1 0 16 0a8 3 0 1 0 -16 0", "M4 6v6a8 3 0 0 0 16 0v-6", "M4 12v6a8 3 0 0 0 16 0v-6"],
  "fingerprint": ["M18.9 7a8 8 0 0 1 1.1 5v1a6 6 0 0 0 .8 3", "M8 11a4 4 0 0 1 8 0v1a10 10 0 0 0 2 6", "M12 11v2a14 14 0 0 0 2.5 8", "M8 15a18 18 0 0 0 1.8 6", "M4.9 19a22 22 0 0 1 -.9 -7v-1a8 8 0 0 1 12 -6.95"],
  "lock": ["M5 13a2 2 0 0 1 2 -2h10a2 2 0 0 1 2 2v6a2 2 0 0 1 -2 2h-10a2 2 0 0 1 -2 -2v-6z", "M11 16a1 1 0 1 0 2 0a1 1 0 0 0 -2 0", "M8 11v-4a4 4 0 1 1 8 0v4"],
  "folder-cog": ["M12.5 19h-7.5a2 2 0 0 1 -2 -2v-11a2 2 0 0 1 2 -2h4l3 3h7a2 2 0 0 1 2 2v3", "M19.001 19m-2 0a2 2 0 1 0 4 0a2 2 0 1 0 -4 0", "M19.001 15.5v1.5", "M19.001 21v1.5", "M22.032 17.25l-1.299 .75", "M17.27 20l-1.3 .75", "M15.97 17.25l1.3 .75", "M20.733 20l1.3 .75"],
  "device-laptop": ["M3 19l18 0", "M5 6m0 1a1 1 0 0 1 1 -1h12a1 1 0 0 1 1 1v8a1 1 0 0 1 -1 1h-12a1 1 0 0 1 -1 -1z"],
  "device-desktop": ["M3 5a1 1 0 0 1 1 -1h16a1 1 0 0 1 1 1v10a1 1 0 0 1 -1 1h-16a1 1 0 0 1 -1 -1v-10z", "M7 20h10", "M9 16v4", "M15 16v4"],
  "device-mobile": ["M6 5a2 2 0 0 1 2 -2h8a2 2 0 0 1 2 2v14a2 2 0 0 1 -2 2h-8a2 2 0 0 1 -2 -2v-14z", "M11 4h2", "M12 17v.01"],
  "device-sim": ["M6 3h8.5l4.5 4.5v12.5a1 1 0 0 1 -1 1h-12a1 1 0 0 1 -1 -1v-16a1 1 0 0 1 1 -1z", "M9 11h3v6", "M15 17v.01", "M15 14v.01", "M15 11v.01", "M9 14v.01", "M9 17v.01"],
  "devices": ["M13 9a1 1 0 0 1 1 -1h6a1 1 0 0 1 1 1v10a1 1 0 0 1 -1 1h-6a1 1 0 0 1 -1 -1v-10z", "M18 8v-3a1 1 0 0 0 -1 -1h-13a1 1 0 0 0 -1 1v12a1 1 0 0 0 1 1h9", "M16 9h2"],
  "keyboard": ["M2 6m0 2a2 2 0 0 1 2 -2h16a2 2 0 0 1 2 2v8a2 2 0 0 1 -2 2h-16a2 2 0 0 1 -2 -2z", "M6 10l0 .01", "M10 10l0 .01", "M14 10l0 .01", "M18 10l0 .01", "M6 14l0 .01", "M18 14l0 .01", "M10 14l4 .01"],
  "printer": ["M17 17h2a2 2 0 0 0 2 -2v-4a2 2 0 0 0 -2 -2h-14a2 2 0 0 0 -2 2v4a2 2 0 0 0 2 2h2", "M17 9v-4a2 2 0 0 0 -2 -2h-6a2 2 0 0 0 -2 2v4", "M7 13m0 2a2 2 0 0 1 2 -2h6a2 2 0 0 1 2 2v4a2 2 0 0 1 -2 2h-6a2 2 0 0 1 -2 -2z"],
  "headset": ["M4 14v-3a8 8 0 1 1 16 0v3", "M18 19c0 1.657 -2.686 3 -6 3", "M4 14a2 2 0 0 1 2 -2h1a2 2 0 0 1 2 2v3a2 2 0 0 1 -2 2h-1a2 2 0 0 1 -2 -2v-3z", "M15 14a2 2 0 0 1 2 -2h1a2 2 0 0 1 2 2v3a2 2 0 0 1 -2 2h-1a2 2 0 0 1 -2 -2v-3z"],
  "mail-cog": ["M12 19h-7a2 2 0 0 1 -2 -2v-10a2 2 0 0 1 2 -2h14a2 2 0 0 1 2 2v5", "M3 7l9 6l9 -6", "M19.001 19m-2 0a2 2 0 1 0 4 0a2 2 0 1 0 -4 0", "M19.001 15.5v1.5", "M19.001 21v1.5", "M22.032 17.25l-1.299 .75", "M17.27 20l-1.3 .75", "M15.97 17.25l1.3 .75", "M20.733 20l1.3 .75"],
  "mail-forward": ["M12 18h-7a2 2 0 0 1 -2 -2v-10a2 2 0 0 1 2 -2h14a2 2 0 0 1 2 2v7.5", "M3 6l9 6l9 -6", "M15 18h6", "M18 15l3 3l-3 3"],
  "message-2": ["M8 9h8", "M8 13h6", "M9 18h-3a3 3 0 0 1 -3 -3v-8a3 3 0 0 1 3 -3h12a3 3 0 0 1 3 3v8a3 3 0 0 1 -3 3h-3l-3 3l-3 -3z"],
  "id-badge-2": ["M7 12h3v4h-3z", "M10 6h-6a1 1 0 0 0 -1 1v12a1 1 0 0 0 1 1h16a1 1 0 0 0 1 -1v-12a1 1 0 0 0 -1 -1h-6", "M10 3m0 1a1 1 0 0 1 1 -1h2a1 1 0 0 1 1 1v3a1 1 0 0 1 -1 1h-2a1 1 0 0 1 -1 -1z", "M14 16h2", "M14 12h4"],
  "shopping-cart-plus": ["M4 19a2 2 0 1 0 4 0a2 2 0 0 0 -4 0", "M12.5 17h-6.5v-14h-2", "M6 5l14 1l-.86 6.017m-2.64 .983h-10.5", "M16 19h6", "M19 16v6"],
  "disc": ["M12 12m-9 0a9 9 0 1 0 18 0a9 9 0 1 0 -18 0", "M12 12m-1 0a1 1 0 1 0 2 0a1 1 0 1 0 -2 0", "M7 12a5 5 0 0 1 5 -5", "M12 17a5 5 0 0 0 5 -5"],
  "list-check": ["M3.5 5.5l1.5 1.5l2.5 -2.5", "M3.5 11.5l1.5 1.5l2.5 -2.5", "M3.5 17.5l1.5 1.5l2.5 -2.5", "M11 6l9 0", "M11 12l9 0", "M11 18l9 0"],
  "presentation-analytics": ["M9 12v-4", "M15 12v-2", "M12 12v-1", "M3 4h18", "M4 4v10a2 2 0 0 0 2 2h12a2 2 0 0 0 2 -2v-10", "M12 16v4", "M9 20h6"],
  "settings-cog": ["M12.003 21c-.732 .001 -1.465 -.438 -1.678 -1.317a1.724 1.724 0 0 0 -2.573 -1.066c-1.543 .94 -3.31 -.826 -2.37 -2.37a1.724 1.724 0 0 0 -1.065 -2.572c-1.756 -.426 -1.756 -2.924 0 -3.35a1.724 1.724 0 0 0 1.066 -2.573c-.94 -1.543 .826 -3.31 2.37 -2.37c1 .608 2.296 .07 2.572 -1.065c.426 -1.756 2.924 -1.756 3.35 0a1.724 1.724 0 0 0 2.573 1.066c1.543 -.94 3.31 .826 2.37 2.37a1.724 1.724 0 0 0 1.065 2.572c.886 .215 1.325 .957 1.318 1.694", "M9 12a3 3 0 1 0 6 0a3 3 0 0 0 -6 0", "M19.001 19m-2 0a2 2 0 1 0 4 0a2 2 0 1 0 -4 0", "M19.001 15.5v1.5", "M19.001 21v1.5", "M22.032 17.25l-1.299 .75", "M17.27 20l-1.3 .75", "M15.97 17.25l1.3 .75", "M20.733 20l1.3 .75"],
  "chevrons-right": ["M7 7l5 5l-5 5", "M13 7l5 5l-5 5"],
  "file-text": ["M14 3v4a1 1 0 0 0 1 1h4", "M17 21h-10a2 2 0 0 1 -2 -2v-14a2 2 0 0 1 2 -2h7l5 5v11a2 2 0 0 1 -2 2z", "M9 9l1 0", "M9 13l6 0", "M9 17l6 0"],
  "package": ["M12 3l8 4.5l0 9l-8 4.5l-8 -4.5l0 -9l8 -4.5", "M12 12l8 -4.5", "M12 12l0 9", "M12 12l-8 -4.5", "M16 5.25l-8 4.5"],
};

const SVG = 'http://www.w3.org/2000/svg';

// drawIcon builds the named icon as an <svg> element, or the parcel for a name the
// set does not hold. Built with createElementNS, so nothing is parsed. Its size is
// the caller's CSS; the stroke width is the caller's choice of weight.
export function drawIcon(name, strokeWidth = 2) {
  const svg = document.createElementNS(SVG, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', String(strokeWidth));
  svg.setAttribute('stroke-linecap', 'round');
  svg.setAttribute('stroke-linejoin', 'round');
  svg.setAttribute('aria-hidden', 'true');
  for (const d of PRODUCT_ICONS[name] || PRODUCT_ICONS.package) {
    const path = document.createElementNS(SVG, 'path');
    path.setAttribute('d', d);
    svg.appendChild(path);
  }
  return svg;
}
