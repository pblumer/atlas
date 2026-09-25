// The self-service shop (ADR-0312).
//
// A module, so it can reuse theme.js's palette derivation rather than repeat it.
// That reuse is the point: --accent-ink decides whether a button's label is
// readable on whatever colour somebody picked, and a second implementation of it
// is a second place for that to be wrong.
//
// One page with two halves: the catalogue somebody is the audience for, and the
// orders they placed. It reads five endpoints and holds no state the server does
// not already have, which is why it can be reloaded at any point without losing
// anything.
//
// Language follows the browser (ADR-0313),
// on the condition that record names: every string below exists in every locale
// the page offers, held by TestPortalCatalogueIsComplete. A missing key would
// otherwise reach a customer, who cannot act on a review signal.

import { applyAccent } from './theme.js';

const STRINGS = {
  de: {
    'portal.title': 'Shop',
    'portal.catalog': 'Katalog',
    'portal.orders': 'Meine Bestellungen',
    'portal.none': 'Ihnen ist kein Katalog zugeordnet.',
    'portal.none.hint': 'Wenden Sie sich an die Stelle, die Ihren Zugang eingerichtet hat.',
    'portal.empty': 'Dieser Katalog enthält zurzeit nichts Bestellbares.',
    'portal.includes': 'Enthalten',
    'portal.options': 'Zusätzlich wählbar',
    'portal.order': 'Bestellen',
    'portal.ordering': 'Wird bestellt …',
    'portal.noOrders': 'Sie haben noch nichts bestellt.',
    'portal.placed': 'Bestellt am',
    'portal.approval': 'Genehmigung nötig',
    'portal.back': 'Zurück zu Atlas',
    'portal.held': 'Haben Sie bereits',
    'portal.held.since': 'seit',
    'portal.blockedBy': 'Wartet auf',
    'portal.reason': 'Begründung',
    'portal.retry': 'Erneut versuchen',
    'portal.failed': 'Das hat nicht geklappt.',
    'portal.cancel': 'Stornieren',
    'portal.cancelling': 'Wird storniert …',
    'order.cancelled': 'Storniert',
    'status.cancelled': 'Storniert',
    'portal.return': 'Zurückgeben',
    'portal.returning': 'Wird zurückgegeben …',
    'status.returning': 'Wird zurückgegeben',
    'status.returned': 'Zurückgegeben',
    'portal.return.sure': 'Diese Leistung wirklich zurückgeben? Der Zugang wird entzogen.',
    'status.returnFailed': 'Rücknahme gescheitert',
    'status.pending': 'Wartet',
    'status.running': 'Läuft',
    'status.done': 'Erledigt',
    'status.skipped': 'Bereits vorhanden',
    'status.failed': 'Störung',
    'status.rejected': 'Abgelehnt',
    'status.abandoned': 'Aufgegeben',
    'status.blocked': 'Blockiert',
    'order.running': 'In Arbeit',
    'proc.open': 'Prozess ansehen',
    'proc.none.order': 'Zu diesem Auftrag ist keine laufende Prozessinstanz zu finden: Entweder wurde noch keine gestartet, sie ist bereits beendet, oder die Aufbewahrung hat sie entfernt.',
    'proc.archived': 'Der Prozess zu diesem Auftrag steht nur noch im ausgelagerten Ereignisprotokoll. Dieser Server hat ihn nicht mehr und kann ihn nicht anzeigen.',
    'proc.asking': 'Wird abgefragt …',
    'proc.slow': 'Der Server hat auf die Suche nach dem Prozess nicht rechtzeitig geantwortet. Der Auftrag selbst ist davon nicht betroffen; bitte später erneut versuchen.',
    'task.by.fixed': 'Genehmigung durch',
    'task.by.role': 'Genehmigung durch die Gruppe',
    'task.by.superior': 'Genehmigung durch die vorgesetzte Person',
    'task.waits.person': 'wartet auf',
    'task.waits.group': 'wartet auf die Gruppe',
    'task.open': 'von allen übernehmbar',
    'task.due': 'fällig am',
    'task.work': 'Erledigen',
    'task.complete': 'Abschliessen',
    'task.completing': 'Wird abgeschlossen …',
    'task.noForm': 'Diese Aufgabe fragt nichts ab. Abschliessen meldet sie als erledigt.',
    'task.formFailed': 'Das Formular dieser Aufgabe konnte nicht geladen werden.',
    'task.truncated': 'Nicht alle offenen Aufgaben konnten gelesen werden. Aufträge, in denen Sie eine Aufgabe halten, fehlen hier möglicherweise.',
    'task.held': 'zur Bearbeitung',
    'step.back': 'Zurück',
    'step.to': 'Weiter zu',
    'order.completed': 'Abgeschlossen',
    'order.partial': 'Teilweise erfüllt',
    'order.unfulfilled': 'Nicht erfüllt',
    'nav.catalog': 'Katalog',
    'nav.orders': 'Meine Aufträge',
    'nav.services': 'Meine Leistungen',
    'nav.help': 'Hilfe',
    'col.category': 'Kategorie',
    'col.group': 'Produktgruppe',
    'group.all': 'Alle Gruppen',
    'group.none': 'Ohne Gruppe',
    'col.offering': 'Marktleistung',
    'col.options': 'Optional',
    'col.service': 'Service',
    'act.back': '< zurück',
    'act.discard': 'Auftrag löschen',
    'act.toBasket': 'In den Warenkorb >',
    'act.place': 'bestellen >',
    'act.cancelLines': 'Leistung(en) kündigen',
    'act.changeLine': 'Leistung mutieren',
    'basket.title': 'Warenkorb',
    'basket.empty': 'Der Warenkorb ist leer.',
    'basket.count': 'Im Warenkorb',
    'for.order': 'Bestellen für:',
    'for.approve': 'Genehmigen für:',
    'for.search': 'Person suchen',
    'for.self': 'mich selbst',
    'for.hits': 'Personen',
    'for.none': 'Niemand mit diesem Namen. Kennung, Benutzername oder Mailadresse geht auch.',
    'for.clear': 'Wieder für mich selbst bestellen',
    'cfg.title': 'Angaben zu dieser Leistung',
    'cfg.loading': 'Formular wird geladen …',
    'cfg.failed': 'Dieses Formular lässt sich nicht laden. Bestellen ist weiterhin möglich; die Angaben fehlen dann.',
    'cfg.invalid': 'Einige Angaben sind noch nicht vollständig. Bitte korrigieren Sie sie vor dem Bestellen.',
    'line.withdraw': 'Position zurückziehen',
    'line.withdrawing': 'Wird zurückgezogen …',
    'line.details': 'Angaben ändern',
    'line.save': 'Angaben speichern',
    'line.saving': 'Wird gespeichert …',
    'line.close': 'Abbrechen',
    'line.amended': 'Korrigiert',
    'line.amendedFrom': 'vorher',
    'info.price': 'Kosten',
    'price.none': 'Der Katalog nennt keine Kosten.',
    'cat.none': 'Ohne Kategorie',
    // The single-user mode: the catalogue is readable and an order is not
    // possible, because an order belongs to somebody.
    'noid.title': 'Bestellen ist ohne Anmeldung nicht möglich.',
    'noid.hint': 'Eine Bestellung gehört jemandem. Ohne Anmeldung gibt es niemanden, dem sie gehört, und niemanden, der benachrichtigt wird. Der Katalog steht hier zum Ansehen; für eine Bestellung startet man den Server mit --auth.',
    'cat.all': 'Alle',
    'tbl.company': 'Unternehmen',
    'tbl.person': 'Person',
    'tbl.placed': 'bestellt',
    'tbl.order': 'Auftrag',
    'tbl.status': 'Status',
    'tbl.searchCompany': 'Unternehmen suchen',
    'tbl.searchPerson': 'Person suchen',
    'tbl.searchDate': 'Datum',
    'tbl.searchOrder': 'Auftrag suchen',
    'tbl.searchStatus': 'Status suchen',
    'tbl.noMatch': 'Kein Auftrag entspricht der Suche.',
    'note.noCompany': 'Die Spalte Unternehmen bleibt leer: ein Auftrag trägt heute keine Organisation. Er nennt nur, wer bestellt und wer empfängt.',
    'note.included': 'Fest enthalten — nicht abwählbar.',
    'window.later': 'Bestellbar ab',
    'window.over': 'Nicht mehr bestellbar seit',
    'window.until': 'Bestellbar bis',
    'variant.label': 'Ausführung',
    'variant.many': 'Mehrere Ausführungen möglich — jede angekreuzte ist eine eigene Position.',
    'variant.one': 'Genau eine Ausführung wählen.',
    'variant.missing': 'Bitte wählen Sie zuerst für jede Position eine Ausführung.',
    'info.title': 'Angaben zum Service',
    'info.id': 'Kennung',
    'info.approval': 'Genehmigung',
    'info.none': 'keine',
    'info.repeatable': 'Mehrfach beziehbar',
    'info.includes': 'Fest enthalten',
    'info.options': 'Optional wählbar',
    'info.yes': 'ja',
    'info.no': 'nein',
    'services.none': 'Sie beziehen zurzeit keine Leistungen.',
    'fav.mark': 'Als Favorit merken',
    'fav.clear': 'Favorit entfernen',
    'fav.only': 'Nur Favoriten',
    'fav.none': 'Sie haben nichts als Favorit gemerkt.',
    'fav.unresolved': 'Favoriten, die dieser Katalog nicht führt',
    'fav.full': 'Mehr Favoriten als ein Konto führen darf. Entfernen Sie einen, bevor Sie einen weiteren merken.',
    'find.label': 'Leistung suchen',
    'find.hint': 'Name, Abkürzung oder wofür Sie es brauchen',
    'find.none': 'Keine Leistung entspricht der Suche.',
    'find.hits': 'Treffer',
    'find.clear': 'Suche zurücksetzen',
    'find.where': 'in',
    // The sign-in, for a shop that enforces one. Every string here is read by
    // somebody who is not an operator and has no server log to consult, so each
    // one names what to do next rather than what went wrong.
    'signin.title': 'Bitte melden Sie sich an',
    'signin.hint': 'Dieser Shop zeigt Ihnen den Katalog, der Ihnen zugeordnet ist, und Ihre eigenen Aufträge. Dazu muss er wissen, wer Sie sind.',
    'signin.user': 'Benutzername',
    'signin.password': 'Passwort',
    'signin.submit': 'Anmelden',
    'signin.busy': 'Wird angemeldet …',
    'signin.wrong': 'Benutzername oder Passwort stimmt nicht.',
    'signin.throttled': 'Zu viele Versuche — das Passwort wurde gar nicht geprüft. Warten Sie einige Minuten und versuchen Sie es dann erneut.',
    'signin.failed': 'Die Anmeldung liess sich nicht abschliessen. Versuchen Sie es erneut, oder wenden Sie sich an die Stelle, die Ihren Zugang eingerichtet hat.',
    'signin.expired': 'Ihre Anmeldung ist abgelaufen. Bitte melden Sie sich erneut an.',
    'signin.sso': 'Anmelden mit',
    'signin.ssoFailed': 'Die Anmeldung über Ihren Identitätsanbieter hat nicht geklappt. Versuchen Sie es erneut, oder wenden Sie sich an die Stelle, die Ihren Zugang eingerichtet hat.',
    'signin.or': 'oder mit Benutzername und Passwort',
    'signin.register': 'Noch kein Konto?',
    'signin.registerLink': 'Registrieren',
  },
  en: {
    'portal.title': 'Shop',
    'portal.catalog': 'Catalogue',
    'portal.orders': 'My orders',
    'portal.none': 'No catalogue is assigned to you.',
    'portal.none.hint': 'Ask whoever set up your access.',
    'portal.empty': 'This catalogue currently offers nothing.',
    'portal.includes': 'Included',
    'portal.options': 'Also available',
    'portal.order': 'Order',
    'portal.ordering': 'Ordering …',
    'portal.noOrders': 'You have not ordered anything yet.',
    'portal.placed': 'Ordered on',
    'portal.approval': 'Needs approval',
    'portal.back': 'Back to Atlas',
    'portal.held': 'You already have this',
    'portal.held.since': 'since',
    'portal.blockedBy': 'Waiting for',
    'portal.reason': 'Reason',
    'portal.retry': 'Try again',
    'portal.failed': 'That did not work.',
    'portal.cancel': 'Cancel',
    'portal.cancelling': 'Cancelling …',
    'order.cancelled': 'Cancelled',
    'status.cancelled': 'Cancelled',
    'portal.return': 'Return',
    'portal.returning': 'Returning …',
    'status.returning': 'Being returned',
    'status.returned': 'Returned',
    'portal.return.sure': 'Really give this back? The access will be revoked.',
    'status.returnFailed': 'Return failed',
    'status.pending': 'Waiting',
    'status.running': 'In progress',
    'status.done': 'Done',
    'status.skipped': 'Already held',
    'status.failed': 'Failed',
    'status.rejected': 'Refused',
    'status.abandoned': 'Given up on',
    'status.blocked': 'Blocked',
    'order.running': 'In progress',
    'proc.open': 'View the process',
    'proc.none.order': 'No running process instance was found for this order: either none has started yet, it has already finished, or retention has removed it.',
    'proc.archived': 'This order\'s process is only in the exported event log now. This server no longer holds it and cannot show it.',
    'proc.asking': 'Asking …',
    'proc.slow': 'The server did not answer the search for the process in time. The order itself is not affected; please try again later.',
    'task.by.fixed': 'Approval by',
    'task.by.role': 'Approval by the group',
    'task.by.superior': 'Approval by the line manager',
    'task.waits.person': 'waiting for',
    'task.waits.group': 'waiting for the group',
    'task.open': 'open to anyone',
    'task.due': 'due',
    'task.work': 'Work on it',
    'task.complete': 'Complete',
    'task.completing': 'Completing …',
    'task.noForm': 'This task asks for nothing. Completing it reports it as done.',
    'task.formFailed': 'The form of this task could not be loaded.',
    'task.truncated': 'Not every open task could be read. Orders in which you hold a task may be missing here.',
    'task.held': 'for you to handle',
    'step.back': 'Back',
    'step.to': 'On to',
    'order.completed': 'Completed',
    'order.partial': 'Partly fulfilled',
    'order.unfulfilled': 'Not fulfilled',
    'nav.catalog': 'Catalogue',
    'nav.orders': 'My orders',
    'nav.services': 'My services',
    'nav.help': 'Help',
    'col.category': 'Category',
    'col.group': 'Product group',
    'group.all': 'All groups',
    'group.none': 'No group',
    'col.offering': 'Offering',
    'col.options': 'Optional',
    'col.service': 'Service',
    'act.back': '< back',
    'act.discard': 'Discard order',
    'act.toBasket': 'Add to basket >',
    'act.place': 'order >',
    'act.cancelLines': 'Cancel service(s)',
    'act.changeLine': 'Change service',
    'basket.title': 'Basket',
    'basket.empty': 'The basket is empty.',
    'basket.count': 'In the basket',
    'for.order': 'Order for:',
    'for.approve': 'Approve for:',
    'for.search': 'Find a person',
    'for.self': 'myself',
    'for.hits': 'people',
    'for.none': 'Nobody by that name. An id, username or mail address works too.',
    'for.clear': 'Order for myself again',
    'cfg.title': 'Details for this service',
    'cfg.loading': 'Loading the form …',
    'cfg.failed': 'This form cannot be loaded. Ordering still works; the details will be missing.',
    'cfg.invalid': 'Some details are not complete yet. Please correct them before ordering.',
    'line.withdraw': 'Withdraw this position',
    'line.withdrawing': 'Withdrawing …',
    'line.details': 'Change the details',
    'line.save': 'Save the details',
    'line.saving': 'Saving …',
    'line.close': 'Cancel',
    'line.amended': 'Corrected',
    'line.amendedFrom': 'was',
    'info.price': 'Cost',
    'price.none': 'The catalogue names no cost.',
    'cat.none': 'Without a category',
    'noid.title': 'Ordering needs somebody to be.',
    'noid.hint': 'An order belongs to somebody. Without a sign-in there is nobody it belongs to and nobody to notify. The catalogue is here to be looked at; start the server with --auth to order from it.',
    'cat.all': 'All',
    'tbl.company': 'Organisation',
    'tbl.person': 'Person',
    'tbl.placed': 'ordered',
    'tbl.order': 'Order',
    'tbl.status': 'Status',
    'tbl.searchCompany': 'Search organisation',
    'tbl.searchPerson': 'Search person',
    'tbl.searchDate': 'Date',
    'tbl.searchOrder': 'Search order',
    'tbl.searchStatus': 'Search status',
    'tbl.noMatch': 'No order matches the search.',
    'note.noCompany': 'The organisation column stays empty: an order carries no organisation today. It names only who ordered and who receives.',
    'note.included': 'Always included — cannot be deselected.',
    'window.later': 'Orderable from',
    'window.over': 'No longer orderable since',
    'window.until': 'Orderable until',
    'variant.label': 'Version',
    'variant.many': 'More than one version may be taken — each tick is its own position.',
    'variant.one': 'Choose exactly one version.',
    'variant.missing': 'Choose a version for every line before ordering.',
    'info.title': 'About this service',
    'info.id': 'Identifier',
    'info.approval': 'Approval',
    'info.none': 'none',
    'info.repeatable': 'May be held more than once',
    'info.includes': 'Always included',
    'info.options': 'Optional',
    'info.yes': 'yes',
    'info.no': 'no',
    'services.none': 'You currently hold no services.',
    'fav.mark': 'Mark as favourite',
    'fav.clear': 'Remove favourite',
    'fav.only': 'Favourites only',
    'fav.none': 'You have marked nothing as a favourite.',
    'fav.unresolved': 'Favourites this catalogue does not carry',
    'fav.full': 'That is more favourites than one account may keep. Remove one before marking another.',
    'find.label': 'Find a service',
    'find.hint': 'Name, abbreviation, or what you need it for',
    'find.none': 'No service matches the search.',
    'find.hits': 'matches',
    'find.clear': 'Clear search',
    'find.where': 'in',
    'signin.title': 'Please sign in',
    'signin.hint': 'This shop shows you the catalogue assigned to you, and your own orders. To do that it has to know who you are.',
    'signin.user': 'Username',
    'signin.password': 'Password',
    'signin.submit': 'Sign in',
    'signin.busy': 'Signing in …',
    'signin.wrong': 'That username or password is not right.',
    'signin.throttled': 'Too many attempts — the password was not checked at all. Wait a few minutes and try again.',
    'signin.failed': 'The sign-in could not be completed. Try again, or ask whoever set up your access.',
    'signin.expired': 'Your session has run out. Please sign in again.',
    'signin.sso': 'Sign in with',
    'signin.ssoFailed': 'Signing in with your identity provider did not work. Try again, or ask whoever set up your access.',
    'signin.or': 'or sign in with a username and password',
    'signin.register': 'No account yet?',
    'signin.registerLink': 'Register',
  },
  // Français, pour les catalogues tenus en français. Vouvoiement partout, comme
  // dans l’allemand : le shop s’adresse à une personne qui commande pour son
  // travail, pas à un compte.
  fr: {
    'portal.title': 'Shop',
    'portal.catalog': 'Catalogue',
    'portal.orders': 'Mes commandes',
    'portal.none': 'Aucun catalogue ne vous est attribué.',
    'portal.none.hint': 'Adressez-vous au service qui a créé votre accès.',
    'portal.empty': 'Ce catalogue ne propose actuellement rien.',
    'portal.includes': 'Inclus',
    'portal.options': 'Également disponible',
    'portal.order': 'Commander',
    'portal.ordering': 'Commande en cours …',
    'portal.noOrders': 'Vous n’avez encore rien commandé.',
    'portal.placed': 'Commandé le',
    'portal.approval': 'Approbation nécessaire',
    'portal.back': 'Retour à Atlas',
    'portal.held': 'Vous avez déjà',
    'portal.held.since': 'depuis',
    'portal.blockedBy': 'En attente de',
    'portal.reason': 'Motif',
    'portal.retry': 'Réessayer',
    'portal.failed': 'Cela n’a pas fonctionné.',
    'portal.cancel': 'Annuler',
    'portal.cancelling': 'Annulation en cours …',
    'order.cancelled': 'Annulée',
    'status.cancelled': 'Annulée',
    'portal.return': 'Restituer',
    'portal.returning': 'Restitution en cours …',
    'status.returning': 'Restitution en cours',
    'status.returned': 'Restituée',
    'portal.return.sure': 'Restituer réellement cette prestation ? L’accès sera retiré.',
    'status.returnFailed': 'Échec de la restitution',
    'status.pending': 'En attente',
    'status.running': 'En cours',
    'status.done': 'Terminé',
    'status.skipped': 'Déjà attribué',
    'status.failed': 'Incident',
    'status.rejected': 'Refusé',
    'status.abandoned': 'Abandonné',
    'status.blocked': 'Bloqué',
    'order.running': 'En traitement',
    'proc.open': 'Voir le processus',
    'proc.none.order': 'Aucune instance de processus en cours ne correspond à cette commande : soit aucune n’a encore été lancée, soit elle est déjà terminée, soit la conservation l’a supprimée.',
    'proc.archived': 'Le processus de cette commande ne figure plus que dans le journal d’événements externalisé. Ce serveur ne le possède plus et ne peut pas l’afficher.',
    'proc.asking': 'Interrogation en cours …',
    'proc.slow': 'Le serveur n’a pas répondu à temps à la recherche du processus. La commande elle-même n’est pas concernée ; veuillez réessayer plus tard.',
    'task.by.fixed': 'Approbation par',
    'task.by.role': 'Approbation par le groupe',
    'task.by.superior': 'Approbation par le supérieur hiérarchique',
    'task.waits.person': 'en attente de',
    'task.waits.group': 'en attente du groupe',
    'task.open': 'ouverte à tous',
    'task.due': 'échéance',
    'task.work': 'Traiter',
    'task.complete': 'Terminer',
    'task.completing': 'Fin en cours …',
    'task.noForm': 'Cette tâche ne demande rien. La terminer la signale comme accomplie.',
    'task.formFailed': 'Le formulaire de cette tâche n’a pas pu être chargé.',
    'task.truncated': 'Toutes les tâches ouvertes n’ont pas pu être lues. Des commandes dans lesquelles vous détenez une tâche peuvent manquer ici.',
    'task.held': 'à traiter',
    'step.back': 'Retour',
    'step.to': 'Vers',
    'order.completed': 'Terminée',
    'order.partial': 'Partiellement exécutée',
    'order.unfulfilled': 'Non exécutée',
    'nav.catalog': 'Catalogue',
    'nav.orders': 'Mes commandes',
    'nav.services': 'Mes prestations',
    'nav.help': 'Aide',
    'col.category': 'Catégorie',
    'col.group': 'Groupe de produits',
    'group.all': 'Tous les groupes',
    'group.none': 'Sans groupe',
    'col.offering': 'Prestation',
    'col.options': 'En option',
    'col.service': 'Service',
    'act.back': '< retour',
    'act.discard': 'Supprimer la commande',
    'act.toBasket': 'Ajouter au panier >',
    'act.place': 'commander >',
    'act.cancelLines': 'Résilier la ou les prestations',
    'act.changeLine': 'Modifier la prestation',
    'basket.title': 'Panier',
    'basket.empty': 'Le panier est vide.',
    'basket.count': 'Dans le panier',
    'for.order': 'Commander pour :',
    'for.approve': 'Approuver pour :',
    'for.search': 'Rechercher une personne',
    'for.self': 'moi-même',
    'for.hits': 'personnes',
    'for.none': 'Personne de ce nom. Un identifiant, un nom d’utilisateur ou une adresse électronique fonctionne aussi.',
    'for.clear': 'Commander à nouveau pour moi-même',
    'cfg.title': 'Informations sur cette prestation',
    'cfg.loading': 'Chargement du formulaire …',
    'cfg.failed': 'Ce formulaire ne peut pas être chargé. La commande reste possible ; les informations manqueront.',
    'cfg.invalid': 'Certaines informations sont incomplètes. Veuillez les corriger avant de commander.',
    'line.withdraw': 'Retirer cette position',
    'line.withdrawing': 'Retrait en cours …',
    'line.details': 'Modifier les informations',
    'line.save': 'Enregistrer les informations',
    'line.saving': 'Enregistrement en cours …',
    'line.close': 'Annuler',
    'line.amended': 'Corrigé',
    'line.amendedFrom': 'auparavant',
    'info.price': 'Coût',
    'price.none': 'Le catalogue n’indique aucun coût.',
    'cat.none': 'Sans catégorie',
    'noid.title': 'Commander exige une identité.',
    'noid.hint': 'Une commande appartient à quelqu’un. Sans connexion, il n’y a personne à qui elle appartienne ni personne à informer. Le catalogue est ici pour être consulté ; pour commander, démarrez le serveur avec --auth.',
    'cat.all': 'Toutes',
    'tbl.company': 'Organisation',
    'tbl.person': 'Personne',
    'tbl.placed': 'commandé',
    'tbl.order': 'Commande',
    'tbl.status': 'Statut',
    'tbl.searchCompany': 'Rechercher une organisation',
    'tbl.searchPerson': 'Rechercher une personne',
    'tbl.searchDate': 'Date',
    'tbl.searchOrder': 'Rechercher une commande',
    'tbl.searchStatus': 'Rechercher un statut',
    'tbl.noMatch': 'Aucune commande ne correspond à la recherche.',
    'note.noCompany': 'La colonne Organisation reste vide : une commande ne porte aujourd’hui aucune organisation. Elle indique seulement qui commande et qui reçoit.',
    'note.included': 'Toujours inclus — ne peut pas être désélectionné.',
    'window.later': 'Commandable à partir du',
    'window.over': 'Plus commandable depuis le',
    'window.until': 'Commandable jusqu’au',
    'variant.label': 'Variante',
    'variant.many': 'Plusieurs variantes sont possibles — chaque case cochée est une position distincte.',
    'variant.one': 'Choisissez exactement une variante.',
    'variant.missing': 'Choisissez une variante pour chaque position avant de commander.',
    'info.title': 'Informations sur le service',
    'info.id': 'Identifiant',
    'info.approval': 'Approbation',
    'info.none': 'aucune',
    'info.repeatable': 'Peut être détenu plusieurs fois',
    'info.includes': 'Toujours inclus',
    'info.options': 'En option',
    'info.yes': 'oui',
    'info.no': 'non',
    'services.none': 'Vous ne détenez actuellement aucune prestation.',
    'fav.mark': 'Ajouter aux favoris',
    'fav.clear': 'Retirer des favoris',
    'fav.only': 'Favoris uniquement',
    'fav.none': 'Vous n’avez rien mis en favori.',
    'fav.unresolved': 'Favoris que ce catalogue ne propose pas',
    'fav.full': 'Cela dépasse le nombre de favoris qu’un compte peut garder. Retirez-en un avant d’en ajouter un autre.',
    'find.label': 'Rechercher une prestation',
    'find.hint': 'Nom, abréviation ou usage prévu',
    'find.none': 'Aucune prestation ne correspond à la recherche.',
    'find.hits': 'résultats',
    'find.clear': 'Réinitialiser la recherche',
    'find.where': 'dans',
    'signin.title': 'Veuillez vous connecter',
    'signin.hint': 'Ce shop vous montre le catalogue qui vous est attribué ainsi que vos propres commandes. Pour cela, il doit savoir qui vous êtes.',
    'signin.user': 'Nom d’utilisateur',
    'signin.password': 'Mot de passe',
    'signin.submit': 'Se connecter',
    'signin.busy': 'Connexion en cours …',
    'signin.wrong': 'Ce nom d’utilisateur ou ce mot de passe n’est pas correct.',
    'signin.throttled': 'Trop de tentatives — le mot de passe n’a même pas été vérifié. Patientez quelques minutes, puis réessayez.',
    'signin.failed': 'La connexion n’a pas pu aboutir. Réessayez ou adressez-vous au service qui a créé votre accès.',
    'signin.expired': 'Votre session a expiré. Veuillez vous reconnecter.',
    'signin.sso': 'Se connecter avec',
    'signin.ssoFailed': 'La connexion via votre fournisseur d’identité n’a pas fonctionné. Réessayez ou adressez-vous au service qui a créé votre accès.',
    'signin.or': 'ou avec un nom d’utilisateur et un mot de passe',
    'signin.register': 'Pas encore de compte ?',
    'signin.registerLink': 'S’inscrire',
  },
  // Italiano, per i cataloghi tenuti in italiano. Forma di cortesia ovunque, come
  // nelle altre lingue.
  it: {
    'portal.title': 'Shop',
    'portal.catalog': 'Catalogo',
    'portal.orders': 'I miei ordini',
    'portal.none': 'Non le è assegnato alcun catalogo.',
    'portal.none.hint': 'Si rivolga al servizio che ha creato il suo accesso.',
    'portal.empty': 'Questo catalogo al momento non offre nulla.',
    'portal.includes': 'Incluso',
    'portal.options': 'Disponibile anche',
    'portal.order': 'Ordinare',
    'portal.ordering': 'Ordine in corso …',
    'portal.noOrders': 'Non ha ancora ordinato nulla.',
    'portal.placed': 'Ordinato il',
    'portal.approval': 'Richiede approvazione',
    'portal.back': 'Ritorno ad Atlas',
    'portal.held': 'Dispone già di',
    'portal.held.since': 'dal',
    'portal.blockedBy': 'In attesa di',
    'portal.reason': 'Motivo',
    'portal.retry': 'Riprovare',
    'portal.failed': 'Non ha funzionato.',
    'portal.cancel': 'Annullare',
    'portal.cancelling': 'Annullamento in corso …',
    'order.cancelled': 'Annullato',
    'status.cancelled': 'Annullato',
    'portal.return': 'Restituire',
    'portal.returning': 'Restituzione in corso …',
    'status.returning': 'Restituzione in corso',
    'status.returned': 'Restituito',
    'portal.return.sure': 'Restituire davvero questa prestazione? L’accesso verrà revocato.',
    'status.returnFailed': 'Restituzione non riuscita',
    'status.pending': 'In attesa',
    'status.running': 'In corso',
    'status.done': 'Concluso',
    'status.skipped': 'Già disponibile',
    'status.failed': 'Guasto',
    'status.rejected': 'Rifiutato',
    'status.abandoned': 'Abbandonato',
    'status.blocked': 'Bloccato',
    'order.running': 'In lavorazione',
    'proc.open': 'Visualizzare il processo',
    'proc.none.order': 'Per questo ordine non risulta alcuna istanza di processo in corso: o non ne è ancora stata avviata una, o è già terminata, oppure la conservazione l’ha rimossa.',
    'proc.archived': 'Il processo di questo ordine si trova ormai solo nel registro eventi esternalizzato. Questo server non lo possiede più e non può mostrarlo.',
    'proc.asking': 'Interrogazione in corso …',
    'proc.slow': 'Il server non ha risposto in tempo alla ricerca del processo. L’ordine stesso non ne è interessato; riprovare più tardi.',
    'task.by.fixed': 'Approvazione di',
    'task.by.role': 'Approvazione del gruppo',
    'task.by.superior': 'Approvazione del superiore',
    'task.waits.person': 'in attesa di',
    'task.waits.group': 'in attesa del gruppo',
    'task.open': 'aperta a tutti',
    'task.due': 'scadenza',
    'task.work': 'Gestire',
    'task.complete': 'Completare',
    'task.completing': 'Completamento in corso …',
    'task.noForm': 'Questa attività non chiede nulla. Completarla la segnala come svolta.',
    'task.formFailed': 'Non è stato possibile caricare il modulo di questa attività.',
    'task.truncated': 'Non è stato possibile leggere tutte le attività aperte. Gli ordini in cui lei detiene un’attività potrebbero mancare qui.',
    'task.held': 'da gestire',
    'step.back': 'Indietro',
    'step.to': 'Verso',
    'order.completed': 'Concluso',
    'order.partial': 'Parzialmente evaso',
    'order.unfulfilled': 'Non evaso',
    'nav.catalog': 'Catalogo',
    'nav.orders': 'I miei ordini',
    'nav.services': 'Le mie prestazioni',
    'nav.help': 'Aiuto',
    'col.category': 'Categoria',
    'col.group': 'Gruppo di prodotti',
    'group.all': 'Tutti i gruppi',
    'group.none': 'Senza gruppo',
    'col.offering': 'Prestazione',
    'col.options': 'Opzionale',
    'col.service': 'Servizio',
    'act.back': '< indietro',
    'act.discard': 'Eliminare l’ordine',
    'act.toBasket': 'Aggiungere al carrello >',
    'act.place': 'ordinare >',
    'act.cancelLines': 'Disdire la o le prestazioni',
    'act.changeLine': 'Modificare la prestazione',
    'basket.title': 'Carrello',
    'basket.empty': 'Il carrello è vuoto.',
    'basket.count': 'Nel carrello',
    'for.order': 'Ordinare per:',
    'for.approve': 'Approvare per:',
    'for.search': 'Cercare una persona',
    'for.self': 'me stesso',
    'for.hits': 'persone',
    'for.none': 'Nessuno con questo nome. Funziona anche un identificativo, un nome utente o un indirizzo di posta elettronica.',
    'for.clear': 'Ordinare di nuovo per me stesso',
    'cfg.title': 'Indicazioni su questa prestazione',
    'cfg.loading': 'Caricamento del modulo …',
    'cfg.failed': 'Questo modulo non può essere caricato. È comunque possibile ordinare; le indicazioni mancheranno.',
    'cfg.invalid': 'Alcune indicazioni non sono complete. La preghiamo di correggerle prima di ordinare.',
    'line.withdraw': 'Ritirare questa posizione',
    'line.withdrawing': 'Ritiro in corso …',
    'line.details': 'Modificare le indicazioni',
    'line.save': 'Salvare le indicazioni',
    'line.saving': 'Salvataggio in corso …',
    'line.close': 'Annullare',
    'line.amended': 'Corretto',
    'line.amendedFrom': 'prima',
    'info.price': 'Costo',
    'price.none': 'Il catalogo non indica alcun costo.',
    'cat.none': 'Senza categoria',
    'noid.title': 'Per ordinare occorre essere qualcuno.',
    'noid.hint': 'Un ordine appartiene a qualcuno. Senza accesso non c’è nessuno a cui appartenga né nessuno da informare. Il catalogo è qui per essere consultato; per ordinare, avviare il server con --auth.',
    'cat.all': 'Tutte',
    'tbl.company': 'Organizzazione',
    'tbl.person': 'Persona',
    'tbl.placed': 'ordinato',
    'tbl.order': 'Ordine',
    'tbl.status': 'Stato',
    'tbl.searchCompany': 'Cercare un’organizzazione',
    'tbl.searchPerson': 'Cercare una persona',
    'tbl.searchDate': 'Data',
    'tbl.searchOrder': 'Cercare un ordine',
    'tbl.searchStatus': 'Cercare uno stato',
    'tbl.noMatch': 'Nessun ordine corrisponde alla ricerca.',
    'note.noCompany': 'La colonna Organizzazione resta vuota: oggi un ordine non porta alcuna organizzazione. Indica soltanto chi ordina e chi riceve.',
    'note.included': 'Sempre incluso — non deselezionabile.',
    'window.later': 'Ordinabile dal',
    'window.over': 'Non più ordinabile dal',
    'window.until': 'Ordinabile fino al',
    'variant.label': 'Variante',
    'variant.many': 'Sono possibili più varianti — ogni casella selezionata è una posizione a sé.',
    'variant.one': 'Scegliere esattamente una variante.',
    'variant.missing': 'Scegliere una variante per ogni posizione prima di ordinare.',
    'info.title': 'Indicazioni sul servizio',
    'info.id': 'Identificativo',
    'info.approval': 'Approvazione',
    'info.none': 'nessuna',
    'info.repeatable': 'Può essere detenuto più volte',
    'info.includes': 'Sempre incluso',
    'info.options': 'Opzionale',
    'info.yes': 'sì',
    'info.no': 'no',
    'services.none': 'Al momento non detiene alcuna prestazione.',
    'fav.mark': 'Aggiungere ai preferiti',
    'fav.clear': 'Rimuovere dai preferiti',
    'fav.only': 'Solo preferiti',
    'fav.none': 'Non ha contrassegnato nulla come preferito.',
    'fav.unresolved': 'Preferiti che questo catalogo non offre',
    'fav.full': 'Sono più preferiti di quanti un conto possa conservarne. Ne rimuova uno prima di aggiungerne un altro.',
    'find.label': 'Cercare una prestazione',
    'find.hint': 'Nome, abbreviazione o a che cosa le serve',
    'find.none': 'Nessuna prestazione corrisponde alla ricerca.',
    'find.hits': 'risultati',
    'find.clear': 'Azzerare la ricerca',
    'find.where': 'in',
    'signin.title': 'Si prega di accedere',
    'signin.hint': 'Questo shop le mostra il catalogo che le è assegnato e i suoi ordini. Per farlo deve sapere chi è lei.',
    'signin.user': 'Nome utente',
    'signin.password': 'Password',
    'signin.submit': 'Accedere',
    'signin.busy': 'Accesso in corso …',
    'signin.wrong': 'Il nome utente o la password non sono corretti.',
    'signin.throttled': 'Troppi tentativi — la password non è stata nemmeno verificata. Attenda alcuni minuti e riprovi.',
    'signin.failed': 'L’accesso non è andato a buon fine. Riprovi oppure si rivolga al servizio che ha creato il suo accesso.',
    'signin.expired': 'La sua sessione è scaduta. Si prega di accedere di nuovo.',
    'signin.sso': 'Accedere con',
    'signin.ssoFailed': 'L’accesso tramite il suo fornitore di identità non ha funzionato. Riprovi oppure si rivolga al servizio che ha creato il suo accesso.',
    'signin.or': 'oppure con nome utente e password',
    'signin.register': 'Non ha ancora un conto?',
    'signin.registerLink': 'Registrarsi',
  },
};

