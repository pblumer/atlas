// googlecontacts-ops.js — what the Modeler knows about the Google Contacts Worker's
// operations (ADR-0476): each operation's label, which values it
// needs and takes, what it returns, and the bound a listing takes by default and at most.
//
// It is a copy of connector/googlecontacts/catalog, the table the compiler and the
// worker read. A copy is unavoidable — the panel runs in a browser — so it is held to the
// Go table field by field by TestModelerKnowsEveryGoogleContactsOperation
// (api/googlecontacts_modeler_test.go): a row that differed would offer a field the
// deploy then refuses, or hide one the operation needs.
//
// One row per line, in the shape the test reads; keep it that way.
export const GOOGLE_CONTACTS_OPERATIONS = [
  { v: "find-contacts", l: "Find contacts (e-mail, phone or custom field)", area: "contact", needs: ["query"], takes: ["field", "group"], result: "array", max: [100, 1000] },
  { v: "get-contact", l: "Read contact", area: "contact", needs: ["contactId"], takes: [], result: "object", max: [0, 0] },
  { v: "search-contacts", l: "Search contacts (Google search)", area: "contact", needs: ["query"], takes: [], result: "array", max: [10, 30] },
  { v: "list-contacts", l: "List contacts", area: "contact", needs: [], takes: ["group"], result: "array", max: [500, 10000] },
  { v: "create-contact", l: "Create contact", area: "contact", needs: ["contact"], takes: [], result: "object", max: [0, 0] },
  { v: "update-contact", l: "Update contact", area: "contact", needs: ["contactId", "contact"], takes: [], result: "object", max: [0, 0] },
  { v: "delete-contact", l: "Delete contact", area: "contact", needs: ["contactId"], takes: [], result: "", max: [0, 0] },
  { v: "add-to-group", l: "Add to label", area: "group", needs: ["contactId", "group"], takes: [], result: "", max: [0, 0] },
  { v: "remove-from-group", l: "Remove from label", area: "group", needs: ["contactId", "group"], takes: [], result: "", max: [0, 0] },
  { v: "search-directory", l: "Search the organization's directory", area: "directory", needs: ["query"], takes: [], result: "array", max: [10, 500] },
];

const AREAS = { contact: "Contact", group: "Label", directory: "Directory" };

/** The row for an operation, or undefined. */
export function googleContactsOperation(name) {
  return GOOGLE_CONTACTS_OPERATIONS.find((o) => o.v === name);
}

/** The operation dropdown's options, the area in front of each label. */
export function googleContactsOperationOptions() {
  return GOOGLE_CONTACTS_OPERATIONS.map((o) => ({ v: o.v, l: `${AREAS[o.area]}: ${o.l}` }));
}

/** Whether the operation needs or takes a value — what decides whether its field shows. */
export function googleContactsUses(name, field) {
  const op = googleContactsOperation(name);
  return !!op && (op.needs.includes(field) || op.takes.includes(field));
}

/** The result variable's FEEL type for the type hints: object, array, or none. */
export function googleContactsResultType(name) {
  return googleContactsOperation(name)?.result || "";
}

/** The maxResults placeholder: the bound an operation gets when none is authored. */
export function googleContactsMaxPlaceholder(name) {
  const op = googleContactsOperation(name);
  return op && op.max[0] ? String(op.max[0]) : "";
}

/** The maxResults hint: what the bound means for this operation, and its ceiling. */
export function googleContactsMaxHint(name) {
  const op = googleContactsOperation(name);
  if (!op || !op.max[1]) return "";
  if (op.v === "search-contacts" || op.v === "search-directory") {
    return `How many matches the result holds, as Google ranks them; at most ${op.max[1]}. A search answers with its best matches, so more of them are simply not returned. Empty uses ${op.max[0]}.`;
  }
  return `Caps what may land in the result variable, at most ${op.max[1]}. A result holding more fails the task instead of being cut short, because a short list is a wrong answer a process would act on. Empty uses ${op.max[0]}.`;
}

/** What the result variable receives, for its hint. */
export function googleContactsResultShape(name) {
  switch (name) {
    case "get-contact":
      return "The contact as one object: id, displayName, givenName, familyName, email, emails, phone, phones, organization, jobTitle, department, address, addresses, birthday, notes, fields (the custom fields by name) and groups (the labels). A key the contact has no value for is null, a list empty.";
    case "find-contacts":
    case "search-contacts":
    case "list-contacts":
      return "A list of contacts, each the object Read contact returns; empty when nothing matched. FEEL lists are 1-based, so the first is =kunden[1].";
    case "search-directory":
      return "A list of people from the directory, in the contact's shape; their id is a profile, not a contact, so it cannot be updated or labelled.";
    case "create-contact":
    case "update-contact":
      return "The contact as Google holds it afterwards, with its id — keep it to address the contact later. Leave empty to discard it.";
  }
  return "This operation returns nothing, and the compiler refuses a result variable on it.";
}

/** The query field's hint for the operation that uses it. */
export function googleContactsQueryHint(name) {
  switch (name) {
    case "find-contacts":
      return "An e-mail address or a phone number, compared exactly (an address ignoring case, a number by its digits) — or, with a custom field named below, that field's value. Find reads the address book itself, so a contact created a minute ago is found. Usually a FEEL expression (fx), e.g. =anfrage.absender.";
    case "search-contacts":
      return "Free text Google matches as a prefix against names, e-mail addresses, phone numbers and organizations. Google answers from a search cache that can lag behind recent changes: to decide whether a customer exists, use Find contacts instead.";
    case "search-directory":
      return "A prefix of a name or an e-mail address of somebody in the Workspace organization. Needs the directory scope on the Worker's credential.";
  }
  return "";
}

/** A contact template to start from: the fields a create usually sets, as =expressions to replace. */
export function googleContactsContactPlaceholder(name) {
  if (name === "update-contact") {
    return '{\n  "email": "=formular.mail",\n  "fields": { "Kundennummer": "=kundennummer" }\n}';
  }
  return '{\n  "givenName": "=formular.vorname",\n  "familyName": "=formular.nachname",\n  "email": "=formular.mail",\n  "organization": "=formular.firma",\n  "groups": ["Kunden"]\n}';
}
