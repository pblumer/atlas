// Package sharepointserver administers site collections on SharePoint Server
// (Subscription Edition, on-premises) on behalf of a BPMN service task
// (ADR-draft-sharepoint-server-worker).
//
// # Why it is not the sharepoint Worker Type
//
// connector/sharepoint speaks Microsoft Graph about lists and files in SharePoint
// Online, in the engine or on any host. SharePoint Server has no Graph, and its site
// administration is the farm's PowerShell, which only runs on a server of the farm. The
// two share a product name and no operation, so a target inside one Worker Type would
// be a type with two disjoint operation tables.
//
// # How a call runs
//
// The Worker starts Windows PowerShell for each job with a fixed program,
// [bootstrap]. The operation and every value travel to it as JSON in the environment,
// so no authored value is ever parsed as PowerShell, and the program dispatches on the
// operation to a fixed sequence of cmdlets: a model cannot name a cmdlet here.
//
// # Worker-only, and never supervised
//
// The account the worker runs as can administer every site of the farm, and the
// cmdlets exist only on a farm server, so neither the engine nor a worker Atlas
// supervises beside itself can do this work. The worker is installed on the farm by an
// operator.
//
// # Retries
//
// A job is delivered at least once, so the bootstrap is written for the second
// delivery: delete and restore that already happened succeed, changing an account that
// is already as asked changes nothing, and create-site resumes a site whose default
// groups were not made yet. A refusal a process can act on carries a code from [Codes].
package sharepointserver