// The locale, from the browser and narrowed to what the page offers.
//
// The record puts a signed-in visitor's choice on their account rather than in
// this browser, because they arrive from a phone and a desktop. That endpoint
// does not exist yet, so the choice is remembered here in the meantime — which
// is the one place this page knowingly falls short of its own record.
function pickLocale() {
  const url = new URLSearchParams(location.search).get('lang');
  const stored = (() => { try { return localStorage.getItem('portal.lang'); } catch { return null; } })();
  for (const want of [url, stored, ...(navigator.languages || [navigator.language || ''])]) {
    if (!want) continue;
    const base = String(want).toLowerCase().split('-')[0];
    if (STRINGS[base]) return base;
  }
  return 'de';
}

let locale = pickLocale();

// t renders a key. A key with no string shows as itself — in this page that can
// only happen if the completeness test was removed, and looking broken in review
// is better than guessing at a language nobody chose.
function t(key) {
  // By the tag and then by its language, so a locale of `de-CH` renders the German
  // catalogue. offeredLocales only ever hands out a tag whose language this page
  // has strings for, so this never falls past the second step — which is what
  // keeps ADR-0313's condition true: every string exists in every locale offered.
  const own = STRINGS[locale] || STRINGS[baseOf(locale)] || {};
  return own[key] || key;
}

// offeredLocales is what the language switch offers
// (ADR-0415).
//
// **The languages this catalogue is kept in**, rather than the two this page
// happens to be translated into. A catalogue kept only in German used to show an
// EN button that turned the furniture English and left every product name German
// — a half-translated screen offered by the page itself, which is the thing
// ADR-0267 refuses to do by guessing and ADR-0313 sets the condition for.
//
// Narrowed to the languages this page can render, and that narrowing is the whole
// of ADR-0313 applied here: a catalogue may be kept in French, and until the
// furniture is French too, offering an FR button would land somebody on exactly
// the half-translated screen that record forbids. The French product names are
// stored and reachable through the fallback; what is not offered is a button that
// promises a French shop.
//
// Before there is a catalogue — the sign-in screen, or a visitor who is nobody's
// audience — it is this page's own languages, because the switch has to be
// reachable before the sign-in and there is nothing else to go on.
function offeredLocales() {
  const kept = ((state.catalog || {}).languages || []).filter((l) => STRINGS[baseOf(l)]);
  return kept.length ? kept : Object.keys(STRINGS);
}

