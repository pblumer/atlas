// Package googlecontacts integrates Google Contacts as a Worker Type (ADR-0203): a BPMN
// service task performs one operation on an address book — find a contact by its e-mail
// address, phone number or a custom field, read one, list a label's members, search,
// create, update or delete a contact, put a label on one or take it off — or searches
// the organization's directory, through the People API
// (ADR-0476).
//
// It is the Exchange Worker Type's shape (ADR-0462):
//
//   - The task creates a job carrying the reserved [compiler.GoogleContactsJobType].
//     [Resolve] evaluates the task's values against the instance's variables — engine
//     work, because only the engine has the scope chain — and [Run] performs the job on
//     a worker, off the processor goroutine and after fsync (invariants I1, I2).
//   - The kind is worker-only. The credential — a service account that reads a person's
//     address book through domain-wide delegation, or that person's own refresh token —
//     lives in the vault, reaches the supervised worker at spawn, and never enters the
//     engine, a model, an event or a variable. A model names a Worker and nothing else
//     about the account.
//   - The operation table is [catalog], which the compiler reads too, so what a deploy
//     refuses and what the worker runs are the same rows.
//
// (The BPMN attribute a task states its Worker in is spelled `connector="…"` and the
// extension element <atlas:googleContactsConnector>: the pre-ADR-0203 spelling every
// Worker Type's element keeps, and the one the Modeler's moddle drift guard recognizes.)
//
// # Find, not search, to ask "is this person known?"
//
// The People API's own search (searchContacts) answers from a cache Google asks to be
// warmed with an empty query first, matches prefixes of names, e-mail addresses, phone
// numbers and organizations, and lags behind changes. search-contacts uses it, warming
// the cache before every search, for what it is good at: a person's free text.
//
// A process deciding whether a customer exists must not use it. A contact created a
// minute ago may not be in the cache yet, and a process that reads "not found" then
// creates the customer a second time. find-contacts therefore reads the address book
// itself — exact and current, at the cost of reading it — and compares e-mail addresses
// (ignoring case), phone numbers (by their digits, as stored or in Google's
// international form) or one named custom field.
//
// # Writing a contact
//
// A model writes a flat contact ([catalog.WritableKeys]); [apply] decides what each key
// means to Google's person. A null value changes nothing, "" clears, a singular key
// (email, phone, address) changes the primary entry and keeps the others, and a plural
// key replaces the list. An update reads the contact's current version and writes it
// back under its etag; a contact that changed in between is read again, up to three
// times.
//
// # Delivery
//
// At-least-once. Every operation but create-contact is safe to repeat: an update
// writes the same values again, a delete of a contact already gone succeeds, a label
// put on twice is one label. A create that Google performed and whose job then failed
// to complete is performed again on the retry, and the address book then holds the
// contact twice — the People API has no request id a create is deduplicated on. A
// process that cannot tolerate that finds before it creates; Google Contacts' own
// "Merge & fix" view shows the duplicate when one slips through.
package googlecontacts
