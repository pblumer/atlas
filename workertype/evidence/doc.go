// Package evidence keeps and checks the evidence that one built-in Worker Type
// operation conforms to one technical capability contract (ADR-0483).
//
// A record binds four things a reviewer chose: a published contract, a Worker Type,
// one of its operations, and the suite whose cases check that operation against the
// contract. Everything else in it is derived from the module: the contract's digest,
// the Worker Type's version and job type, the packages every runtime path of the
// Worker Type goes through and their dependency digest, the suite's digest, the
// outcome of every case, and the approval rule in force. A record is current exactly
// when deriving it again gives the bytes on disk, which is what [Verify] checks and
// what [Renew] restores.
//
// The approval itself is not in the file. A string naming a reviewer proves nothing;
// a record counts as approved once it reached the protected main branch through a pull
// request the code owner approved or merged, and the file records only which rule that
// was. A record with a failed or unexercised case is still a current record: it says
// what the operation does not do, and it verifies no claim.
//
// Published contracts live under workertype/contracts and records under
// workertype/evidence/records; both paths belong to the code owner. This is build and
// review tooling: it runs the go command and the suites' fakes, and the engine never
// calls it.
package evidence
