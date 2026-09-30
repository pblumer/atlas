// Package ownerfile makes a file readable and writable by the account running this
// process and by nobody else, and checks that it is.
//
// On Unix that is mode 0600. Windows does not enforce Unix modes: os.Chmod there only
// toggles the read-only attribute, and a file keeps the access rights it inherits from
// its directory — which, depending on where the data directory lives, can include
// every local user. So on Windows the file gets an explicit DACL with a single entry
// for this account, protected from inheritance, which is what 0600 means there.
package ownerfile