// settleLocale moves the chosen language onto one the catalogue is actually kept
// in, once that is known.
//
// The choice is made before the catalogue is read — from the address, from this
// browser, or from the visitor's own list — so it is a language and not yet one
// of this catalogue's tags. A reader who chose English meets a catalogue kept in
// `en-EN` and should be reading it, not falling through to whatever came first.
//
// Same language first, then the catalogue's own first language. It never widens a
// choice: a reader who chose English and meets a German-only catalogue gets
// German, because there is no English here to give them.
function settleLocale() {
  const offered = offeredLocales();
  if (offered.includes(locale)) return;
  locale = offered.find((l) => baseOf(l) === baseOf(locale)) || offered[0];
}

function setLocale(next) {
  locale = next;
  try { localStorage.setItem('portal.lang', next); } catch { /* private window */ }
  render();
}

// baseOf is the language a tag is in: `de-CH` is German, `zh-Hans` is Chinese,
// and a bare `de` is its own base.
//
// The same reduction pickLocale does to the browser's list, and the two have to
// agree — that is the whole point of it being one function's worth of rule
// written twice rather than two rules.
function baseOf(tag) {
  return String(tag).toLowerCase().split('-')[0];
}

// pickText is the entry one language selects out of a map keyed by language tags.
//
// **By the language and not by the whole tag**, which is the correction. This page
// narrows a browser's language to its base, because its own words live in a
// message catalogue keyed that way; a product's texts are keyed by whatever the
// CATALOGUE declares, and `de-DE`, `en-GB` and `pt-BR` are all correct and all
// invisible to a lookup for `de`, `en`, `pt`. A catalogue kept in `de-DE; en-EN`
// would otherwise store every name under a key nothing here ever asks for, fall
// through to the first value it had, and show one word in both languages — the
// defect ADR-0413 was written about, arrived at down a different road.
//
// The exact tag wins over a regional one. A catalogue carrying both `de` and
// `de-CH` means the two deliberately, and answering with whichever the release
// happened to list first would be a coin toss. Between two regionals of the same
// language it IS the listed order, which is the release's own and therefore
// stable — worth knowing rather than worth preventing.
//
// A key that is present and blank is not an answer, for the reason it is not one
// in descriptionOf: it is the shape a cleared box leaves behind.
function pickText(texts, base) {
  const said = (v) => typeof v === 'string' && v.trim() !== '';
  if (said(texts[base])) return texts[base];
  for (const tag of Object.keys(texts)) {
    if (said(texts[tag]) && baseOf(tag) === base) return texts[tag];
  }
  return '';
}

// textOf reads a catalogue item's name in the current locale, falling back to
// whatever the catalogue has. A product is named by its catalogue, not by this
// page, so there is no key to look up and no way to be complete about it.
function textOf(texts, fallback) {
  if (!texts) return fallback;
  return pickText(texts, locale) || pickText(texts, 'de') || pickText(texts, 'en')
    || Object.values(texts).find((t) => typeof t === 'string' && t.trim() !== '')
    || fallback;
}

// The product's description, in the language this page is being read in where the
// catalogue has one and in whatever it does have otherwise.
//
// The fall-through was deliberately absent, on the reasoning that publishing
// refuses a product described in one declared language and not another — so a
// missing description could only be a state the release already rejects, and
// falling back would quietly undo the rule.
//
// That reasoning was wrong, and it hid descriptions rather than surfacing gaps.
// The two language lists are not the same list. Publishing demands a description
// in every language the *catalogue* declares; this page is read in one of its
// *own* locales, taken from the browser and narrowed to what it is translated
// into. A catalogue offered in German and French is complete by the publish rule
// and had nothing at all to say to a reader whose browser is English: two
// descriptions stored, neither shown, and no rule anywhere had been broken.
//
// So the locale is asked for first and the rest are reached after it. A paragraph
// in a language somebody does not read is worse than one they do — which is what
// the order encodes — and better than the blank the strict read gave them.
//
// A key that is present and blank is not an answer. That is the shape a
// half-filled form leaves behind, and taken as one it would end the search before
// the language that does say something.
function descriptionOf(item) {
  const d = (item || {}).descriptions;
  if (!d) return '';
  // Through pickText for each step, so a catalogue kept in `de-DE` is reached by
  // a reader on `de` — the same correction the name above carries.
  for (const text of [pickText(d, locale), pickText(d, 'de'), pickText(d, 'en'),
    ...Object.values(d)]) {
    if (typeof text === 'string' && text.trim() !== '') return text.trim();
  }
  return '';
}

// The typeface stacks the server ships, mirrored here because the page paints
// with them. The server refuses a name that is not one of these, so the two
// cannot drift into a catalogue naming a face the page has no stack for.
const TYPEFACES = {
  system: 'system-ui, -apple-system, "Segoe UI", Roboto, sans-serif',
  humanist: '"Segoe UI", Candara, Optima, "Trebuchet MS", sans-serif',
  serif: 'Georgia, Cambria, "Times New Roman", serif',
  mono: 'ui-monospace, "SF Mono", "Cascadia Mono", Menlo, monospace',
};

// applyTheme paints the catalogue's brand, deriving every shade from the one
// colour that is stored.
//
// The derivation lives in theme.js and is reused rather than repeated: eleven
// copies of it is eleven places for one to be wrong, and the one that matters is
// --accent-ink, which decides whether a button's label is readable on whatever
// colour somebody picked (ADR-0263).
//
// The sign-in screen keeps the operator's brand, because a catalogue is resolved
// from who you are and nobody is signed in yet. So a first-time visitor sees the
// operator's face until their catalogue answers; a returning one is painted from
// the cache before the first frame, which is what the cache is for.
function applyTheme(catalog) {
  const theme = (catalog && catalog.theme) || {};
  const root = document.documentElement;

  // applyAccent with nothing clears the overrides, which falls back to the
  // instance brand this page already declared — the right answer for a catalogue
  // that has no face of its own.
  applyAccent(theme.accent || '');
  if (theme.typeface && TYPEFACES[theme.typeface]) {
    root.style.setProperty('--font-sans', TYPEFACES[theme.typeface]);
  }
  try {
    // Remember which catalogue this was painted for. Without the id the cache
    // would repaint a returning visitor in somebody else's brand after their
    // assignment changed — worse than the flash it exists to prevent.
    localStorage.setItem('portal.theme', JSON.stringify({
      id: catalog && catalog.id, accent: theme.accent, typeface: theme.typeface,
    }));
  } catch { /* private window */ }
}

// paintFromCache runs before anything is fetched, so a returning visitor sees
// their own brand from the first frame. The server always wins: applyTheme
// overwrites this once the catalogue answers, and an unreachable server leaves
// the cached paint intact — the discipline ADR-0113 established.
function paintFromCache() {
  try {
    const cached = JSON.parse(localStorage.getItem('portal.theme') || 'null');
    if (cached) applyTheme({ id: cached.id, theme: cached });
  } catch { /* nothing cached, or no storage */ }
}

const state = {
  // tasks are the open tasks of the orders on this page (ADR-0416); taskOpen is
  // the one whose form is open, and taskError what answering it last said.
  tasks: [],
  tasksTruncated: false,
  taskOpen: '',
  taskError: '',
  catalog: null,
  release: null,
  orders: [],
  // held is what the inventory says this person already has, as itemId -> since.
  // It is read from the inventory and not derived from the orders on this page:
  // the order that granted a right is deleted by retention long before the right
  // ends, and a catalogue that marked from orders would stop marking on the
  // ninetieth day (ADR-0312).
  held: new Map(),
  // principals is the directory as it was read, and directory is the same thing as
  // principalId -> name.
  //
  // An order names people by id and by nothing else, because a name copied into a
  // record outlives the reason for holding it (ADR-0314).
  // The same decision leaves the other half to the screen: a name is resolved when
  // the screen is rendered. This is that resolution, read once per load rather
  // than per row — and read once for both readers of it, the column and the
  // recipient picker, which were otherwise two calls for one list.
  principals: [],
  directory: new Map(),
  chosen: new Set(),
  busy: false,
  error: '',

  // --- Who is reading, and whether anybody is ---------------------------------

  // me is the answer to GET /api/v1/auth/me, read once per load and consumed by
  // everything on the page that depends on it. One read rather than two: the gate
  // below and loadWhoIAm asked the same question, and two answers to it can
  // disagree.
  me: null,
  // needSignIn is the case this page had no answer for: enforcement is on and
  // nobody is signed in, so every route here answers 401. It is set only by a
  // refusal — an unreadable answer leaves it false, because not knowing who
  // somebody is is not the same as knowing they are nobody.
  needSignIn: false,
  // providers is whatever the server federates its login to, empty where it
  // federates nothing. Read before the form is drawn, because on an installation
  // that has one there may be no password to type at all.
  providers: [],
  // registerURL is where somebody with no account starts, empty where the
  // instance does not offer that (ADR-0126).
  registerURL: '',
  // signinError is what the last attempt is waiting to say, as a catalogue key.
  // A key and not a sentence: the page is redrawn on every keystroke of state and
  // a sentence captured in one locale would survive the language switch.
  signinError: '',
  // signinBusy is an attempt in flight — one at a time, or an impatient second
  // press spends one of the five tries the throttle counts.
  signinBusy: false,
  // signinUser is what was typed into the username field, kept because an attempt
  // redraws the page twice — once to say it is busy, once to say what happened —
  // and a field rebuilt from nothing comes back empty. Somebody who mistyped their
  // password would have to retype their name as well, which is the small cruelty
  // that turns a second attempt into a telephone call. The password is deliberately
  // not kept: clearing it is what every login does, and it is what somebody
  // retyping expects to find.
  signinUser: '',

  // --- What the mockups add: a shell with three destinations, a cascade that
  // remembers where it is, and a basket that survives moving between products.

  // view is which of the three the nav is on.
  view: 'catalog',
  // group and offering are where the cascade stands. A column shows nothing until
  // the one to its left is chosen, which is what makes it a cascade rather than
  // four lists — and it is *selection* rather than filtering, because a
  // decomposition is read one branch at a time.
  //
  // group is null for "every group", the way category is, because it is an
  // attribute and every heading is a legitimate answer. offering is the empty
  // string because it names a product, and "no product chosen" is not one.
  group: null,
  offering: '',
  // step is which of the cascade's four columns a narrow screen shows (ADR-0417).
  // A wide one shows all four and never reads it: the columns side by side are the
  // point of the screen there. A phone cannot hold four columns, so it shows one
  // and moves right as somebody chooses — which is the order a cascade is read in
  // anyway. Choosing in a column advances it; the stepper's back button retreats.
  step: 0,
  // basket is every item id chosen so far, across products. It is the whole
  // reason this is a two-step order now: the previous page ordered the moment a
  // card's button was pressed, so two bundles were two orders, two approvals and
  // two provisioning runs for one decision somebody made once.
  basket: new Set(),
  // variants is which shapes of each product were chosen, as itemId -> [variantId].
  //
  // Kept beside the basket rather than on the basket entry, because the product
  // that carries variants is usually not the one that was clicked: somebody orders
  // a bundle and the colour belongs to the phone inside it. Nothing is answered in
  // advance — variants are unordered on purpose, so there is no first one to fall
  // back on, and a pre-selected colour would be shipped to everybody who did not
  // look. A list rather than a single answer, because the catalogue's own rule is
  // that the same product in two shapes is something the orderer may keep both of,
  // where the product says it may be held more than once.
  variants: {},
  // inBasket is whether the basket screen is showing instead of the cascade. Not
  // a fourth nav entry: the mockups make it the next step of the same screen,
  // reached and left by the action row.
  inBasket: false,
  // forWhom is the recipient an order is placed for, empty for oneself. What is
  // sent: a principal id picked from the directory, or whatever was typed — the
  // server resolves a principal id, a username, a directory id or a mail address
  // (ADR-0356).
  forWhom: '',
  // filters is the orders table's per-column search, keyed by column.
  filters: { company: '', person: '', date: '', order: '', status: '' },
  // info is the service whose details are open, empty for none.
  info: '',
  // favourites is what this person marked, as ids — a bookmark and never an
  // entitlement (ADR-0348). Held as a Set because every row asks
  // "is this one of mine" while the cascade renders.
  favourites: new Set(),
  // favouritesOnly narrows the cascade to marked products, which is what a
  // shortcut list is for. It is a filter over the columns rather than a fourth
  // destination in the nav: a favourite is still a product in the catalogue, and
  // pulling it onto its own screen would hide what it is part of.
  favouritesOnly: false,
  // query is what somebody is looking for. A search is not a fifth column: while
  // it is set the cascade is replaced by a flat list of matches, each showing the
  // path it sits on (ADR-0355).
  query: '',
  // forWhomLabel is what the field shows while forWhom holds what is sent. The
  // two differ after somebody picks from the directory: the field reads "Ada
  // Lovelace" and the order carries the principal id, because a display name is
  // not something the server can resolve and an id is not something a person can
  // read (ADR-0356).
  // category narrows the cascade to one heading
  // (ADR-0360). Three values, because
  // there are three questions: null is every heading, '' is the bucket for
  // products that carry none, and anything else is that heading.
  category: null,
  forWhomLabel: '',
  // people is the principals directory, users only, loaded once and only for a
  // caller who may order in somebody else's name. It is the list every member and
  // assignee picker in Atlas already reads (ADR-0073).
  people: [],
  // canOrder is whether an order can be placed at all, which is not a permission
  // but an identity: the server refuses an order with no orderer, because one has
  // nobody to notify and nobody to hold responsible. With enforcement off there is
  // nobody to be, so the catalogue is readable and an order is not — and the page
  // says so rather than offering a button that fails at the end
  // (ADR-0377).
  canOrder: false,
  // meID is the account reading, which is what addresses its picture. Separate
  // from meName because a name is for a person to read and an id is for a URL.
  meID: '',
  // meName is whoever is reading, as a name rather than an id: the display name
  // the account carries, its username where it has none, and empty where there is
  // nobody to be. It is what the corner says when no recipient has been chosen —
  // see renderNav.
  meName: '',
  // mayOrderForOthers mirrors the gate the server enforces
  // (ADR-0349). The page asks so it can leave the field
  // out, rather than offering something that answers 403 — a field somebody may
  // not use is worse than no field, because it looks like a permission that
  // failed rather than one they never had.
  mayOrderForOthers: false,
  // mayFollowProcess is whether this reader may open the instance fulfilling an
  // order. It is an operations surface — every route that finds or opens an
  // instance is operator-only — so the link is offered to whoever may follow it
  // and to nobody else.
  mayFollowProcess: false,
  // config holds what somebody filled in per product, keyed by item id and then by
  // the form's own field key (ADR-0358).
  //
  // Kept in state rather than read off the page at the last moment, because the
  // basket is redrawn whenever anything on it changes and a rendered form does not
  // survive its container being replaced. What was typed is captured back into here
  // before each redraw and handed to the form again as its prefill.
  config: {},
  // configError names the product whose form is not valid yet, empty for none.
  configError: '',
  // editing names the position whose details are open for correction, as
  // "<orderId>|<itemId>", empty for none (ADR-0359).
  editing: '',
  // following is what the process link last found, by order id. Kept per row
  // because the question was asked from a row: an answer at the top of the page is
  // off-screen for whoever pressed a button further down, and a button whose
  // answer nobody sees is a button that did nothing.
  following: new Map(),
};

// --- The four levels the mockups draw ---------------------------------------
//
// Atlas has no "Kategorie / Bundle / Marktleistung / Service" typing: an item is
// an item, and the hierarchy is the containment graph, of any depth. So the
// columns are derived from *position in that graph* rather than read from a
// field that does not exist:
//
//   Bundle        — an item nothing else contains
//   Marktleistung — what a bundle directly contains
//   Service       — what a Marktleistung directly contains
//
// Kategorie has no source at all. It is rendered as the catalogue itself and
// says so, rather than inventing a grouping: a column filled with a guess is
// worse than one that explains what it is waiting for.

// parentIn names the item that directly contains this one, or "" for a root.
//
// Both kinds of containment count. Whether something can be taken out of the whole
// is a different question from whether it sits inside it, and this one is about
// where it sits.
function parentIn(rel, child) {
  for (const [whole, parts] of Object.entries((rel || {}).includes || {})) {
    if ((parts || []).includes(child)) return whole;
  }
  for (const [whole, parts] of Object.entries((rel || {}).options || {})) {
    if ((parts || []).includes(child)) return whole;
  }
  return '';
}

// levelOf says what one position is called, and it is the only thing that does
// (ADR-0383).
//
// Atlas has no Bundle/Marktleistung/Service typing: an item is an item, and the
// hierarchy is the containment graph. So the level is read off position in that
// graph — once, here, rather than once per view. The cascade used depth and the
// basket used *kind*, so the direct part of a bundle was a Marktleistung in one
// column and a Service in the other: one position, two names, on one screen.
//
// A root that contains nothing is not a bundle. A bundle is a thing made of other
// things, and announcing a single product as one says something untrue about it
// in the one word the column has. It is an offering — what the catalogue's own
// vocabulary calls a thing that is offered on its own.
//
// Depth beyond the third level keeps the third name. The screen has three columns
// and a deeper graph has to land somewhere; calling it a service is true of it,
// and inventing a fourth word for a shape nobody has drawn would not be.
function levelOf(rel, id) {
  // Depth from the one walk that already knows how to do it, cycles and all. A
  // second traversal written here would be a second place for "how deep is this"
  // to be answered, which is the shape of the defect this rule exists to remove.
  //
  // Two levels, not three. A bundle is offered as a Marktleistung — it holds the
  // orchestration, and the services behind it hold their own provisioning — so a
  // root is a Marktleistung whether or not it carries parts, and everything behind
  // one is a service however deep it sits. The Bundle level is not answered
  // differently, it is gone: asking whether something is a bundle or an offering
  // was the question that announced a single product as something made of other
  // things, and there is no third word for a service two edges down that would be
  // truer than "service".
  return depthOf(rel || {}, id) === 0 ? 'offering' : 'service';
}

// levelName is that answer in the reader's language, and the same word the column
// heads carry — which is the whole point of there being one rule.
function levelName(rel, id) {
  return t(`col.${levelOf(rel, id)}`);
}

// partsOf returns what an item directly carries, integral parts first.
//
// The two kinds stay apart, because they mean opposite things to a basket: an
// inclusion is a consequence of ordering the whole and never deselectable, an
// option is an offer.
function partsOf(release, id) {
  const inc = ((release.includes || {})[id] || []).map((x) => ({ id: x, integral: true }));
  const opt = ((release.options || {})[id] || []).map((x) => ({ id: x, integral: false }));
  return [...inc, ...opt];
}

// levelsOf returns the four columns for where the cascade currently stands.
// categoriesOf is every heading this release's top-level products carry, plus the
// bucket for the ones that carry none (ADR-0360).
//
// Sorted alphabetically, because a heading is a string and there is nothing on it
// to sort by. An ordering of its own would be the entity the decision refused,
// arriving through the back door. The bucket is always last and only appears when
// something is in it: a heading for nothing is a heading nobody can use, and
// hiding uncategorised products entirely would lose them.
//
// Both of those live in headingsOf below, with the reading of the heading itself,
// because the services view draws the same two columns off a different set of
// products — and a second implementation of this is how the two screens came to
// disagree about a heading once already.
function categoriesOf(release) {
  return headingsOf(products(release), 'category');
}

// headingOf is one product's heading, in the language this page is being read in.
//
// Two fields and not one: the string on the product is the KEY — what everything
// groups by, what a search hit sets to open the cascade at the right column, what
// an already published release holds — and the map beside it is how that key is
// written for a reader (ADR-0412). Where the
// catalogue has no translation the key renders, which is every product written
// before the field existed and every catalogue declaring one language.
//
// It reads through textOf, so a heading reaches a reader by exactly the rule every
// other text on this page does: the page's own locale first, then whatever the
// catalogue does have. The two language lists are not the same list, and a heading
// stored in German and French with nothing shown to an English reader is the gap
// the description already learned about.
function headingOf(item, field) {
  const key = ((item || {})[field] || '').trim();
  if (!key) return '';
  return textOf((item || {})[`${field}Texts`], key);
}

