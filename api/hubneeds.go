package api

import "sort"

// Exported for the publisher's side of atlas hub (`atlas hub build`): a registry
// index names, for presentation, what a package's processes need from the
// installation, and a package built from loose models needs the portable key a
// server would give the application. Both are the server's own rules, so a
// sample installed from the hub and the same sample installed from the handbook
// land in the same application with the same needs named.

// PortableApplicationKey is the key a server derives for an application of this
// name when nothing else on it holds that key (ADR-0134).
func PortableApplicationKey(name string) string { return slugify(name, "application") }

// ProcessNeeds reads what the processes need from the installation: the Worker
// Types their tasks name a configured worker of, and the server constants they
// read. Both sorted, never nil. unread names the processes that do not compile.
// It compiles, so it never runs on the run loop.
func ProcessNeeds(processes map[string][]byte) (workerTypes, serverConstants, unread []string) {
	ids := make([]string, 0, len(processes))
	for id := range processes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	// One process at a time: needsOf merges workers by name, and two processes
	// of one package may name the same worker as different Worker Types — a
	// conflict the import plan reports, but both types are still needed.
	workerTypes, serverConstants = []string{}, []string{}
	seenType, seenConstant := map[string]bool{}, map[string]bool{}
	for _, id := range ids {
		needs, bad := needsOf([]needSource{{ID: id, XML: processes[id]}})
		unread = append(unread, bad...)
		for _, n := range needs {
			switch {
			case n.Kind == needWorker && !seenType[n.Type]:
				seenType[n.Type] = true
				workerTypes = append(workerTypes, n.Type)
			case n.Kind == needServerConstant && !seenConstant[n.Name]:
				seenConstant[n.Name] = true
				serverConstants = append(serverConstants, n.Name)
			}
		}
	}
	sort.Strings(workerTypes)
	sort.Strings(serverConstants)
	sort.Strings(unread)
	return workerTypes, serverConstants, unread
}
