package api

import (
	"github.com/pblumer/atlas/api/sidecar"
)

// blueprintInstallation records a trusted package selected for installation on
// this server. Pending is deliberately the ONLY status this first BMP1 slice
// writes: a browser's successful import response is not proof that the source,
// application release and shop were all committed. Completion requires separate
// server-side reconciliation of the actual native stores (issue #1422).
//
// The application key is unique per installation (ADR-0499). A record is tied
// to the exact signed registry origin, signer, Blueprint release and digest;
// a different package cannot silently replace an in-flight installation.
type blueprintInstallation struct {
	ApplicationKey     string   `json:"applicationKey"`
	RegistryID         string   `json:"registryId"`
	RegistryOrigin     string   `json:"registryOrigin"`
	SignerFingerprint  string   `json:"signerFingerprint"`
	PublisherID        string   `json:"publisherId"`
	BlueprintID        string   `json:"blueprintId"`
	Version            string   `json:"version"`
	Digest             string   `json:"digest"`
	CatalogueIDs       []string `json:"catalogueIds"`
	Status             string   `json:"status"`
	RequestedBy        string   `json:"requestedBy,omitempty"`
	RequestedAt        int64    `json:"requestedAt"`
}

// blueprintInstallationStore is single-writer design-time metadata. Unlike
// portable application exports, it belongs to this particular installation,
// and therefore travels only in full-instance snapshots.
type blueprintInstallationStore = sidecar.Store[blueprintInstallation]

func newBlueprintInstallationStore(dir string) (*blueprintInstallationStore, error) {
	return sidecar.NewStore(dir, "blueprintinstallationstore", func(rec blueprintInstallation) string {
		return rec.ApplicationKey
	})
}