// headingsOf collects one of the two heading fields off a set of products: each
// distinct key, and the wording to show it under.
//
// **Sorted by the wording, not by the key.** The wording is what is on the screen,
// and a French reader given a column ordered by German words would be reading an
// order nothing on the page explains. The bucket for products carrying none stays
// last, as before.
//
// Where two products agree on the key and disagree on the wording, the first in
// release order wins — deterministic, because a release is sorted by id. It is not
// a state a published catalogue can be in: publishing refuses the disagreement,
// for the reason it refuses a half-translated heading.
function headingsOf(items, field) {
  const wording = new Map();
  let none = false;
  for (const it of items) {
    const key = (it[field] || '').trim();
    if (!key) { none = true; continue; }
    if (!wording.has(key)) wording.set(key, headingOf(it, field));
  }
  const out = [...wording].map(([key, text]) => ({ key, text }))
    .sort((a, b) => a.text.localeCompare(b.text, locale));
  if (none) out.push({ key: '', text: '' });
  return out;
}

// inCategory reports whether a top-level product belongs under the heading now
// selected. null is every heading, which is what the shop opens on.
// groupsOf is every product group named by the products under the heading now
// open, and the bucket for the ones that name none.
//
// Narrowed by the category on purpose. A group has no record and therefore no
// category of its own — the product carries both strings and the chain is
// assembled per product. Built from every product in the catalogue instead, the
// column would offer groups under a heading that holds none of their products.
//
// The consequence, which is a property and not a fault: a group whose products sit
// in two categories appears under both. Nothing is contradicted, because nothing
// anywhere claims a group belongs to one.
//
// Sorted and bucketed like the categories above, through the same function and
// for the reasons given there.
function groupsOf(release) {
  return headingsOf(products(release).filter(inCategory), 'productGroup');
}

// inGroup reports whether a product belongs under the group now selected. null is
// every group, which is what the shop opens on.
function inGroup(item) {
  if (state.group === null) return true;
  return (item.productGroup || '').trim() === state.group;
}

function inCategory(item) {
  if (state.category === null) return true;
  return (item.category || '').trim() === state.category;
}

function levelsOf(release) {
  const offerings = products(release).filter(inCategory).filter(inGroup)
    .map((i) => ({ id: i.id, integral: false }));
  // Everything behind the chosen product, however deep, rather than one level of
  // it. A Marktleistung holds the orchestration and the services behind it hold
  // their own provisioning, so a service two edges down is a service like any
  // other — and the level that used to sit between them is what made one position
  // carry two names.
  const services = state.offering ? descendantsOf(release, state.offering) : [];
  return { offerings, services };
}

// descendantsOf is everything one product carries, at any depth, integral parts
// first and each kept apart by how it arrived.
//
// Depth-first and cycle-safe. A part reached twice — included by the whole and
// offered beside it, or shared by two branches — is listed once, as it was first
// reached: an entry that appeared twice would be two rows for one service, and
// ticking either would be the same position.
function descendantsOf(release, id) {
  const out = [];
  const seen = new Set([id]);
  const walk = (at, integral) => {
    for (const part of partsOf(release, at)) {
      if (seen.has(part.id)) continue;
      seen.add(part.id);
      // Integral only all the way down: a service included by something that was
      // itself an offer is only in the order if that offer was taken.
      out.push({ id: part.id, integral: integral && part.integral });
      walk(part.id, integral && part.integral);
    }
  };
  walk(id, true);
  return out;
}

// carriedBy reports whether choosing this whole already brings the part, at any
// depth. An integral part is not a separate choice, and offering to add one that
// is already coming would put the same thing in a basket twice.
function carriedBy(release, whole, part) {
  const seen = new Set();
  const walk = (id) => {
    if (seen.has(id)) return false;
    seen.add(id);
    for (const p of (release.includes || {})[id] || []) {
      if (p === part || walk(p)) return true;
    }
    return false;
  };
  return walk(whole);
}

// inBasketNow reports whether an item is chosen, directly or because something
// chosen always carries it.
function inBasketNow(release, id) {
  if (state.basket.has(id)) return true;
  for (const chosen of state.basket) {
    if (carriedBy(release, chosen, id)) return true;
  }
  return false;
}

// api reads one route, and its failures carry their status.
//
// The status used to live only inside the message string, which made "was this
// refused or did it break" a question about parsing text. It is the difference
// between showing somebody a sign-in and showing them an error, so it travels as
// a field.
async function api(path, options) {
  const res = await fetch(path, { credentials: 'same-origin', ...options });
  if (!res.ok) {
    const err = new Error(`${res.status} ${await res.text()}`);
    err.status = res.status;
    throw err;
  }
  return res.status === 204 ? null : res.json();
}

// readMe asks who is reading, and is the gate everything else on this page stands
// behind.
//
// A 401 here is the defect this answers. With enforcement on and no session every
// route the shop reads is refused: the catalogue read was swallowed and drawn as
// "no catalogue is assigned to you", which is a statement about entitlement and
// not about authentication, and the orders read was not swallowed at all — so what
// a visitor got was an error line with an HTTP status in it, no catalogue, and
// nothing anywhere offering the sign-in that would have fixed it.
//
// Anything else is not a refusal. Unreadable is not forbidden, and the honest
// response to not knowing is to carry on and offer less: the page loads as it
// always did, and the controls that need an identity are simply not drawn.
async function readMe() {
  state.me = null;
  state.needSignIn = false;
  try {
    state.me = await api('/api/v1/auth/me');
  } catch (e) {
    state.needSignIn = e.status === 401;
  }
  if (state.needSignIn) forgetTheReader();
}

// forgetTheReader drops what was resolved for whoever was here, because nobody is
// now. It matters on the second way in: a session that ran out leaves a catalogue,
// a release and a basket behind, and a sign-in screen drawn over them names
// somebody else's catalogue in its heading and marks it with their brand while
// asking who you are. Nothing is disclosed — it was this browser's own tab — but a
// page that keeps presenting a catalogue as yours after establishing that you are
// nobody is a page saying two things at once.
function forgetTheReader() {
  state.catalog = null;
  state.release = null;
  state.orders = [];
  state.held = new Map();
  state.favourites = new Set();
  state.principals = [];
  state.directory = new Map();
  state.basket = new Set();
  state.me = null;
  loadWhoIAm();
}

// loadSignInOptions reads what the sign-in screen may offer before anybody has a
// session. Both routes are public for exactly this reason, both answer "nothing
// configured" as an ordinary answer, and neither failing may cost the password
// form — which is the one control that works everywhere.
async function loadSignInOptions() {
  state.providers = [];
  state.registerURL = '';
  try {
    const list = await api('/api/v1/auth/providers');
    state.providers = Array.isArray(list) ? list : [];
  } catch { /* no provider, or the server could not say — the password form stands */ }
  try {
    const cfg = await api('/api/v1/settings/registration');
    if (cfg && cfg.enabled && cfg.url) state.registerURL = cfg.url;
  } catch { /* registration off or unreachable — the line stays hidden */ }
}

async function load() {
  state.error = '';
  // First, because everything below needs a session: a load that read the
  // catalogue first would spend a refusal before establishing there is nobody to
  // refuse, and would then have to unpick which of the two answers it was.
  await readMe();
  if (state.needSignIn) {
    await loadSignInOptions();
    render();
    return;
  }
  try {
    state.catalog = await api('/api/v1/shop/catalog');
  } catch {
    // 404 here is the ordinary "you are the audience for nothing" answer, not a
    // failure: the page says so rather than showing an error.
    state.catalog = null;
  }
  applyTheme(state.catalog);
  // The catalogue decides which languages there are to choose between, so the
  // choice is settled onto one of them the moment it is known — before anything
  // is drawn, or the first paint would be in a language the switch cannot show as
  // chosen.
  settleLocale();
  if (state.catalog) {
    const releases = await api(`/api/v1/catalogs/${state.catalog.id}/releases`);
    state.release = releases && releases.length ? releases[0] : null;
  }
  state.orders = await api('/api/v1/orders');
  await loadTasks();
  // What one person holds, and what they have marked. Both are facts about an
  // account, and with enforcement off there is no account — the server says so
  // rather than inventing an empty answer, which is right of the server and must
  // not take the page down with it. An unreadable per-person list is a list
  // missing, not a catalogue missing (ADR-0377).
  state.held = new Map();
  state.favourites = new Set();
  try {
    const inv = await api('/api/v1/inventory');
    state.held = new Map(((inv && inv.items) || []).map((i) => [i.itemId, i.since]));
  } catch { /* nobody holds anything when there is nobody */ }
  try {
    const favs = await api('/api/v1/shop/favourites');
    state.favourites = new Set((favs && favs.itemIds) || []);
  } catch { /* and nobody has marked anything */ }
  // The directory, for the columns that show who an order is for. Any
  // authenticated caller may read it (ADR-0073), and a
  // reader who is nobody cannot — so a failure leaves the map empty and every row
  // falls back to the id, which is what those rows showed before.
  try {
    state.principals = (await api('/api/v1/principals')) || [];
    state.directory = new Map(state.principals.map((p) => [p.id, p.name]));
  } catch {
    // No directory, no names — the ids still say who.
    state.principals = [];
    state.directory = new Map();
  }
  loadWhoIAm();
  render();
}

// loadWhoIAm asks what this account may do, and nothing else.
//
// The page had no idea who was reading it. That was fine while every screen was
// the same for everybody, and stopped being fine the moment ordering in somebody
// else's name became a role: the field was drawn for every visitor and answered
// 403 for almost all of them. Roles come from the same record the session
// snapshots them from, so what the page hides and what the server refuses cannot
// drift apart.
//
// It reads the answer readMe already has rather than asking again. Two reads of
// one question is one round trip too many and, worse, two answers that can
// disagree: a session that expires between them would leave the page holding a
// catalogue for somebody it has just decided is nobody.
function loadWhoIAm() {
  state.mayOrderForOthers = false;
  state.canOrder = false;
  state.meName = '';
  state.meID = '';
  state.people = [];
  const me = state.me;
  // Nothing was readable. Offer less rather than guess more: the ordinary shop
  // still works, ordering for somebody else simply is not offered.
  if (!me) return;
  const user = (me && me.user) || {};
  const roles = user.roles || [];
  // The name, not the id. An id in the corner is the account's identifier and not
  // an answer to "who am I signed in as" — and the same corner shows a recipient's
  // display name when one is chosen, so the two halves of one label would
  // otherwise be two different kinds of thing.
  state.meName = String(user.displayName || user.username || '').trim();
  state.meID = String(user.id || '').trim();
  // The server's own rule, mirrored rather than guessed: an order needs an
  // identity, and what makes one is a principal carrying a user id.
  state.canOrder = state.meID !== '';
  // With enforcement off there is nobody to be, exactly as the server has it.
  // Ordering in somebody else's name is a question about authority, and it only
  // arises where an order can be placed at all. Offered without an identity it
  // would be a field whose every use ends in a refusal.
  state.mayOrderForOthers = state.canOrder &&
    (!me.authEnabled || roles.some((r) => r === 'operator' || r === 'admin'));
  // Whether the instance view is reachable at all. Not tied to canOrder: somebody
  // may follow an order they did not place, and a reader with no identity in an
  // unenforced deployment may follow anything the server will answer.
  state.mayFollowProcess = !me.authEnabled ||
    roles.some((r) => r === 'operator' || r === 'admin');
  if (!state.mayOrderForOthers) return;
  // From the directory the load already read, rather than a second call for the
  // same list. Users only: a group cannot receive an order, because an entitlement
  // is held by a person, and offering a team would produce a recipient the server
  // refuses.
  //
  // An empty directory leaves the field taking a typed id. A picker that could not
  // load is a convenience missing, not a screen broken.
  state.people = state.principals.filter((e) => e.type === 'user');
}

// signIn posts the password form and, on success, loads the shop the visitor
// asked for.
//
// It stays here. The Console's sign-in lands on the Console, which is the wrong
// product for somebody who followed a link from a mail to order a laptop and
// holds no Console role — they would land in a shell whose every entry is missing
// and have to find their own way back. The endpoints are the same; only where it
// returns to differs, and that is the whole reason this screen exists rather than
// a redirect.
async function signIn(username, password) {
  if (state.signinBusy) return;
  state.signinBusy = true;
  state.signinError = '';
  render();
  try {
    await api('/api/v1/auth/login', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ username, password }),
    });
  } catch (e) {
    state.signinBusy = false;
    state.signinError = signInFailureText(e);
    render();
    return;
  }
  state.signinBusy = false;
  await load();
}

// signInFailureText names what refused the attempt, as a catalogue key.
//
// A 401 is the only answer that is about the credentials, and it stays vague: the
// server refuses an unknown account and a wrong password identically so a login
// cannot be read as a directory, and this screen must not undo that.
//
// A 429 is the throttle (ADR-0197), and it is a different
// failure entirely — it refuses the *attempt*, before the password is looked at,
// once five have been wrong. Reported as a credential failure it is how somebody
// spends a quarter of an hour hunting a password that is already correct. It
// matters more here than on the Console: an operator can read the server log, and
// the person this page is for can only telephone the desk this shop exists to
// save. Saying so leaks nothing, because the throttle counts attempts against
// names that do not exist too.
//
// Anything else is not a credential failure either, and the server's own wording
// does not go onto a pre-auth screen.
function signInFailureText(e) {
  if (e && e.status === 401) return 'signin.wrong';
  if (e && e.status === 429) return 'signin.throttled';
  return 'signin.failed';
}

// renderSignIn is the whole page while nobody is signed in: no nav, no catalogue,
// no basket. Not a banner over the shop — there is no shop to put it over, since
// every route behind this screen answers 401, and a page drawn around empty lists
// would report "you are the audience for nothing" to somebody who is simply not
// signed in yet.
function renderSignIn() {
  const form = el('form', { class: 'card signin' });
  const user = el('input', {
    name: 'username', autocomplete: 'username', required: 'required',
    value: state.signinUser,
    // The field somebody has to fill next: the name on a first visit, the password
    // once the name survived an attempt.
    autofocus: state.signinUser ? null : 'autofocus',
  });
  const pass = el('input', {
    name: 'password', type: 'password', autocomplete: 'current-password', required: 'required',
    autofocus: state.signinUser ? 'autofocus' : null,
  });
  form.addEventListener('submit', (ev) => {
    ev.preventDefault();
    state.signinUser = user.value;
    signIn(user.value, pass.value);
  });
  paint(form,
    el('h2', { class: 'signin-title' }, t('signin.title')),
    el('p', { class: 'muted' }, t('signin.hint')),
    // The callback sends a failed federated attempt back here with ?sso=failed and
    // no reason. The reason is in the server's audit log, where somebody who can
    // act on it will find it, rather than in a URL anybody could send anybody.
    new URLSearchParams(location.search).get('sso') === 'failed'
      ? el('p', { class: 'error' }, t('signin.ssoFailed'))
      : null,
    state.providers.length
      ? el('div', { class: 'providers' },
        // returnTo, because the callback lands wherever the login started and its
        // default is the Console — which is where somebody who followed a link to
        // order a laptop least belongs. The server takes the value from an
        // allowlist of the two pages that can start one of these, so this names a
        // page rather than choosing a destination.
        state.providers.map((p) => el('a', {
          class: 'provider',
          href: `${p.start}${p.start.includes('?') ? '&' : '?'}returnTo=${encodeURIComponent(location.pathname)}`,
        }, `${t('signin.sso')} ${p.name}`)),
        el('p', { class: 'muted or' }, t('signin.or')))
      : null,
    el('label', { class: 'field' }, t('signin.user'), user),
    el('label', { class: 'field' }, t('signin.password'), pass),
    state.signinError
      ? el('p', { class: 'error', role: 'alert' }, t(state.signinError))
      : null,
    el('button', { class: 'primary', type: 'submit', disabled: state.signinBusy },
      state.signinBusy ? t('signin.busy') : t('signin.submit')),
    state.registerURL
      ? el('p', { class: 'muted' }, `${t('signin.register')} `,
        el('a', { href: state.registerURL }, t('signin.registerLink')))
      : null);
  return form;
}

// products returns what a person picks from: the items nothing else includes.
// A part is shown under the whole it belongs to rather than beside it, or the
// catalogue would read as a list of components.
function products(release) {
  const included = new Set();
  for (const parts of Object.values(release.includes || {})) parts.forEach((p) => included.add(p));
  for (const parts of Object.values(release.options || {})) parts.forEach((p) => included.add(p));
  return (release.items || []).filter((i) => !included.has(i.id));
}

function itemsById(release) {
  const by = {};
  for (const i of release.items || []) by[i.id] = i;
  return by;
}

// order places one order for everything in the basket.
//
// One order and not one per product, which is the whole reason the basket
// exists: the previous page ordered the moment a card's button was pressed, so
// two bundles were two orders, two approvals and two provisioning runs for one
// decision somebody made once.
//
// recipient travels only when somebody was named. The server decides whether the
// caller may order for them and whether that person is eligible for what is in
// the basket — this page does not pre-judge either, because it would have to
// guess at rules the release carries.
async function order() {
  if (!state.basket.size) return;
  // Every form is asked whether it is complete before anything is sent. The form
  // runtime decides that, against the schema's own rules — refusing here rather
  // than letting the order go keeps what was typed on screen instead of losing it
  // to a round trip that fails somewhere else.
  state.configError = '';
  for (const [itemID, form] of mounted) {
    let errors;
    try { ({ errors } = form.submit()); } catch { continue; }
    if (errors && Object.keys(errors).length) {
      state.configError = itemID;
      render();
      return;
    }
  }
  harvest();

  state.busy = true;
  state.error = '';
  render();
  try {
    const rel = state.release || {};
    const chosen = [];
    const seen = new Set();
    const add = (id) => {
      if (seen.has(id)) return;
      seen.add(id);
      chosen.push({ id });
      for (const p of (rel.includes || {})[id] || []) add(p);
    };
    for (const id of state.basket) add(id);
    const config = answersFor(rel, chosen);
    // Only for what is actually being ordered. A choice left over from a product
    // that was taken out again would be refused by the server as an answer about
    // something the order does not carry — correctly, and for nothing.
    const variants = {};
    for (const c of chosen) {
      const taken = state.variants[c.id] || [];
      if (taken.length) variants[c.id] = [...taken];
    }

    await api('/api/v1/orders', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        releaseId: state.release.id,
        items: [...state.basket],
        ...(state.forWhom.trim() ? { recipient: state.forWhom.trim() } : {}),
        ...(Object.keys(config).length ? { config } : {}),
        ...(Object.keys(variants).length ? { variants } : {}),
      }),
    });
    state.basket.clear();
    state.chosen.clear();
    state.config = {};
    state.variants = {};
    state.configError = '';
    state.inBasket = false;
    state.view = 'orders';
    await load();
  } catch (e) {
    state.error = `${t('portal.failed')} ${e.message}`;
  } finally {
    state.busy = false;
    render();
  }
}

function el(tag, attrs, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    // A nullish or false value means "do not set this attribute". setAttribute has
    // no falsy handling of its own, so `disabled: busy ? 'disabled' : null` would
    // render disabled="null" — which a browser reads as disabled, permanently.
    // This page spreads a conditional object instead and so never hit it; the
    // guard is here so the next conditional attribute written the obvious way
    // works.
    if (v == null || v === false) continue;
    if (k === 'class') node.className = v;
    else if (k.startsWith('on')) node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v);
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    node.append(c.nodeType ? c : document.createTextNode(String(c)));
  }
  return node;
}

// heldPill marks what the person already has, with the day it started.
//
// It marks and does not disable. A card orders a product *and* whatever options
// are ticked under it, so a bundle somebody already holds may still have an
// option they do not — and a button greyed out for the whole card would make that
// option unreachable. The server does the deciding: an item already held and not
// repeatable is ordered as a skipped line, which says "you asked, you had it"
// rather than silently dropping the request.
//
// An item the catalogue marks repeatable gets no pill even when it is held:
// holding a second licence is the ordinary case there, and a mark that means
// nothing trains people to ignore the mark.
function heldPill(item) {
  if (!item || item.multipleAllowed) return null;
  const since = state.held.get(item.id);
  if (since == null) return null;
  return el('p', { class: 'pill ok' },
    `${t('portal.held')} — ${t('portal.held.since')} `
    + new Date(since / 1e6).toLocaleDateString(locale));
}

// --- The cascade -------------------------------------------------------------

