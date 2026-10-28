package blueprintregistry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"
)

// MaxPackageExpandedBytes caps the entire uncompressed tar stream, including
// headers and padding. Compressed bytes are already capped by VerifyArchive.
const MaxPackageExpandedBytes int64 = 200 << 20

const (
	maxPackageEntries   = 2000
	maxPackageManifest  = 2 << 20
	maxPackageCatalogue = 16 << 20
)

type archiveLimits struct {
	maxExpandedBytes int64
	maxEntries       int
}

// PackageFile is a verified tar member's path and uncompressed size.
// No member data is returned or written by inspection.
type PackageFile struct {
	Path      string
	SizeBytes int64
}

// PackageCatalogue lists one catalogue and the product IDs it offers.
type PackageCatalogue struct {
	ID    string   `json:"id"`
	Items []string `json:"items"`
}

// PackageProduct names a product and its authoritative home catalogue.
type PackageProduct struct {
	ID          string `json:"id"`
	HomeCatalog string `json:"homeCatalog"`
}

// PackageInspection describes the transport-level contents of a pinned
// Blueprint package. It does not prove that its BPMN, DMN or forms are valid.
type PackageInspection struct {
	ApplicationKey   string
	ApplicationName  string
	Files            []PackageFile
	Catalogues       []PackageCatalogue
	Products         []PackageProduct
	CataloguePublish bool
}

// InspectPackage verifies the exact signed archive bytes and inspects a bounded
// gzip tar in memory. Import planning, semantic validation, permissions and
// explicit publication remain the responsibility of existing atlas APIs.
func InspectPackage(release Release, compressed []byte) (PackageInspection, error) {
	return inspectPackageWithLimits(release, compressed, archiveLimits{
		maxExpandedBytes: MaxPackageExpandedBytes, maxEntries: maxPackageEntries,
	})
}

func inspectPackageWithLimits(release Release, compressed []byte, limits archiveLimits) (PackageInspection, error) {
	var result PackageInspection
	if err := VerifyArchive(release, compressed); err != nil {
		return result, fmt.Errorf("verify pinned Blueprint package: %w", err)
	}
	if limits.maxEntries <= 0 || limits.maxExpandedBytes < 1 {
		return result, errors.New("invalid package inspection limits")
	}
	src := bytes.NewReader(compressed)
	gz, err := gzip.NewReader(src)
	if err != nil {
		return result, fmt.Errorf("package is not a gzip archive: %w", err)
	}
	defer gz.Close()
	// A second gzip member must not hide content behind the tar EOF marker.
	// bytes.Reader implements io.ByteReader, so the gzip reader cannot silently
	// prefetch bytes from an appended second member when Multistream is false.
	gz.Multistream(false)
	bounded := &io.LimitedReader{R: gz, N: limits.maxExpandedBytes + 1}
	tr := tar.NewReader(bounded)
	seen := make(map[string]struct{})
	haveManifest := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return result, fmt.Errorf("invalid package tar header: %w", err)
		}
		if len(result.Files) >= limits.maxEntries {
			return result, errors.New("package contains too many files")
		}
		if hdr.Typeflag != tar.TypeReg {
			return result, fmt.Errorf("unsupported package tar entry type for %q", hdr.Name)
		}
		if !validPackagePath(hdr.Name) {
			return result, fmt.Errorf("unsafe package tar path %q", hdr.Name)
		}
		key := strings.ToLower(hdr.Name)
		if _, exists := seen[key]; exists {
			return result, fmt.Errorf("duplicate package path %q", hdr.Name)
		}
		seen[key] = struct{}{}
		if hdr.Size < 0 || hdr.Size > limits.maxExpandedBytes {
			return result, fmt.Errorf("package file %q exceeds expanded archive limit", hdr.Name)
		}
		if hdr.Name == "atlas.json" {
			if hdr.Size > maxPackageManifest {
				return result, errors.New("application manifest exceeds size limit")
			}
			manifest := make([]byte, int(hdr.Size))
			if _, err := io.ReadFull(tr, manifest); err != nil {
				return result, fmt.Errorf("read application manifest: %w", err)
			}
			if err := inspectManifest(manifest, &result); err != nil {
				return result, err
			}
			haveManifest = true
		} else if hdr.Name == "katalog.json" {
			if hdr.Size > maxPackageCatalogue {
				return result, errors.New("catalogue document exceeds preview limit")
			}
			catalogue := make([]byte, int(hdr.Size))
			if _, err := io.ReadFull(tr, catalogue); err != nil {
				return result, fmt.Errorf("read catalogue document: %w", err)
			}
			if err := inspectCatalogue(catalogue, &result); err != nil {
				return result, err
			}
		} else {
			if _, err := io.CopyN(io.Discard, tr, hdr.Size); err != nil {
				return result, fmt.Errorf("read package file %q: %w", hdr.Name, err)
			}
		}
		result.Files = append(result.Files, PackageFile{Path: hdr.Name, SizeBytes: hdr.Size})
	}
	// Force the gzip CRC/footer to be consumed. Tar readers stop at their EOF
	// blocks, potentially leaving an invalid footer or hidden appended bytes.
	var buffer [4096]byte
	for {
		n, err := bounded.Read(buffer[:])
		for _, b := range buffer[:n] {
			if b != 0 {
				return result, errors.New("nonzero trailing data after tar end")
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, fmt.Errorf("invalid gzip stream trailer: %w", err)
		}
	}
	if limits.maxExpandedBytes+1-bounded.N > limits.maxExpandedBytes {
		return result, errors.New("package expanded data exceeds size limit")
	}
	if src.Len() != 0 {
		return result, errors.New("unexpected compressed bytes after gzip member")
	}
	if !haveManifest {
		return result, errors.New("package does not carry atlas.json")
	}
	return result, nil
}

