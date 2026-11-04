package catalog

import "slices"

// Icons are the names a product's Icon may carry: the set the shop's tile layout
// draws (api/web/shop-icons.js, from Tabler Icons, MIT). A name outside it would
// draw nothing, so publishing refuses it (ADR-draft-shop-tile-layout).
var Icons = []string{
	"user-cog",
	"users-group",
	"users",
	"user-shield",
	"user-search",
	"user-circle",
	"user-plus",
	"door-exit",
	"sitemap",
	"affiliate",
	"network",
	"server-2",
	"database",
	"fingerprint",
	"lock",
	"folder-cog",
	"device-laptop",
	"device-desktop",
	"device-mobile",
	"device-sim",
	"devices",
	"keyboard",
	"printer",
	"headset",
	"mail-cog",
	"mail-forward",
	"message-2",
	"id-badge-2",
	"shopping-cart-plus",
	"disc",
	"list-check",
	"presentation-analytics",
	"settings-cog",
	"chevrons-right",
	"file-text",
	"package",
}

// checkIcon refuses an icon name the shop does not draw. Whitespace is not a
// name either: it is what a cleared box leaves, and it would draw nothing while
// claiming to have chosen something.
func checkIcon(it Item, add func(Problem)) {
	if it.Icon == "" || slices.Contains(Icons, it.Icon) {
		return
	}
	add(Problem{Item: it.ID, Message: "names icon " + quote(it.Icon) +
		", which the shop does not draw; choose one of the shop's icon names or leave it out"})
}