// cell renders one row of a column: an optional toggle, the label, and whatever
// sits at the right edge.
function cell(opts) {
  const o = opts || {};
  // Three slots and the rule that tells them apart: lead and trail hold controls,
  // meta holds text about the row. A control has a fixed size and must not shrink;
  // text has no size of its own and must. Putting text in the trail gave it the
  // control's promise never to give width back, which is exactly what starved the
  // name beside it.
  //
  // The trail is wrapped here rather than by each caller. Every icon in it already
  // refuses to shrink, but an unstyled span is a flex item that may, and its
  // contents are inline boxes that wrap inside it — so in a narrow column the star,
  // the +/- and the "i" broke onto a second line and one row read as two.
  //
  // A rule applied per caller is a rule the next caller forgets, and there are
  // seven of them. Wrapping here means a trail cannot be built without it. The
  // wrapper takes an array as readily as a node, because el flattens its children.
  return el('div', { class: 'cell' },
    o.lead || null,
    // The name, and under it whatever describes the row rather than acts on it.
    //
    // They are one flex item and not two, because what may shrink is the pair: a
    // name and its price are both text about the same thing, and putting the price
    // beside the trail's icons made it a sibling that refused to give width back.
    // In a 220px column that left the name a few pixels and it wrapped one letter
    // per line — a row that read as a vertical alphabet.
    el('span', { class: 'body' },
      o.onOpen
        ? el('button', {
          class: o.open ? 'label on' : 'label',
          onclick: o.onOpen,
        }, o.text)
        : el('span', { class: 'label' }, o.text),
      o.meta ? el('span', { class: 'meta' }, o.meta) : null),
    o.trail ? el('span', { class: 'trail' }, o.trail) : null);
}

// --- When a product may be ordered -------------------------------------------
//
// The server is the gate (ADR-0397): a rule enforced
// only where it is displayed is a rule every other caller walks past. This is the
// courtesy half — a basket that cannot be submitted is the control-that-fails, and
// filling one to be refused at the end teaches somebody the page is broken.
//
// Shown and disabled rather than hidden, which is the same choice the integral
// part beside it makes. A product whose window has not opened is exactly the case
// the field exists for — a catalogue published ahead of the date it opens — so
// hiding it would remove the one thing somebody wants to know, which is when.

// windowOf reads an item's window, tolerating a release published before the
// shop read the field.
function windowOf(item) {
  const w = (item || {}).lifecycle || {};
  return { from: Number(w.from) || 0, until: Number(w.until) || 0 };
}

// orderableNow reports whether this moment is inside the item's window. Both sides
// are inclusive and zero is unbounded, exactly as the server reads them — two
// readings of one rule that disagreed would be a shop offering what the order is
// refused for, which is the failure this pairing exists to prevent.
function orderableNow(item, at) {
  const w = windowOf(item);
  const now = at == null ? Date.now() * 1e6 : at;
  if (w.from && now < w.from) return false;
  if (w.until && now > w.until) return false;
  return true;
}

// windowNote is what a row says about its own window, or null where there is
// nothing to say — which is the ordinary product.
function windowNote(item) {
  const w = windowOf(item);
  if (!w.from && !w.until) return null;
  const day = (ns) => new Date(ns / 1e6).toLocaleDateString(locale);
  if (!orderableNow(item)) {
    return w.from && Date.now() * 1e6 < w.from
      ? `${t('window.later')} ${day(w.from)}`
      : `${t('window.over')} ${day(w.until)}`;
  }
  // Inside the window and about to leave it: the one case where somebody reading
  // an orderable row still needs the date.
  return w.until ? `${t('window.until')} ${day(w.until)}` : null;
}

// toggle renders the mockups' square −/+ control.
//
// Integral parts get a disabled "−": they are in, and they cannot be taken out.
// Showing them greyed rather than hiding the control keeps the column readable
// as a decomposition — the point of the screen — while saying they are not a
// choice.
function toggle(release, id, integral) {
  const inIt = inBasketNow(release, id);
  // A basket that cannot be submitted is the control-that-fails this mode is
  // meant to avoid: filling one and finding no way out teaches somebody the page
  // is broken. Shown disabled rather than hidden, for the reason an integral part
  // is — the column stays readable as a decomposition, which is what the mode is
  // for (ADR-0377).
  if (!state.canOrder) {
    return el('button', {
      class: 'sq', disabled: 'disabled', title: t('noid.title'),
      'aria-label': t('noid.title'),
    }, '+');
  }
  if (integral) {
    return el('button', {
      class: 'sq', disabled: 'disabled', title: t('note.included'),
      'aria-label': t('note.included'),
    }, '\u2212');
  }
  // Outside its window. The title carries the date rather than a bare "no",
  // because the date is the whole of what somebody does next: come back, or stop
  // looking.
  const item = itemsById(release)[id];
  const shut = windowNote(item);
  if (!orderableNow(item)) {
    return el('button', {
      class: 'sq', disabled: 'disabled', title: shut || '', 'aria-label': shut || '',
    }, '+');
  }
  return el('button', {
    class: 'sq',
    'aria-pressed': inIt ? 'true' : 'false',
    onclick: () => {
      if (state.basket.has(id)) state.basket.delete(id); else state.basket.add(id);
      render();
    },
  }, inIt ? '\u2212' : '+');
}

// infoButton is the "i" the mockups put at the right edge of the service column,
// and which now sits at the right edge of every column that carries a product.
//
// It was drawn on services alone, which made the catalogue the only place on this
// page where a bundle could not be opened. The panel already worked for one:
// picking a bundle out of the search opens it, and the services view has carried
// the button on all four levels since it was built. So a maintainer could write a
// price onto a bundle, see it in "my services", find it through the search — and
// not reach it from the column the bundle lives in.
function infoButton(id) {
  return el('button', {
    class: 'sq info',
    'aria-label': t('info.title'),
    onclick: () => { state.info = state.info === id ? '' : id; render(); },
  }, 'i');
}

// infoPanel is what the "i" opens: what the catalogue actually knows about a
// service. It says nothing the release does not carry — a panel that padded
// itself out with invented detail would be worse than no panel.
function infoPanel(rel, item) {
  const kind = item.approval && item.approval.kind && item.approval.kind !== 'none'
    ? item.approval.kind : t('info.none');
  // What the product carries, in two groups that are never merged: one is a
  // consequence of ordering it and cannot be dropped, the other an offer standing
  // beside it (ADR-0312). A single list would tell
  // somebody they are buying three phone cases.
  //
  // This is not the panel branching on which column was clicked — a product with
  // no parts simply has two empty lists and shows neither line. It reads the same
  // two groups for everything, which is why the panel stays one thing.
  //
  // **At any depth, through descendantsOf, and not the one level below this
  // product.** The column beside this card and the basket behind it both walk the
  // whole containment graph, so a panel reading one level disagreed with the
  // screen next to it: a bundle whose hardware carries an operating system named
  // the hardware and stayed silent about the system, which is ordered with it
  // either way. The rule that decides the group is descendantsOf's and is the one
  // the basket applies — integral all the way down, so a part included by
  // something that was itself an offer is listed as an offer rather than as
  // something nobody can drop.
  const parts = descendantsOf(rel, item.id);
  const carried = namesOf(rel, parts.filter((p) => p.integral).map((p) => p.id));
  const offered = namesOf(rel, parts.filter((p) => !p.integral).map((p) => p.id));
  return el('div', { class: 'card' },
    el('h3', {}, textOf(item.texts, item.id)),
    productPicture(item.id),
    // Above the ordering facts and not muted, because it is the one thing on this
    // card written for the person deciding rather than about the transaction.
    // Absent entirely where there is none — an empty paragraph would leave a gap
    // that reads as something that failed to load.
    descriptionOf(item) ? el('p', { class: 'product-description' }, descriptionOf(item)) : null,
    el('p', { class: 'muted' }, `${t('info.id')}: ${item.id}`),
    // As the catalogue wrote it, never reformatted. A price here is a sentence
    // somebody chose — "CHF 1'200.–", "im Grundpaket enthalten" — and a page that
    // parsed it into a number would be inventing the money model the catalogue
    // deliberately does not have (ADR-0361).
    el('p', { class: 'muted' },
      `${t('info.price')}: ${item.price ? item.price : t('price.none')}`),
    el('p', { class: 'muted' }, `${t('info.approval')}: ${kind}`),
    el('p', { class: 'muted' },
      `${t('info.repeatable')}: ${item.multipleAllowed ? t('info.yes') : t('info.no')}`),
    carried.length
      ? el('p', { class: 'muted' }, `${t('info.includes')}: ${carried.join(', ')}`) : null,
    offered.length
      ? el('p', { class: 'muted' }, `${t('info.options')}: ${offered.join(', ')}`) : null,
    // The window, where there is one. Beside the price and the approval rather
    // than as a pill, because it is a fact about the product and not about this
    // person: "orderable until the 31st" is true for everybody reading it.
    windowNote(item)
      ? el('p', { class: 'muted' }, windowNote(item)) : null,
    heldPill(item));
}

// productPicture is the product as it looks, where the catalogue has a picture of
// it (ADR-0391).
//
// Asked for by rendering it and not by asking first whether one exists. A product
// without a picture is the ordinary case and answers 404, which is the browser's
// own cheapest "no" — a probe request per row would double the calls to learn what
// the image request learns anyway. The element removes itself when the answer is
// that 404, so a product with no picture leaves no broken-image icon and no gap.
//
// No width or height attribute: the catalogue does not resize what was uploaded
// (a server that re-encodes somebody's picture decides their product looks near
// enough), so the page bounds it in CSS instead and the image keeps its own shape.
function productPicture(id) {
  return el('img', {
    class: 'picture',
    src: `/api/v1/catalog-products/${encodeURIComponent(id)}/picture`,
    alt: '',
    onerror: (e) => { e.target.remove(); },
  });
}

// namesOf turns part ids into the names the catalogue wrote for them.
//
// An id the release does not carry is dropped rather than printed raw: this is
// read by somebody deciding what to order, and `iphone-18-huelle-clear` in a list
// of names reads as the page having broken.
function namesOf(rel, ids) {
  const by = itemsById(rel || {});
  return (ids || []).filter((id) => by[id]).map((id) => textOf(by[id].texts, id));
}

// star marks or unmarks one product.
//
// It writes through to the server and takes the answer as the new truth rather
// than toggling locally and hoping: a favourites list is the one thing on this
// page a second tab can be changing at the same time, and the route answers with
// the whole list precisely so this does not have to guess.
async function star(id) {
  const marked = state.favourites.has(id);
  // Painted before the request, so a star responds to the press. The answer
  // replaces it either way, so a failure corrects it rather than leaving a lie.
  if (marked) state.favourites.delete(id); else state.favourites.add(id);
  render();
  try {
    const out = await api(`/api/v1/shop/favourites/${encodeURIComponent(id)}`,
      { method: marked ? 'DELETE' : 'PUT' });
    state.favourites = new Set((out && out.itemIds) || []);
  } catch (e) {
    // Put it back and say what happened. A star that silently returned to where
    // it was is the kind of small wrongness somebody stops trusting the page over.
    if (marked) state.favourites.add(id); else state.favourites.delete(id);
    state.error = `${t('portal.failed')} ${e.message}`;
  }
  render();
}

// starButton is the affordance the row carries.
function starButton(id) {
  // A favourite belongs to an account. With nobody signed in there is nobody to
  // hold one, and the route says so — so the mark is not offered rather than
  // offered and refused.
  if (!state.canOrder) return el('span', {});
  const on = state.favourites.has(id);
  return el('button', {
    class: 'sq',
    'aria-pressed': on ? 'true' : 'false',
    'aria-label': t(on ? 'fav.clear' : 'fav.mark'),
    title: t(on ? 'fav.clear' : 'fav.mark'),
    onclick: () => star(id),
  }, on ? '\u2605' : '\u2606');
}

// keepFavourites narrows a column when the favourites filter is on.
//
// A whole is kept when it is marked *or* when something under it is: hiding a
// bundle whose service somebody starred would hide the way to reach the star.
function keepFavourites(rel, entries) {
  if (!state.favouritesOnly) return entries;
  return entries.filter((e) => state.favourites.has(e.id)
    || [...state.favourites].some((f) => carriedBy(rel, e.id, f)));
}

// --- Finding a service ------------------------------------------------------
//
// A search is not a fifth column. A cascade is for *browsing* — it shows what a
// thing is part of — and a search is for *finding*, where the person does not
// know which level the thing sits at. Filtering the four columns would leave a
// match three clicks deep with nothing on screen to say it was there.
//
// So while a query is set the cascade is replaced by a flat list, and each hit
// carries the path it sits on, so the answer says both *what* and *where*.

// searchable is every word one item can be found by.
//
// Every locale's text, not just the one being rendered: a person reading a German
// catalogue may well type the English name, and hiding a product from somebody
// who typed a word the catalogue itself carries would be the search failing at
// the one job it has.
function searchable(item) {
  return [
    item.id,
    ...Object.values(item.texts || {}),
    ...(item.keywords || []),
  ].join(' ').toLowerCase();
}

// pathTo names where an item sits, outermost first. One level is enough for the
// screen: "in Productivity Enabling" tells somebody which branch to open, and the
// full chain rendered as prose would be a worse answer to the same question.
function pathTo(rel, by, id) {
  for (const [whole, parts] of Object.entries(rel.includes || {})) {
    if ((parts || []).includes(id)) return textOf((by[whole] || {}).texts, whole);
  }
  for (const [whole, parts] of Object.entries(rel.options || {})) {
    if ((parts || []).includes(id)) return textOf((by[whole] || {}).texts, whole);
  }
  return '';
}

function searchHits(rel) {
  const q = state.query.trim().toLowerCase();
  if (!q) return [];
  // Every word must appear somewhere, so "vpn zugang" narrows rather than widens.
  // A search that grew its answer as somebody typed more would be teaching them
  // to type less.
  const words = q.split(/\s+/);
  return (rel.items || []).filter((it) => {
    const hay = searchable(it);
    return words.every((w) => hay.includes(w));
  });
}

// renderSearch is the flat answer.
function renderSearch(rel, by) {
  const hits = searchHits(rel);
  if (!hits.length) {
    return el('div', { class: 'empty' }, el('p', {}, t('find.none')));
  }
  return el('div', { class: 'cascade' },
    el('div', { class: 'col', style: 'grid-column: 1 / -1' },
      el('div', { class: 'colhead' }, `${hits.length} ${t('find.hits')}`),
      hits.map((it) => {
        const where = pathTo(rel, by, it.id);
        return cell({
          text: textOf(it.texts, it.id),
          // Choosing a hit takes the person to where it lives, rather than
          // ordering it from a list that does not show what it comes with.
          onOpen: () => {
            state.query = '';
            // The product it belongs to, and the two headings that product writes
            // on itself — a hit opened under the wrong heading would be a cascade
            // showing a column its own selection excludes.
            const root = rootOf(rel, it.id);
            const item = by[root] || {};
            state.category = (item.category || '').trim();
            state.group = (item.productGroup || '').trim();
            state.offering = root;
            state.info = it.id;
            state.step = 3;
            render();
          },
          lead: starButton(it.id),
          meta: where
            ? el('span', {}, `${t('find.where')} ${where}`)
            : null,
        });
      })));
}

// rootOf is the product an item belongs to — itself, where nothing contains it.
//
// Walks all the way up rather than one or two steps: the cascade's third column is
// the product and its fourth is everything behind that product at any depth, so a
// hit three edges down still opens under the product it is part of. Cycle-safe,
// because a release is authored and a cycle is a thing somebody can draw.
function rootOf(rel, id) {
  const seen = new Set();
  let at = id;
  for (;;) {
    const up = parentIn(rel, at);
    if (!up || seen.has(up)) return at;
    seen.add(up);
    at = up;
  }
}

function renderCatalogue() {
  if (!state.catalog) {
    return el('div', { class: 'empty' },
      el('p', {}, t('portal.none')),
      el('p', { class: 'muted' }, t('portal.none.hint')));
  }
  if (!state.release || !(state.release.items || []).length) {
    return el('div', { class: 'empty' }, el('p', {}, t('portal.empty')));
  }

  const rel = state.release;
  const by = itemsById(rel);
  const { offerings, services } = levelsOf(rel);
  const name = (id) => textOf((by[id] || {}).texts, id);

  // Kategorie. The headings the products themselves carry
  // (ADR-0360), with "all" above them so
  // the column is never a dead end.
  const headings = categoriesOf(rel);
  // Choosing anywhere in the chain clears everything to its right: a column still
  // showing what the one before it no longer selects is a screen contradicting
  // itself.
  const clearBelow = (level) => {
    if (level <= 0) state.group = null;
    if (level <= 1) state.offering = '';
    state.info = '';
  };
  const category = el('div', { class: 'col' },
    el('div', { class: 'colhead' }, t('col.category')),
    cell({
      text: t('cat.all'),
      open: state.category === null,
      onOpen: () => { state.category = null; clearBelow(0); state.step = 1; render(); },
    }),
    headings.map((h) => cell({
      text: h.text || t('cat.none'),
      open: state.category === h.key,
      onOpen: () => {
        state.category = state.category === h.key ? null : h.key;
        clearBelow(0);
        state.step = 1;
        render();
      },
    })));

  // Produktgruppe, in the column the Bundle level used to hold. It is an attribute
  // the product writes on itself, like the heading to its left — so this column is
  // read off the products under that heading and not off the containment graph,
  // which is what the two columns to the right are read off.
  const groups = groupsOf(rel);
  const groupCol = el('div', { class: 'col' },
    el('div', { class: 'colhead' }, t('col.group')),
    cell({
      text: t('group.all'),
      open: state.group === null,
      onOpen: () => { state.group = null; clearBelow(1); state.step = 2; render(); },
    }),
    groups.map((g) => cell({
      text: g.text || t('group.none'),
      open: state.group === g.key,
      onOpen: () => {
        state.group = state.group === g.key ? null : g.key;
        clearBelow(1);
        state.step = 2;
        render();
      },
    })));

  // Produkt — the Marktleistung. Every root is one, with or without parts: it is
  // what holds the orchestration, and what stands behind it are the services that
  // are actually provisioned.
  const offeringCol = el('div', { class: 'col' },
    el('div', { class: 'colhead' }, t('col.offering')),
    keepFavourites(rel, offerings).map((o) => cell({
      text: name(o.id),
      open: state.offering === o.id,
      onOpen: () => {
        state.offering = state.offering === o.id ? '' : o.id;
        state.info = '';
        // Only a product chosen has services to show; taking the choice back
        // leaves a narrow screen on the products it was picked from.
        state.step = state.offering ? 3 : 2;
        render();
      },
      trail: [starButton(o.id), toggle(rel, o.id, o.integral), infoButton(o.id)],
    })));

  const serviceCol = el('div', { class: 'col' },
    el('div', { class: 'colhead' }, t('col.service')),
    keepFavourites(rel, services).map((sv) => cell({
      text: name(sv.id),
      lead: toggle(rel, sv.id, sv.integral),
      trail: [starButton(sv.id), infoButton(sv.id)],
    })));

  // Favourites this catalogue does not carry. Counted rather than hidden in
  // silence: a mark that stopped appearing with no word looks like the page lost
  // it, and the person cannot tell that from a catalogue that moved under them.
  const unresolved = [...state.favourites].filter((id) => !by[id]).length;

  // The part of the screen a query replaces. Built as a thunk because typing
  // repaints it without re-rendering the page: re-rendering would replace the
  // search field somebody is typing into, and the caret would jump to the end of
  // the word after every character. This is the same trick the order filters use,
  // for the same reason.
  const body = () => (state.query.trim() !== ''
    ? [renderSearch(rel, by)]
    : [stepper(name, headings, groups),
      el('div', { class: 'cascade', 'data-step': String(state.step) },
        category, groupCol, offeringCol, serviceCol),
      state.info && by[state.info]
        ? el('div', { style: 'margin-top:16px' }, infoPanel(rel, by[state.info])) : null]);
  catalogueBody = body;
  catalogueBodyNode = el('div', {}, body());

  return el('div', {},
    el('div', { class: 'favbar' },
      el('input', {
        type: 'search', id: 'find', value: state.query,
        placeholder: t('find.hint'), 'aria-label': t('find.label'),
        oninput: (e) => { state.query = e.target.value; repaintCatalogueBody(); },
      }),
      el('label', {},
        el('input', {
          type: 'checkbox', id: 'fav-only',
          ...(state.favouritesOnly ? { checked: 'checked' } : {}),
          onchange: (e) => { state.favouritesOnly = e.target.checked; render(); },
        }),
        ' ', t('fav.only')),
      state.favouritesOnly && !state.favourites.size
        ? el('span', { class: 'muted' }, t('fav.none')) : null,
      unresolved
        ? el('span', { class: 'muted' }, `${t('fav.unresolved')}: ${unresolved}`) : null),
    catalogueBodyNode);
}