func validPackagePath(name string) bool {
	if name == "" || len(name) > 1024 || !utf8.ValidString(name) ||
		path.IsAbs(name) || path.Clean(name) != name ||
		name == "." || name == ".." || strings.HasPrefix(name, "../") ||
		strings.ContainsAny(name, "\\:%") {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func inspectManifest(raw []byte, result *PackageInspection) error {
	if !json.Valid(raw) {
		return errors.New("invalid application manifest JSON")
	}
	// Reject an ambiguous manifest before the later source importer can see it.
	// Duplicate JSON keys are legal to the standard decoder but not a trusted
	// description of a pinned package's identity.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := checkUniqueJSON(decoder, 0); err != nil {
		return fmt.Errorf("invalid application manifest: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("application manifest must contain exactly one JSON value")
	}
	var manifest struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("decode application manifest: %w", err)
	}
	if strings.TrimSpace(manifest.Key) == "" || strings.TrimSpace(manifest.Name) == "" {
		return errors.New("application manifest is missing its key or name")
	}
	result.ApplicationKey = manifest.Key
	result.ApplicationName = manifest.Name
	return nil
}

// inspectCatalogue reads only the bounded, signed package document to describe
// what is offered. Native import planning still owns semantics, authorization,
// bindings and publication decisions; this preview never writes to a store.
func inspectCatalogue(raw []byte, result *PackageInspection) error {
	if !json.Valid(raw) {
		return errors.New("invalid catalogue document JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := checkUniqueJSON(decoder, 0); err != nil {
		return fmt.Errorf("ambiguous catalogue document: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("catalogue document must contain exactly one JSON value")
	}
	var doc struct {
		Publish    bool               `json:"publish"`
		Catalogues []PackageCatalogue `json:"catalogs"`
		Products   []PackageProduct   `json:"products"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("decode catalogue document: %w", err)
	}
	catalogueIDs := make(map[string]struct{}, len(doc.Catalogues))
	for _, catalogue := range doc.Catalogues {
		if strings.TrimSpace(catalogue.ID) == "" {
			return errors.New("catalogue document contains an unnamed catalogue")
		}
		if _, found := catalogueIDs[catalogue.ID]; found {
			return fmt.Errorf("duplicate catalogue id %q", catalogue.ID)
		}
		catalogueIDs[catalogue.ID] = struct{}{}
	}
	productIDs := make(map[string]struct{}, len(doc.Products))
	for _, product := range doc.Products {
		if strings.TrimSpace(product.ID) == "" {
			return errors.New("catalogue document contains an unnamed product")
		}
		if _, found := productIDs[product.ID]; found {
			return fmt.Errorf("duplicate product id %q", product.ID)
		}
		productIDs[product.ID] = struct{}{}
	}
	result.Catalogues, result.Products, result.CataloguePublish = doc.Catalogues, doc.Products, doc.Publish
	return nil
}