// stepper is the cascade's way back on a narrow screen (ADR-0417), where one
// column shows at a time: a back button and the path chosen so far. A wide screen
// hides it — every column is on screen there, and a back button would go
// somewhere already in view.
function stepper(name, headings, groups) {
  const cols = [t('col.category'), t('col.group'), t('col.offering'), t('col.service')];
  // The words the columns show, not the keys they select by: a heading's key is
  // its first language's wording and the reader may be reading another.
  const said = (list, key, all, none) => {
    if (key === null) return all;
    const found = list.find((x) => x.key === key);
    return (found && found.text) || key || none;
  };
  const path = [
    state.step > 0 ? said(headings, state.category, t('cat.all'), t('cat.none')) : null,
    state.step > 1 ? said(groups, state.group, t('group.all'), t('group.none')) : null,
    state.step > 2 && state.offering ? name(state.offering) : null,
  ].filter(Boolean);
  return el('div', { class: 'stepper' },
    state.step > 0
      ? el('button', {
        class: 'secondary step-back',
        onclick: () => { state.step = Math.max(0, state.step - 1); render(); },
      }, `\u2039 ${t('step.back')}`)
      : null,
    el('span', { class: 'step-path' },
      path.length ? el('span', { class: 'muted' }, `${path.join(' \u203a ')} \u203a `) : null,
      el('strong', {}, cols[state.step] || cols[0])));
}

// What a keystroke redraws, and the node it redraws into. Both are reset by every
// render; a repaint before the first render, or after the view moved elsewhere, is
// a no-op rather than a write into a node nobody is looking at.
let catalogueBodyNode = null;
let catalogueBody = () => [];

function repaintCatalogueBody() {
  if (!catalogueBodyNode || !catalogueBodyNode.isConnected) return;
  paint(catalogueBodyNode, catalogueBody());
}

// pickShape takes or gives back one shape of one product.
//
// Where the catalogue says the product may be held more than once, a second tick
// is a second position rather than a change of mind — which is the case an answer
// per product could not hold. Where it may not, the tick moves: two positions of
// something nobody may hold twice is an order that cannot be satisfied, and the
// server refuses it, so offering it here would be offering a mistake.
function pickShape(id, variant, multiple) {
  const chosen = state.variants[id] || [];
  const at = chosen.indexOf(variant);
  if (at >= 0) {
    chosen.splice(at, 1);
  } else if (multiple) {
    chosen.push(variant);
  } else {
    chosen.length = 0;
    chosen.push(variant);
  }
  state.variants[id] = chosen;
  render();
}

// variantsMissing lists the basket's lines that still have no shape chosen.
//
// It walks the same expansion the basket draws, because the product carrying the
// variants is frequently an integral part nobody clicked: ordering a bundle is
// ordering the phone inside it, and the colour is still the orderer's to name.
//
// Held on the page as well as at the server. The server refuses such an order —
// it must, since the basket is one caller of a route anybody may call — but a
// round trip that comes back 400 loses the reader's place to tell them something
// they could be told without leaving it.
function variantsMissing() {
  const rel = state.release || {};
  const by = itemsById(rel);
  const seen = new Set();
  const out = [];
  const walk = (id) => {
    if (seen.has(id)) return;
    seen.add(id);
    if (((by[id] || {}).variants || []).length && !(state.variants[id] || []).length) {
      out.push(id);
    }
    for (const p of (rel.includes || {})[id] || []) walk(p);
  };
  for (const id of state.basket) walk(id);
  return out;
}

// renderBasket is the second step of the same screen: what has been chosen,
// before anybody is asked to approve it.
function renderBasket() {
  if (!state.basket.size) {
    return el('div', { class: 'empty' }, el('p', {}, t('basket.empty')));
  }
  const rel = state.release || {};
  const by = itemsById(rel);
  // Every chosen item and everything each one always carries, so the basket
  // shows what will actually be provisioned rather than what was clicked.
  const shown = [];
  const seen = new Set();
  const add = (id, integral) => {
    if (seen.has(id)) return;
    seen.add(id);
    shown.push({ id, integral });
    for (const p of (rel.includes || {})[id] || []) add(p, true);
  };
  for (const id of state.basket) add(id, false);

  // And what those products offer beside themselves, that nobody has taken.
  //
  // An aggregation is an offer, not a consequence, so it is never pulled in — it
  // is listed unticked and taken deliberately (ADR-0312).
  // It is listed *here* and not only in the column it hangs under because the
  // basket is the screen where somebody decides what they are actually asking
  // for: an offer reachable only by navigating back to a column they have left is
  // an offer they will not see. Taking one moves it into its level column, where
  // it is a position like any other.
  const offers = [];
  const offered = new Set();
  for (const x of shown) {
    for (const id of (rel.options || {})[x.id] || []) {
      if (offered.has(id) || seen.has(id) || !by[id]) continue;
      offered.add(id);
      offers.push(id);
    }
  }

  // The columns are the three levels, read from the one rule that decides them.
  //
  // They were "what was chosen" and "what came with it" — which is a different
  // question wearing the level names: whether a position can be taken out is
  // answered per row by its control, and where it sits is answered here. Reading
  // one off the other is what made the direct part of a bundle a Marktleistung in
  // the cascade and a Service here.
  const row = (x) => cell({
    text: textOf((by[x.id] || {}).texts, x.id),
    lead: x.integral
      ? el('button', { class: 'sq', disabled: 'disabled', title: t('note.included') }, '\u2212')
      : el('button', {
        class: 'sq',
        'aria-label': t('act.discard'),
        onclick: () => { state.basket.delete(x.id); render(); },
      }, 'X'),
    trail: infoButton(x.id),
  });
  const offerCell = (id) => cell({
    text: textOf((by[id] || {}).texts, id),
    // The same control the cascade uses, so a tick means one thing on the
    // whole page: it adds to the basket, and a second press takes it out.
    lead: toggle(rel, id, false),
    meta: [
      (by[id] || {}).price ? el('span', {}, by[id].price) : null,
      // The level it will sit under once it is taken, so the same position is
      // called the same thing before and after the decision.
      el('span', {}, levelName(rel, id)),
    ],
    trail: infoButton(id),
  });

  // One group per offering, and each group one line of the grid.
  //
  // The columns were three flat lists, each stacked on its own, so a row's height
  // in one had nothing to do with its height in the next: a service sat beside
  // whichever offering happened to share its line, and with two offerings in the
  // basket nothing said which service belonged to which. What the reader needs is
  // the relation, and the relation is the one thing three independent lists
  // cannot draw.
  //
  // So every offering is a line and its four cells are the grid's four columns in
  // that line. The grid makes a line as tall as its tallest cell, which is what
  // keeps the next offering from starting beside the last one's third service.
  //
  // Which offering a row belongs to is read off the same containment the level
  // is — ownerAmong walks up through what includes and offers it — and the level
  // itself is still levelOf's answer, not this grouping's.
  const roots = shown.filter((x) => levelOf(rel, x.id) === 'offering');
  const ownerOf = ownerAmong(rel, new Set(roots.map((x) => x.id)));
  const groups = roots.map((x) => ({ root: x, services: [], offers: [] }));
  const byRoot = new Map(groups.map((g) => [g.root.id, g]));
  // A service whose offering is not in the basket — an option taken from the
  // search while the product that offers it was not. It still gets a line of its
  // own rather than vanishing, because a position nobody can see is a position
  // nobody can take out.
  const loose = { root: null, services: [], offers: [] };
  for (const x of shown) {
    if (levelOf(rel, x.id) !== 'service') continue;
    (byRoot.get(ownerOf(x.id)) || loose).services.push(x);
  }
  for (const id of offers) (byRoot.get(ownerOf(id)) || loose).offers.push(id);
  if (loose.services.length || loose.offers.length) groups.push(loose);

  const cols = el('div', { class: 'cascade basket-groups' },
    // Named once, at the top: the three names belong to the grid, and repeating
    // them per group would make a table of tables. The third column is the gap
    // the cascade has there, so a basket and the catalogue above it keep the same
    // columns in the same places.
    el('div', { class: 'colhead' }, t('col.offering')),
    el('div', { class: 'colhead' }, t('col.service')),
    el('div', {}),
    el('div', { class: 'colhead' }, t('col.options')),
    // The labels on the second and fourth cells are for a narrow screen, where the
    // four cells of a line stack under each other and the names at the top no
    // longer stand above them (ADR-0417). A wide screen does not draw them.
    groups.flatMap((g) => [
      el('div', { class: 'col grp grp-first' }, g.root ? row(g.root) : null),
      el('div', { class: 'col grp', 'data-label': t('col.service') }, g.services.map(row)),
      el('div', { class: 'col grp' }),
      el('div', { class: 'col grp', 'data-label': t('col.options') }, g.offers.map(offerCell)),
    ]));

  // The forms below the basket rather than beside each row: a form is taller than a
  // row and an integral part asks its own questions, so a column that had to hold
  // both would put the cascade and a text field in the same width.
  const asking = shown.filter((x) => configFormOf(rel, x.id));

  // And which shape of each line was ordered, where the product comes in more than
  // one. Below the columns for the same reason, and above the forms because it is
  // the question that decides what the thing *is* rather than how it is set up.
  //
  // Nothing is selected when the list is drawn: variants are unordered on purpose,
  // so there is no first one to fall back on, and a colour chosen by the page is a
  // colour nobody chose.
  const choosing = shown.filter((x) => ((by[x.id] || {}).variants || []).length);

  return el('div', {},
    cols,
    choosing.length
      ? el('div', { style: 'margin-top:18px' }, choosing.map((x) => {
        const item = by[x.id] || {};
        const taken = state.variants[x.id] || [];
        // How many may be ticked is the catalogue's statement and not this
        // screen's: multipleAllowed already says whether somebody may hold the
        // product more than once, and a second rule here would be a second answer
        // to one question.
        const multiple = !!item.multipleAllowed;
        return el('div', { class: 'card cfg' },
          el('h3', {}, `${t('variant.label')}: ${textOf(item.texts, x.id)}`),
          el('p', { class: 'note' }, t(multiple ? 'variant.many' : 'variant.one')),
          el('div', {}, (item.variants || []).map((v) => el('label',
            { style: 'display:inline-block;margin-right:14px' },
            el('input', {
              type: multiple ? 'checkbox' : 'radio',
              name: `variant-${x.id}`,
              ...(taken.includes(v.id) ? { checked: 'checked' } : {}),
              onchange: () => pickShape(x.id, v.id, multiple),
            }),
            ' ', textOf(v.texts, v.id)))));
      }))
      : null,
    asking.length
      ? el('div', { style: 'margin-top:18px' }, asking.map((x) => el('div', { class: 'card cfg' },
        el('h3', {}, `${t('cfg.title')}: ${textOf((by[x.id] || {}).texts, x.id)}`),
        state.configError === x.id
          ? el('p', { class: 'error' }, t('cfg.invalid')) : null,
        el('div', {
          'data-configkey': x.id,
          'data-formid': configFormOf(rel, x.id),
        }, el('p', { class: 'note' }, t('cfg.loading'))))))
      : null,
    // What the "i" on a basket row opens. The button was drawn here from the
    // start and the panel was not, so pressing it set state.info, redrew the
    // page and showed nothing — a control that answers with a blank.
    //
    // It matters most on this screen. The basket is where somebody decides
    // whether to actually order the thing, and the panel is where the price, the
    // approval rule, the description and the picture are; a row here is a name
    // and two buttons, and the name is all they had to go on.
    //
    // Below the forms rather than above them, because it belongs to a row and the
    // forms belong to the order: a panel wedged between a row and the questions
    // that row asks would read as part of the question. render() harvests every
    // mounted form before it repaints, so opening this does not cost somebody
    // what they have typed.
    state.info && by[state.info]
      ? el('div', { style: 'margin-top:18px' }, infoPanel(rel, by[state.info])) : null);
}

// --- What a product needs that its name does not say -------------------------
//
// A laptop is not fully described by being a laptop: somebody has to say which
// cost centre it is booked to. A product names an Atlas form, and the basket is
// where it is filled in — the last screen before an order exists, and the one that
// already shows what will actually be provisioned
// (ADR-0358).
//
// The form is rendered by Atlas's own form runtime, the one the Tasks app and the
// incident repair already use. Nothing here interprets a field: which questions
// there are, which are required and what counts as valid are the form's own
// statements, and a second copy of those rules would be wrong the first time
// somebody edits the form.

// amendKey is the bucket a correction's answers live in. It is deliberately not
// the item id: the same product can be in the basket and in an order at once, and
// one set of answers for both would put what somebody is correcting into what they
// are about to buy.
function amendKey(orderID, itemID) { return `amend:${orderID}:${itemID}`; }

// mounted holds the live form instances by mount key. A render replaces their
// containers, so each one is read back, destroyed and built again.
const mounted = new Map();
// schemas caches a form definition per id, so redrawing the basket does not refetch
// what has not changed.
const schemas = new Map();

// harvest reads what is currently typed into every mounted form back into state.
// Called before the page is redrawn, because a form does not survive its container
// being replaced and whatever was typed would go with it.
function harvest() {
  for (const [itemID, form] of mounted) {
    try {
      const { data } = form.submit();
      state.config[itemID] = { ...(data || {}) };
    } catch { /* a form that cannot be read keeps the last values we had */ }
  }
}

// configFormOf names the form a product asks for, or '' for one that asks nothing.
function configFormOf(rel, id) {
  const it = itemsById(rel)[id];
  return (it && it.configForm) || '';
}

// answersFor is what will be sent: only the products actually in the basket, and
// only those that ask something. The server refuses anything else, and it is right
// to — but a page that sent it anyway would turn a stale basket into a refused
// order the person cannot explain.
function answersFor(rel, shown) {
  const out = {};
  for (const x of shown) {
    if (!configFormOf(rel, x.id)) continue;
    const given = state.config[x.id];
    if (given && Object.keys(given).length) out[x.id] = given;
  }
  return out;
}

// mountConfigForms builds every form the basket is showing. Asynchronous because
// the form runtime is a lazy import; the container says so meanwhile.
async function mountConfigForms() {
  const hosts = [...document.querySelectorAll('[data-configkey]')];
  for (const [, form] of mounted) {
    try { form.destroy(); } catch { /* already gone with its container */ }
  }
  mounted.clear();
  if (!hosts.length) return;

  let Form;
  try {
    const mod = await import('./formviewer.js');
    mod.ensureFormStyles();
    ({ Form } = await mod.loadFormViewer());
  } catch {
    for (const host of hosts) paint(host, el('p', { class: 'note' }, t('cfg.failed')));
    return;
  }

  for (const host of hosts) {
    const itemID = host.dataset.configkey;
    const formID = host.dataset.formid;
    try {
      if (!schemas.has(formID)) {
        const def = await api(`/api/v1/forms/${encodeURIComponent(formID)}`);
        schemas.set(formID, def && def.schema);
      }
      const schema = schemas.get(formID);
      if (!schema) throw new Error('no schema');
      const form = new Form({ container: host });
      // Handed back what was typed before the last redraw, which is what makes the
      // basket survivable: adding a second product must not empty the first's form.
      await form.importSchema(
        typeof schema === 'string' ? JSON.parse(schema) : schema,
        state.config[itemID] || {});
      mounted.set(itemID, form);
    } catch {
      // A form id that no longer resolves is a stale binding in the catalogue, not
      // a broken basket. Say so and leave the order possible: refusing it here
      // would let one edited form stop every order for that product.
      paint(host, el('p', { class: 'note' }, t('cfg.failed')));
    }
  }
}

// deriveStatus mirrors the server's own rule rather than asking for it: an order
// carries its lines, and its standing is computed from them so the two cannot
// disagree. Doing it here keeps that property — a stored status could.

// FOLLOW_TIMEOUT_MS is how long the process link waits for the server before it
// says so. Long enough for a slow scoped search on a busy engine, short enough
// that nobody reads the note as a page about to arrive.
const FOLLOW_TIMEOUT_MS = 20000;

// followProcess opens the instance fulfilling one order.
//
// Looked up when the link is pressed rather than resolved for every row: finding
// an instance is a search, and a table of thirty orders would be thirty searches
// to draw a column most readers never use.
//
// Narrowed to the fulfilment process by name. Every provisioning sub-process is
// started with the order id too, so a search that took the first hit would open
// one position's process and call it the order.
//
// Whatever it finds out is said beside the row it was pressed from. It used to be
// said in state.error, which is painted above the table: an order further down the
// page produced a message off-screen, and the button read as broken — which is how
// it was reported. The one case that works navigates away, and the two that cannot
// are the two that have to be visible.
//
// And narrowed by definition, not only filtered by name afterwards. A search that
// names no definition reads every instance on the server and every variable of
// each, and on an installation of any size that does not come back: the note said
// "Wird abgefragt …" and stayed, which is how it was reported. Named, the search
// reads that definition's own index — the instances of the fulfilment process,
// which is one per order. Every deployed version is asked, newest first, because an
// order placed before the last redeploy is worked by the version it started on.
//
// And bounded in time. A lookup that never answers is the one outcome that must not
// look like "still asking": the reader waits for a page that is not coming.
async function followProcess(order) {
  state.following.set(order.id, t('proc.asking'));
  render();
  const said = (what) => { state.following.set(order.id, what); render(); };
  const giveUp = new AbortController();
  const timer = setTimeout(() => giveUp.abort(), FOLLOW_TIMEOUT_MS);
  try {
    const defs = await api('/api/v1/processes', { signal: giveUp.signal });
    const versions = (defs || [])
      .filter((d) => d.processId === 'atlas-auftrag-erfuellung')
      .sort((a, b) => b.version - a.version);
    const query = `orderId=${order.id}`;
    const hits = [];
    let hit = null;
    for (const d of versions) {
      const page = await api(`/api/v1/instances/search?process=${d.key}` +
        `&q=${encodeURIComponent(query)}`, { signal: giveUp.signal });
      const found = (page && page.items) || [];
      hits.push(...found);
      // Archived first, and separately. The search falls back to the exported event
      // log when this server's own index has nothing, and marks what it answers with:
      // the instance was hard-deleted by history retention (ADR-0115) and exists only
      // in the export. Following one reaches a replay view with nothing to replay,
      // which says "Could not load this instance's replay." — a dead end two screens
      // from the page that knew better.
      hit = found.find((i) => !i.archived && i.processId === 'atlas-auftrag-erfuellung');
      if (hit) break;
    }
    if (!hit) {
      // What is known, and not a cause that was guessed. This said the instance had
      // been removed by retention, which is one of three reasons it is not found and
      // the least likely of them: an order whose fulfilment never started has no
      // instance to remove, and that is what somebody reads this message about on the
      // day they ordered. A page that names a cause it cannot know sends whoever
      // reads it to look in the wrong place.
      const archived = hits.some((i) => i.archived && i.processId === 'atlas-auftrag-erfuellung');
      said(t(archived ? 'proc.archived' : 'proc.none.order'));
      return;
    }
    window.location.href = `/index.html#/operations/i/${hit.key}`;
  } catch (e) {
    said(giveUp.signal.aborted ? t('proc.slow') : `${t('portal.failed')} ${e.message}`);
  } finally {
    clearTimeout(timer);
  }
}

// followNote is what the link last found for this order, or nothing where it was
// never pressed. It sits under the button, which is what makes the press visible.
function followNote(order) {
  const said = state.following.get(order.id);
  return said ? el('div', { class: 'muted follow-note' }, said) : null;
}

// lineKey is what one position is called, mirroring the server's own rule
// (ADR-0384): the
// product, and the shape of it where one was chosen.
//
// Written out here rather than read from the order, because a line placed before
// positions had names carries no key of its own and the rule reproduces it exactly.
// A page that approximated it would offer routes the server refuses, which is how
// a reader learns to distrust a screen.
function lineKey(line) {
  return line.variantId ? `${line.itemId}#${line.variantId}` : line.itemId;
}

// lineLabel is that position in words: the product as the catalogue names it, and
// the shape beside it where the product comes in more than one.
//
// Falls back to the ids, and for the reason the person column does: a product
// withdrawn from the catalogue, or an order against a release this reader's
// catalogue no longer carries, still has to say what was ordered.
function lineLabel(line) {
  const by = itemsById(state.release || {});
  const item = by[line.itemId];
  const name = item ? textOf(item.texts, line.itemId) : line.itemId;
  if (!line.variantId) return name;
  const shape = (item && (item.variants || []).find((v) => v.id === line.variantId));
  return `${name} — ${shape ? textOf(shape.texts, line.variantId) : line.variantId}`;
}

function deriveStatus(order) {
  const lines = order.lines || [];
  let provisioned = 0;
  let cancelled = 0;
  for (const l of lines) {
    const terminal = l.status === 'blocked'
      ? !!l.terminallyBlocked
      : ['done', 'skipped', 'rejected', 'abandoned', 'cancelled', 'returned'].includes(l.status);
    if (!terminal) return 'order.running';
    if (l.status === 'done' || l.status === 'skipped') provisioned++;
    if (l.status === 'cancelled') cancelled++;
  }
  if (!lines.length || provisioned === lines.length) return 'order.completed';
  // Withdrawn in full is its own answer. "Not fulfilled" is what an order says
  // when it tried and did not manage, and telling somebody that about their own
  // cancellation invites them to ask why it failed.
  if (cancelled === lines.length) return 'order.cancelled';
  return provisioned ? 'order.partial' : 'order.unfulfilled';
}

// cancellable mirrors the server's rule: what can still be taken back is what has
// not happened yet. Shown rather than hidden when nothing can be — an order that
// offers no way to withdraw it, with no word about why, is the case that produces
// the telephone call this whole thing exists to prevent.
function cancellable(order) {
  return (order.lines || []).some((l) => l.status === 'pending' || l.status === 'blocked');
}

// returnable mirrors the server's rule as far as the page can see it: a line is
// held, and nothing still held requires it. The second half is why this reads the
// order's own requires rather than only the line — giving back an account under a
// laptop that still uses it is the mistake the guard exists for, and offering the
// button would invite it before the server refused it.
function returnable(order, line) {
  if (line.status !== 'done' && line.status !== 'returnFailed') return false;
  // Held is wider than "done": a revocation only asked for has not happened, and
  // one that failed plainly has not. Either way the access is still there, and
  // offering to revoke what is underneath would invite the mistake the server
  // then refuses.
  const held = new Set((order.lines || [])
    .filter((l) => ['done', 'returning', 'returnFailed'].includes(l.status))
    .map((l) => l.itemId));
  const requires = order.requires || {};
  for (const [dependent, needs] of Object.entries(requires)) {
    if (held.has(dependent) && (needs || []).includes(line.itemId)) return false;
  }
  return true;
}

// giveBack starts a line's deprovisioning, then reloads. It asks first: revoking
// an access somebody has been using is the one thing on this page with a
// consequence outside Atlas, and a mis-click deletes an account.
async function giveBack(order, line) {
  if (state.busy) return;
  if (!window.confirm(t('portal.return.sure'))) return;
  state.busy = true;
  state.error = '';
  render();
  try {
    await api(`/api/v1/orders/${encodeURIComponent(order.id)}/lines/${encodeURIComponent(lineKey(line))}/return`,
      { method: 'POST' });
    state.busy = false;
    await load();
  } catch (e) {
    state.busy = false;
    state.error = `${t('portal.failed')} ${e.message}`;
    render();
  }
}

// cancel withdraws an order, then reloads: what the server did to each line is
// what the page then shows, rather than what the page assumed it would do.
async function cancel(order) {
  if (state.busy) return;
  state.busy = true;
  state.error = '';
  render();
  try {
    await api(`/api/v1/orders/${encodeURIComponent(order.id)}/cancel`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({}),
    });
    state.busy = false;
    await load();
  } catch (e) {
    state.busy = false;
    state.error = `${t('portal.failed')} ${e.message}`;
    render();
  }
}

// --- The orders table --------------------------------------------------------
//
// The mockups put a search field directly above the column it searches, which is
// the arrangement that needs no legend: what a field filters is the thing it is
// sitting on.

// personName is who a principal id belongs to, or the id where nothing knows.
//
// The fallback is the point. A deleted account, a directory that would not load, a
// recipient from before this tenancy — the row still has to say who, and the id is
// the honest answer to "the name is no longer known". An empty cell would read as
// the column being broken.
function personName(id) {
  if (!id) return '';
  return state.directory.get(id) || id;
}

// matchesFilters reports whether one order survives the column searches. Case
// blind and substring, because somebody typing "gen" into a status field means
// "genehmigt" and should not have to know how it is spelled internally.
function matchesFilters(o) {
  const f = state.filters;
  const like = (hay, needle) => !needle
    || String(hay || '').toLowerCase().includes(needle.toLowerCase());
  const placed = new Date(o.createdAt / 1e6).toLocaleDateString(locale);
  // The name the column shows *and* the id behind it: somebody who pasted an id
  // meant to find that row, and somebody who typed a name meant the same.
  return (like(personName(o.recipient), f.person) || like(o.recipient, f.person))
    && like(placed, f.date)
    && like(o.id, f.order)
    && like(t(deriveStatus(o)), f.status)
    // The organisation column has no source; a filter over nothing must not
    // silently hide every row, so an empty field is the only one that matches.
    && (!f.company);
}

function filterCell(key, label) {
  return el('td', {},
    el('input', {
      type: 'search', value: state.filters[key], placeholder: label, 'aria-label': label,
      oninput: (e) => {
        state.filters[key] = e.target.value;
        // Re-rendering replaces the node, so the caret would jump to the end of a
        // field somebody is editing in the middle. Patch the rows and leave the
        // filter row alone.
        repaintOrderRows();
      },
    }));
}

let orderRowsNode = null;

function repaintOrderRows() {
  if (!orderRowsNode) return;
  // The same two steps render() takes, and for the same reason: a correction's
  // form is inside these rows, it does not survive its container being replaced,
  // and typing in a column filter must not empty a cost centre somebody is in the
  // middle of fixing.
  harvest();
  orderRowsNode.replaceChildren(...orderRowBodies());
  mountConfigForms();
}

function orderRowBodies() {
  const rows = state.orders.filter(matchesFilters);
  if (!rows.length) {
    return [el('tr', {}, el('td', { colspan: '6', class: 'muted' }, t('tbl.noMatch')))];
  }
  return rows.map((o) => el('tr', {},
    // Organisation: rendered because the layout has the column, empty because an
    // order carries no organisation. The note under the table says so once,
    // rather than each row implying the data went missing.
    el('td', { class: 'muted org' }),
    el('td', { 'data-label': t('tbl.person') }, personName(o.recipient)),
    el('td', { 'data-label': t('tbl.placed') }, new Date(o.createdAt / 1e6).toLocaleDateString(locale)),
    el('td', { 'data-label': t('tbl.order') }, o.id,
      // An order in front of somebody because they hold one of its tasks, not
      // because it is theirs. Said, so nobody mistakes it for one they placed.
      o.held ? el('div', { class: 'muted' }, t('task.held')) : null),
    el('td', {},
      !o.held && cancellable(o)
        ? el('button', {
          class: 'sq',
          'aria-label': t('portal.cancel'),
          title: t('portal.cancel'),
          disabled: state.busy,
          onclick: () => cancel(o),
        }, 'X')
        : null,
      // Offered to whoever may follow it. An ordinary orderer reads the positions
      // below instead, which is the same question answered out of the order's own
      // record and without an operations surface.
      state.mayFollowProcess
        ? el('button', {
          class: 'linkish',
          title: t('proc.open'),
          disabled: state.busy,
          onclick: () => followProcess(o),
        }, t('proc.open'))
        : null,
      // And what it found, under the button that asked. The one answer that is not
      // drawn here is the one that navigates away.
      followNote(o)),
    el('td', { 'data-label': t('tbl.status') },
      t(deriveStatus(o)),
      el('ul', { class: 'lines' }, (o.lines || []).map((l) => el('li', {},
        el('span', { class: `dot ${l.status}` }),
        ' ', lineLabel(l), ' \u2014 ', t(`status.${l.status}`),
        l.blockedBy && l.blockedBy.length
          ? el('span', { class: 'muted' }, ` (${t('portal.blockedBy')}: ${l.blockedBy.join(', ')})`) : null,
        l.reason ? el('span', { class: 'muted' }, ` (${t('portal.reason')}: ${l.reason})`) : null,
        amendedNote(l),
        !o.held && returnable(o, l)
          ? el('button', {
            class: 'linkish',
            disabled: state.busy,
            onclick: () => giveBack(o, l),
          }, state.busy ? t('portal.returning') : t('portal.return'))
          : null,
        !o.held && withdrawable(l)
          ? el('button', {
            class: 'linkish',
            disabled: state.busy,
            onclick: () => withdrawLine(o, l),
          }, state.busy ? t('line.withdrawing') : t('line.withdraw'))
          : null,
        // No link into a process here. A position row carried two — where the
        // position stands, and the position's own instance — and neither was read
        // as useful by the people this page is for: the status beside the name
        // already answers "what is happening to my laptop" out of the order's own
        // record. The order's link above is the one that stayed.
        !o.held && correctable(l)
          ? el('button', {
            class: 'linkish',
            disabled: state.busy,
            onclick: () => {
              harvest();
              state.editing = state.editing === `${o.id}|${lineKey(l)}` ? '' : `${o.id}|${lineKey(l)}`;
              state.configError = '';
              render();
            },
          }, t('line.details'))
          : null,
        detailsPanel(o, l),
        taskList(o, l)))))));
}

// --- The open tasks of a position (ADR-0416) --------------------------------
//
// "Wartet" says a position is not done; it does not say on whom. Under each
// position stands every open task of the processes working it, whom it waits for,
// and — for whoever holds it — the task's own form, answered here rather than in a
// second window.
//
// The server decides all three: which tasks there are (from the instances the
// order records on the position, never from a search), whom they wait for (by the
// approval rule where the task is an approval, which is how an orderer knows it),
// and whether this reader may answer (the task route's own gate). The page draws.

// loadTasks reads the tasks, and the orders the reader holds a task in, which are
// listed beside the reader's own. A failure leaves the orders as they were: a
// missing task list is a list missing, not an order missing.
async function loadTasks() {
  state.tasks = [];
  state.tasksTruncated = false;
  try {
    const got = await api('/api/v1/shop/tasks');
    state.tasks = (got && got.tasks) || [];
    state.tasksTruncated = !!(got && got.truncated);
    const own = new Set(state.orders.map((o) => o.id));
    for (const o of (got && got.orders) || []) {
      if (!own.has(o.id)) state.orders.push({ ...o, held: true });
    }
    state.orders.sort((a, b) => (b.createdAt || 0) - (a.createdAt || 0));
  } catch { /* no task list; the orders still stand */ }
}

// tasksOf are the open tasks of one position.
function tasksOf(order, line) {
  const key = lineKey(line);
  return (state.tasks || []).filter((x) => x.orderId === order.id && x.positionId === key);
}

// holderText is whom a task waits for, as the reader knows it.
function holderText(task) {
  const h = task.holder || {};
  const by = {
    fixed: 'task.by.fixed', role: 'task.by.role', superior: 'task.by.superior',
    person: 'task.waits.person', group: 'task.waits.group',
  }[h.kind];
  if (!by) return t('task.open');
  return h.name ? `${t(by)} ${h.name}` : t(by);
}

// taskFormKey is where a task's answers are kept between redraws, beside the
// basket's: the same mounting and the same harvesting serve both.
const taskFormKey = (task) => `task:${task.key}`;

function taskList(order, line) {
  const list = tasksOf(order, line);
  if (!list.length) return null;
  return el('ul', { class: 'tasks' }, list.map((task) => el('li', { 'data-task': String(task.key) },
    el('span', { class: 'task-name' }, task.name),
    ' \u00b7 ', el('span', { class: 'task-holder' }, holderText(task)),
    task.dueDate
      ? el('span', { class: 'muted' },
        ` \u00b7 ${t('task.due')} ${new Date(task.dueDate).toLocaleDateString(locale)}`)
      : null,
    task.mayWork
      ? el('button', {
        class: 'linkish', disabled: state.busy,
        onclick: () => toggleTask(task),
      }, state.taskOpen === String(task.key) ? t('line.close') : t('task.work'))
      : null,
    taskPanel(task))));
}

// toggleTask opens a task's form, seeded with what the task already holds, or
// closes it. Seeded because a form that opened empty would answer a question with
// nothing the process had already filled in.
async function toggleTask(task) {
  harvest();
  const key = taskFormKey(task);
  if (state.taskOpen === String(task.key)) {
    state.taskOpen = '';
    delete state.config[key];
    render();
    return;
  }
  state.taskOpen = String(task.key);
  state.taskError = '';
  if (task.formId && !state.config[key]) {
    try {
      const scope = task.elementInstanceKey || task.processInstanceKey;
      state.config[key] = (await api(`/api/v1/instances/${scope}/variables`)) || {};
    } catch {
      state.config[key] = {};
    }
    // And the order's people by name, for the form to say for whom. They are not
    // form fields, so answering never writes a name into the process: the order and
    // the process keep ids (ADR-0314), and a name is only for the reader.
    const order = state.orders.find((o) => o.id === task.orderId);
    if (order) {
      const vars = state.config[key];
      if (vars.recipientName == null) vars.recipientName = personName(order.recipient);
      if (vars.ordererName == null) vars.ordererName = personName(order.orderer);
    }
  }
  render();
}

function taskPanel(task) {
  if (state.taskOpen !== String(task.key)) return null;
  const key = taskFormKey(task);
  return el('div', { class: 'card cfg', style: 'margin-top:8px' },
    state.taskError ? el('p', { class: 'error' }, state.taskError) : null,
    task.formId
      ? el('div', { 'data-configkey': key, 'data-formid': task.formId },
        el('p', { class: 'note' }, t('cfg.loading')))
      : el('p', { class: 'note' }, t('task.noForm')),
    el('div', { class: 'row', style: 'margin-top:10px' },
      el('button', {
        class: 'primary', disabled: state.busy,
        onclick: () => completeTask(task),
      }, state.busy ? t('task.completing') : t('task.complete'))));
}

// completeTask answers a task with what its form holds. The form checks itself
// first, as the Tasks app's does: a required field left empty is the reader's to
// fill in, not the server's to refuse.
async function completeTask(task) {
  const key = taskFormKey(task);
  let data = {};
  const form = mounted.get(key);
  if (form) {
    let result;
    try { result = form.submit(); } catch { result = { data: {}, errors: {} }; }
    if (result.errors && Object.keys(result.errors).length) {
      state.taskError = t('cfg.invalid');
      render();
      return;
    }
    data = result.data || {};
  } else if (task.formId) {
    state.taskError = t('task.formFailed');
    render();
    return;
  }
  state.busy = true;
  state.taskError = '';
  render();
  try {
    await api(`/api/v1/tasks/${task.key}/complete`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ variables: data }),
    });
    state.taskOpen = '';
    delete state.config[key];
    mounted.delete(key);
    await load();
  } catch (e) {
    state.taskError = `${t('portal.failed')} ${e.message}`;
  } finally {
    state.busy = false;
    render();
  }
}

// --- Changing one position ---------------------------------------------------
//
// Two acts, and the page keeps them as far apart as the server does
// (ADR-0359). Withdrawing a
// position takes it back; correcting the details changes what was recorded about
// it and never what it is. Ordering something else is neither, and the page does
// not pretend otherwise: give it back and order the other thing.

// withdrawable mirrors the server's rule so the page does not offer what it will
// refuse. Two things: the status must be one that has not happened yet, and the
// position must not be one its whole always carries — the basket does not let
// anybody deselect such a part, and offering it here would be the same rule
// holding in one screen and not the other.
function withdrawable(line) {
  return !line.integral && (line.status === 'pending' || line.status === 'blocked');
}

// correctable mirrors the other half. A position that asks for no details has none
// to correct; one being provisioned now is refused until its process has finished;
// a closed one delivered nothing under these details.
function correctable(line) {
  if (!line.configForm) return false;
  return ['pending', 'blocked', 'done', 'returning', 'returnFailed'].includes(line.status);
}

async function withdrawLine(order, line) {
  state.busy = true;
  state.error = '';
  render();
  try {
    await api(`/api/v1/orders/${encodeURIComponent(order.id)}/lines/${encodeURIComponent(lineKey(line))}/cancel`,
      { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' });
    await load();
  } catch (e) {
    state.error = `${t('portal.failed')} ${e.message}`;
  } finally {
    state.busy = false;
    render();
  }
}

async function saveDetails(order, line) {
  const key = amendKey(order.id, lineKey(line));
  const form = mounted.get(key);
  if (form) {
    const { errors } = form.submit();
    if (errors && Object.keys(errors).length) {
      state.configError = key;
      render();
      return;
    }
  }
  harvest();
  state.busy = true;
  state.error = '';
  state.configError = '';
  render();
  try {
    await api(`/api/v1/orders/${encodeURIComponent(order.id)}/lines/${encodeURIComponent(lineKey(line))}/details`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ config: state.config[key] || {} }),
      });
    state.editing = '';
    delete state.config[key];
    await load();
  } catch (e) {
    state.error = `${t('portal.failed')} ${e.message}`;
  } finally {
    state.busy = false;
    render();
  }
}

// detailsPanel is the correction, open under the position it belongs to.
function detailsPanel(order, line) {
  const key = amendKey(order.id, lineKey(line));
  if (state.editing !== `${order.id}|${lineKey(line)}`) return null;
  // Seeded from what the order carries, so the form opens on what was answered
  // rather than empty — a correction is an edit, not a second filling-in.
  if (!state.config[key]) state.config[key] = { ...(line.config || {}) };
  return el('div', { class: 'card cfg', style: 'margin-top:8px' },
    state.configError === key ? el('p', { class: 'error' }, t('cfg.invalid')) : null,
    el('div', { 'data-configkey': key, 'data-formid': line.configForm },
      el('p', { class: 'note' }, t('cfg.loading'))),
    el('div', { class: 'row', style: 'margin-top:10px' },
      el('button', {
        class: 'primary', disabled: state.busy,
        onclick: () => saveDetails(order, line),
      }, state.busy ? t('line.saving') : t('line.save')),
      el('button', {
        disabled: state.busy,
        onclick: () => {
          harvest();
          state.editing = '';
          state.configError = '';
          delete state.config[key];
          render();
        },
      }, t('line.close'))));
}

// amendedNote says a position's details were corrected after it was held, and what
// they said before. Kept on the page rather than only in the record: somebody
// reading their own order should not have to ask why the cost centre changed.
function amendedNote(line) {
  const list = line.amendments || [];
  if (!list.length) return null;
  const was = list.map((a) => Object.entries(a.was || {})
    .map(([k, v]) => `${k}: ${v}`).join(', ')).filter(Boolean);
  return el('span', { class: 'muted' },
    ` (${t('line.amended')}${was.length ? `, ${t('line.amendedFrom')} ${was.join(' / ')}` : ''})`);
}

function renderOrders() {
  if (!state.orders.length) {
    return el('div', { class: 'empty' }, el('p', {}, t('portal.noOrders')));
  }
  orderRowsNode = el('tbody', {}, orderRowBodies());
  return el('div', {},
    el('div', { class: 'tablewrap' },
      el('table', { class: 'table orders' },
        el('thead', {},
          el('tr', { class: 'filters' },
            filterCell('company', t('tbl.searchCompany')),
            filterCell('person', t('tbl.searchPerson')),
            filterCell('date', t('tbl.searchDate')),
            filterCell('order', t('tbl.searchOrder')),
            el('td', {}),
            filterCell('status', t('tbl.searchStatus'))),
          el('tr', {},
            el('th', {}, t('tbl.company')),
            el('th', {}, t('tbl.person')),
            el('th', {}, t('tbl.placed')),
            el('th', {}, t('tbl.order')),
            el('th', {}, ''),
            el('th', {}, t('tbl.status')))),
        orderRowsNode)),
    state.tasksTruncated ? el('p', { class: 'note' }, t('task.truncated')) : null,
    el('p', { class: 'note' }, t('note.noCompany')));
}

// --- What somebody currently holds ------------------------------------------
//
// Read from the inventory, not assembled from the orders on this page: the order
// that granted a right is deleted by retention long before the right ends
// (ADR-0312), so a list built from orders would start losing services on the
// ninetieth day.
// depthOf is how far down the containment graph an item sits: 0 for something
// nothing contains, 1 for its parts, and so on. It is what puts a held service
// in the column it belongs to rather than all of them in one list.
//
// Shortest path wins, because a part can belong to more than one whole and the
// column it reads best in is the highest place it appears.
function depthOf(release, id) {
  const parentOf = new Map();
  for (const [whole, parts] of Object.entries(release.includes || {})) {
    for (const part of parts) if (!parentOf.has(part)) parentOf.set(part, whole);
  }
  for (const [whole, parts] of Object.entries(release.options || {})) {
    for (const part of parts) if (!parentOf.has(part)) parentOf.set(part, whole);
  }
  let d = 0;
  let at = id;
  const seen = new Set();
  while (parentOf.has(at) && !seen.has(at)) {
    seen.add(at);
    at = parentOf.get(at);
    d += 1;
    if (d > 8) break;
  }
  return d;
}

// ownerAmong returns a function naming, for any product, the offering among
// `roots` it hangs under — or '' when it hangs under none of them.
//
// Up through what includes it and what offers it, both, because a basket groups by
// belonging and an option belongs to its offering exactly as much as a part does.
// That is a different question from the level, and depthOf is not asked it: an
// option is a service by depth whichever offering is in the basket, but which
// line it sits on depends on which one is.
//
// Breadth-first and every parent rather than the first one depthOf keeps. A part
// offered by two products — one case for two phones — belongs to whichever of
// them is in this basket, and the first parent in the release may be the one that
// is not. Ties go to the root nearest in the graph, then to the one the release
// lists first, so the same basket always draws the same lines.
function ownerAmong(release, roots) {
  const parents = new Map();
  const link = (whole, part) => {
    if (!parents.has(part)) parents.set(part, []);
    parents.get(part).push(whole);
  };
  for (const [whole, parts] of Object.entries(release.includes || {})) parts.forEach((p) => link(whole, p));
  for (const [whole, parts] of Object.entries(release.options || {})) parts.forEach((p) => link(whole, p));
  return (id) => {
    const seen = new Set([id]);
    let level = [id];
    while (level.length) {
      const next = [];
      for (const at of level) {
        for (const up of parents.get(at) || []) {
          if (roots.has(up)) return up;
          if (!seen.has(up)) { seen.add(up); next.push(up); }
        }
      }
      level = next;
    }
    return '';
  };
}

// headingsHeld is the Kategorie or Produktgruppe column of what somebody holds,
// read off the same products the catalogue reads it off.
//
// **Off the root, not off the held item.** Both strings are attributes of the
// offering (ADR-0383): the cascade collects them from the products nothing
// contains, so a service two edges down has never had a heading of its own to
// carry. Read directly from what a person holds, this column showed "Ohne
// Kategorie" for every service they have while the catalogue showed real headings
// for the same things — the two sides of the shop disagreeing about one field,
// which is exactly what ADR-0360 promised they would not do.
//
// So each held id is resolved to the product it belongs to and the heading is read
// there, which is the same answer the person saw when they ordered it.
//
// Sorted and bucketed like the catalogue's own two columns, for the reasons given
// at categoriesOf: there is nothing on a string to sort by, and the bucket is last
// and only appears when something is in it.
function headingsHeld(rel, by, ids, field) {
  return headingsOf(ids.map((id) => by[rootOf(rel, id)] || {}), field);
}

function renderServices() {
  const rel = state.release || {};
  const by = itemsById(rel);
  const ids = [...state.held.keys()];
  if (!ids.length) {
    return el('div', { class: 'empty' }, el('p', {}, t('services.none')));
  }
  // Which order granted each right, where one on this page still does — that is
  // what makes a return possible from here at all. It is deliberately not how the
  // *list* is built: the inventory is, because the order behind a right is
  // deleted by retention long before the right ends (ADR-0312), and a list
  // assembled from orders would start losing services on the ninetieth day.
  const lineOf = new Map();
  for (const o of state.orders) {
    for (const l of o.lines || []) lineOf.set(l.itemId, { order: o, line: l });
  }

  const row = (id) => {
    const found = lineOf.get(id);
    const can = found && returnable(found.order, found.line);
    return cell({
      text: textOf((by[id] || {}).texts, id),
      trail: [
        can
          ? el('button', {
            class: 'sq',
            'aria-label': t('act.cancelLines'),
            title: t('act.cancelLines'),
            disabled: state.busy,
            onclick: () => giveBack(found.order, found.line),
          }, 'X')
          : null,
        infoButton(id),
      ],
    });
  };

  // Laid out across the same levels the catalogue and the basket use, from the same
  // rule, so somebody reading what they hold sees it under the name they ordered it
  // under. This was a third derivation of the level, and it disagreed with the
  // other two about a product that has no parts.
  const at = (level) => ids.filter((id) => levelOf(rel, id) === level);

  return el('div', {},
    // No data-step: nothing in these columns is chosen, so a narrow screen stacks
    // them rather than stepping through them (ADR-0417).
    el('div', { class: 'cascade' },
      el('div', { class: 'col' },
        el('div', { class: 'colhead' }, t('col.category')),
        // The headings of what this person actually holds, not the whole
        // catalogue's: this screen answers "what do I have", and a heading with
        // nothing of theirs under it would be a column of other people's shelves.
        headingsHeld(rel, by, ids, 'category')
          .map((c) => cell({ text: c.text || t('cat.none') }))),
      // The product group beside the heading, read off what this person holds for
      // the same reason the heading is: this screen answers "what do I have".
      el('div', { class: 'col' },
        el('div', { class: 'colhead' }, t('col.group')),
        headingsHeld(rel, by, ids, 'productGroup')
          .map((g) => cell({ text: g.text || t('group.none') }))),
      el('div', { class: 'col' },
        el('div', { class: 'colhead' }, t('col.offering')),
        at('offering').map(row)),
      el('div', { class: 'col' },
        el('div', { class: 'colhead' }, t('col.service')),
        at('service').map(row))),
    state.info && by[state.info] ? el('div', { style: 'margin-top:16px' }, infoPanel(rel, by[state.info])) : null);
}

// The brand mark, and the order it is looked for in: the catalogue's own, then
// the operator's, then none at all.
//
// Nothing in the catalogue record says whether a mark exists — the file on the
// server is the fact, and a second copy of that fact is a second copy to be wrong
// after a restore that brought the record and not the image. So the page asks for
// the image and reads a 404 as "there is none", which is what the console already
// does with the instance's own (logo.js).
//
// The built-in Atlas glyph is deliberately *not* the last step, though the console
// falls back to it. This is a customer-facing page: when neither the catalogue nor
// the operator has a mark it shows none, rather than branding somebody's service
// catalogue with the name of the engine underneath it.
const INSTANCE_MARK = '/api/v1/settings/logo';
let mark = null;

function renderMark() {
  // No catalogue and no sign-in to make: nothing to show a mark for.
  //
  // A sign-in screen is the exception, and deliberately so. It asks for a password
  // while no catalogue is resolved — that is what it is for — and a page asking
  // for a password while saying nothing about who is asking is the shape of a
  // phishing page. So it carries the instance's own mark, from the endpoint that
  // is public precisely because a sign-in screen has to be able to read it.
  if (!state.catalog && !state.needSignIn) return null;
  if (!mark) {
    // Rendered through an <img> and never inlined, so a script inside an uploaded
    // SVG has no context to run in; the server serves it sandboxed as well.
    // Decorative: the heading beside it already names the catalogue, so a screen
    // reader that announced the mark too would read it twice.
    mark = el('img', { class: 'mark', alt: '', 'aria-hidden': 'true' });
    mark.addEventListener('error', () => {
      if (mark.src.endsWith(INSTANCE_MARK)) mark.hidden = true;
      else mark.src = INSTANCE_MARK;
    });
  }
  // Assigning src re-requests the image, and render runs on every repaint — so it
  // is assigned when the catalogue changes and not when the basket does.
  const want = state.catalog ? state.catalog.id : '';
  if (mark.dataset.for !== want) {
    mark.dataset.for = want;
    mark.hidden = false;
    mark.src = state.catalog
      ? `/api/v1/catalogs/${encodeURIComponent(state.catalog.id)}/logo`
      : INSTANCE_MARK;
  }
  return mark;
}

// --- The shell ---------------------------------------------------------------

// renderNav is the row every mockup screen carries: three destinations, the help
// affordance beside the first, and whoever is signed in at the far right.
function renderNav() {
  const go = (view) => () => {
    state.view = view;
    state.inBasket = false;
    state.info = '';
    render();
  };
  const link = (view, key) => el('button', {
    class: state.view === view && !state.inBasket ? 'navlink on' : 'navlink',
    'aria-current': state.view === view && !state.inBasket ? 'page' : null,
    onclick: go(view),
  }, t(key));

  return el('nav', { class: 'nav' },
    link('catalog', 'nav.catalog'),
    link('orders', 'nav.orders'),
    link('services', 'nav.services'),
    el('span', { class: 'spacer' }),
    el('span', { class: 'who' },
      // Whoever the order is for, named. A recipient that has been chosen, else
      // the person reading — and only where neither is known does it fall back to
      // saying "myself", which is what it said to everybody before: true, and true
      // of every reader alike, so it identified nobody.
      el('span', {}, whoLabel()),
      // The picture of whoever the label just named — the recipient when one was
      // picked out of the directory, else the reader. A recipient typed by hand is
      // not an id and has no picture; that falls back to the circle like any
      // account without one, which is the same answer and needs no special case.
      avatarNode(state.forWhom.trim() || state.meID)),
    // The round "?" goes to the handbook rather than opening a panel of its own:
    // the explaining is written there already, and a second copy would be a second
    // thing to keep true.
    //
    // It is the last thing in the row, past the person, and not beside the first
    // destination where the mockups drew it. There it read as a fourth
    // destination: a round button the same height as its neighbours, in the row
    // where everything else navigates the catalogue. At the far end it reads as
    // what it is — the corner that is about the reader rather than about what they
    // are reading.
    el('a', { class: 'help', href: '/handbuch.html', target: '_blank', rel: 'noopener',
      title: t('nav.help'), 'aria-label': t('nav.help'),
      style: 'display:flex;align-items:center;justify-content:center;text-decoration:none' }, '?'));
}

// avatarNode is the circle in the corner, and the picture once there is one
// (ADR-0368).
//
// The picture replaces the circle only after its bytes have arrived. Rendering an
// <img> straight away would put a browser's broken-image icon in the corner for
// every account without one — which is every account on the day this ships — and
// a 404 here is not a failure but the ordinary answer to "has this person chosen
// a picture".
function avatarNode(userID) {
  const circle = el('span', { class: 'avatar', 'aria-hidden': 'true' }, '\u25cb');
  const id = String(userID || '').trim();
  if (!id) return circle;
  const img = el('img', {
    class: 'avatar', alt: '',
    onload: () => { if (circle.isConnected) circle.replaceWith(img); },
  });
  img.src = `/api/v1/users/${encodeURIComponent(id)}/avatar`;
  return circle;
}

// whoLabel is the name in the corner: the chosen recipient, else the reader, else
// the word that names neither.
//
// The order matters and is not arbitrary. Once somebody is ordering in another
// person's name, *that* is the fact the corner has to carry — it is the thing that
// makes the next click place an order somebody else will hold, and it must not be
// behind the reader's own name.
function whoLabel() {
  return state.forWhomLabel.trim() || state.meName || t('for.self');
}

// --- Ordering in somebody else's name ---------------------------------------
//
// The mockups' "Bestellen für: [Person suchen]". It was a plain field, on the
// reasoning that a person search would answer for everybody in the estate and so
// amount to shipping an organisation chart.
//
// That reasoning was wrong, and it is worth saying why rather than quietly
// changing it: Atlas already serves exactly this list, to any authenticated
// caller, at GET /api/v1/principals — the directory every member and assignee
// picker reads (ADR-0073). It carries a type, an opaque id
// and a display name, and deliberately nothing else: no address, no roles, no
// reporting line. There is no hierarchy in it to disclose, which is what an
// organisation chart *is*. Refusing to use it here did not withhold anything; it
// only made this one field harder to use than every other picker in the product.
//
// What the field is still gated on is the role, because that is a different
// question — not who may be *seen*, but whose name an order may carry
// (ADR-0349).

// peopleMatching is the suggestion list for what has been typed so far.
//
// Over the display name and the id: somebody who knows the id types it, and
// somebody who does not types a name. Capped, because a list longer than a screen
// is not read, and because a query matching four hundred people is a query that
// has not narrowed anything yet.
function peopleMatching(q) {
  const needle = q.trim().toLowerCase();
  if (!needle) return [];
  const hits = state.people.filter((e) => e.name.toLowerCase().includes(needle)
    || e.id.toLowerCase().includes(needle));
  // Once the field holds exactly one person's name there is nothing left to
  // choose, and a list of one under the cursor is noise.
  if (hits.length === 1 && hits[0].name.toLowerCase() === needle) return [];
  return hits.slice(0, 20);
}

// What a keystroke in the recipient field redraws. Same reason as the catalogue
// search: a full render would replace the field being typed into and send the
// caret to the end of the name after every character.
let peopleNode = null;

function repaintPeople() {
  if (!peopleNode || !peopleNode.isConnected) return;
  paint(peopleNode, peopleBodies());
}

function peopleBodies() {
  if (!state.forWhomLabel.trim()) return [];
  const hits = peopleMatching(state.forWhomLabel);
  if (!hits.length) {
    // Not an error. A typed id, username or mail address resolves on the server
    // and will never appear in this list, so the message says what else works
    // rather than implying the name is wrong.
    return [el('p', { class: 'note' }, t('for.none'))];
  }
  return [el('div', { class: 'people' }, hits.map((e) => el('button', {
    class: 'person',
    // The field shows the name and the order carries the id. A display name is
    // not something the server can resolve, and an id is not something a person
    // can check — so each side gets the form it can use.
    onclick: () => { state.forWhom = e.id; state.forWhomLabel = e.name; render(); },
  }, e.name)))];
}

function renderForWhom() {
  // Not drawn at all for an account that may not use it. A field that answers 403
  // reads as a permission that failed rather than one somebody never had.
  if (!state.mayOrderForOthers) return null;
  peopleNode = el('div', {}, peopleBodies());
  return el('div', { class: 'forwhom' },
    el('label', { class: 'muted', for: 'forwhom' }, t('for.order')),
    el('div', { class: 'forwhomfield' },
      el('input', {
        id: 'forwhom', type: 'search', value: state.forWhomLabel,
        placeholder: t('for.search'), 'aria-label': t('for.order'),
        oninput: (e) => {
          // Both, because typing over a picked name un-picks it: what is sent must
          // never keep pointing at somebody whose name is no longer in the field.
          state.forWhomLabel = e.target.value;
          state.forWhom = e.target.value;
          repaintPeople();
        },
      }),
      peopleNode),
    state.forWhom.trim()
      ? el('button', {
        class: 'sq', title: t('for.clear'), 'aria-label': t('for.clear'),
        onclick: () => { state.forWhom = ''; state.forWhomLabel = ''; render(); },
      }, '\u2715')
      : null);
}

// renderActions is the row along the bottom. Which buttons it carries is the
// step, not the screen: the cascade offers the basket, the basket offers the
// order, and both offer the way back the mockups put on every screen.
function renderActions() {
  if (state.view !== 'catalog') {
    return el('div', { class: 'actions' },
      el('a', { class: 'backlink', href: '/index.html' }, t('act.back')));
  }
  const count = state.basket.size;
  return el('div', { class: 'actions' },
    el('button', {
      class: 'secondary',
      onclick: () => {
        if (state.inBasket) { state.inBasket = false; render(); return; }
        window.location.href = '/index.html';
      },
    }, t('act.back')),
    el('button', {
      class: 'secondary',
      disabled: !count || state.busy,
      onclick: () => { state.basket.clear(); state.inBasket = false; render(); },
    }, `${t('act.discard')} \u2715`),
    // No identity, no order — and the row says which, rather than carrying a
    // button that reaches the server and comes back refused. A control that is
    // offered and then fails teaches somebody that the page is broken; one that is
    // absent with a reason beside it teaches them what the mode is.
    state.canOrder
      ? (state.inBasket
        ? el('span', {},
          // Said rather than left to be discovered: a button that is disabled
          // without a sentence beside it reads as the page being broken.
          variantsMissing().length
            ? el('span', { class: 'muted', style: 'margin-right:8px' }, t('variant.missing'))
            : null,
          el('button', {
            class: 'primary',
            disabled: !count || state.busy || variantsMissing().length > 0,
            onclick: order,
          }, state.busy ? t('portal.ordering') : t('act.place')))
        : el('button', {
          class: 'primary',
          disabled: !count,
          onclick: () => { state.inBasket = true; state.info = ''; render(); },
        }, `${t('act.toBasket')}${count ? ` (${count})` : ''}`))
      : el('span', { class: 'muted', title: t('noid.hint') }, t('noid.title')));
}

function currentView() {
  if (state.view === 'orders') return renderOrders();
  if (state.view === 'services') return renderServices();
  return state.inBasket ? renderBasket() : renderCatalogue();
}

// paint replaces the page's children, dropping the ones that are not there.
//
// replaceChildren is not el(): it turns a non-node argument into a *text node*,
// so a `cond ? node : null` argument renders the word "null" on screen whenever
// the condition is false. This page has carried that since it was written — the
// error slot is conditional and there is usually no error, so a stray "null" sat
// under the header on every ordinary load.
//
// Filtering here rather than at each call site, because the next conditional
// child written the obvious way would reintroduce it.
function paint(root, ...children) {
  root.replaceChildren(...children.flat().filter((c) => c != null && c !== false));
}

function render() {
  const root = document.getElementById('app');
  if (!root) return;
  // Before the page is replaced, not after: a mounted form goes with its container,
  // and what was typed into it would go too.
  harvest();
  paint(root,
    el('header', {},
      el('div', { class: 'brand' },
        renderMark(),
        el('h1', {}, state.catalog ? textOf(state.catalog.texts, t('portal.title')) : t('portal.title'))),
      el('div', { class: 'headright' },
        // The way back. This page is reached from Atlas' own menu and from a link in
        // a mail, and it is a page of its own rather than a view of the shell — so
        // without this the only way out is the browser's back button, and a visitor
        // who arrived by link has no back to press.
        el('a', { class: 'backlink', href: '/index.html' }, '\u2190 ', t('portal.back')),
        // One button per language this catalogue is kept in, and none at all where
        // there is only one: a switch with a single position is a control that
        // says something can be changed and then cannot.
        el('div', { class: 'langs' }, offeredLocales().length > 1
          ? offeredLocales().map((l) => el('button', {
            class: l === locale ? 'lang on' : 'lang',
            onclick: () => setLocale(l),
          }, l.toUpperCase()))
          : null))),
    // The header stands on the sign-in screen too — the mark says who is asking,
    // and the language switch has to be reachable before the sign-in, not after
    // it: a German-speaking visitor meeting an English form is the case this
    // page's whole message catalogue exists to avoid. Everything below the header
    // needs a session, so below the header there is nothing but the sign-in.
    ...(state.needSignIn ? [renderSignIn()] : [
      renderNav(),
      state.error ? el('p', { class: 'error' }, state.error,
        ' ', el('button', { onclick: load }, t('portal.retry'))) : null,
      state.view === 'catalog' ? renderForWhom() : null,
      el('h2', {}, t(state.inBasket ? 'basket.title'
        : state.view === 'orders' ? 'nav.orders'
          : state.view === 'services' ? 'nav.services' : 'nav.catalog')),
      currentView(),
      renderActions(),
    ]));
  // After the page exists. Fire and forget: the containers say they are loading
  // until this finishes, and a failure to load the runtime leaves a sentence rather
  // than an empty box.
  mountConfigForms();
}

document.addEventListener('DOMContentLoaded', () => {
  document.documentElement.lang = locale;
  paintFromCache();
  render();
  load().catch((e) => {
    // A refusal is not a broken shop. The page is one people leave open, so the
    // session behind it runs out while it stands there, and the next load is the
    // same 401 the gate exists for — reported as a failure it is the original
    // defect one step later, with an HTTP status where the sign-in should be.
    if (e && e.status === 401) {
      state.needSignIn = true;
      state.signinError = 'signin.expired';
      forgetTheReader();
      loadSignInOptions().finally(render);
      return;
    }
    state.error = `${t('portal.failed')} ${e.message}`;
    render();
  });
});
