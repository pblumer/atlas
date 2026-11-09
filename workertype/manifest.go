// Package workertype defines the declarative Worker Type package contract from
// ADR-0207. It is design-time metadata: parsing and validation must never run in
// the processor hot path.
package workertype

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

const (
	// APIVersionV1Alpha1 is the first Worker Type manifest schema version.
	APIVersionV1Alpha1 = "atlas.io/v1alpha1"
	// KindWorkerType identifies a Worker Type manifest.
	KindWorkerType = "WorkerType"
	// WorkerProtocolV1 is the first public Worker protocol version supported by manifests.
	WorkerProtocolV1 = "v1"
	// RuntimeExternal describes a separately packaged Worker runtime.
	RuntimeExternal = "external"
	// RuntimeBundled describes a Worker runtime shipped as part of Atlas.
	RuntimeBundled = "bundled"
)

var (
	workerTypeIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)
	digestPattern       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	identifierPattern   = regexp.MustCompile(`^[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*$`)
)

// Manifest is a versioned declarative Worker Type package description.
type Manifest struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Metadata   Metadata `json:"metadata"`
	Spec       Spec     `json:"spec"`
}

// Metadata is the stable package identity plus presentation name.
type Metadata struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Name    string `json:"name"`
}

// Spec describes compatibility, authoring metadata and runtime packaging.
type Spec struct {
	AtlasCompatibility  string      `json:"atlasCompatibility"`
	WorkerProtocol      string      `json:"workerProtocol"`
	Runtime             Runtime     `json:"runtime"`
	JobTypes            []string    `json:"jobTypes"`
	Operations          []Operation `json:"operations"`
	ConfigurationSchema string      `json:"configurationSchema"`
	// Configuration is the configuration contract inline, as a JSON Schema object
	// whose writeOnly top-level properties are secrets (ADR-0535 §4). An installed
	// artifact carries no other file, so this is where its schema travels.
	Configuration   json.RawMessage `json:"configuration,omitempty"`
	ModelerTemplate string          `json:"modelerTemplate"`
	Artifact        *Artifact       `json:"artifact,omitempty"`
	// Artifacts are the runtime's executables, one per platform (ADR-0535 §1). A
	// manifest names either Artifact (one platform) or Artifacts, not both.
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

// Runtime declares how the Worker runtime is supplied.
type Runtime struct {
	Mode string `json:"mode"`
}

// Operation is one authorable operation implemented by a declared job type.
type Operation struct {
	ID           string            `json:"id"`
	JobType      string            `json:"jobType"`
	Capabilities []CapabilityClaim `json:"capabilities,omitempty"`
	// Idempotency and Effect say what a second delivery of one of its jobs does
	// (ADR-0538): both are required, so the
	// property is never assumed silently.
	Idempotency string `json:"idempotency"`
	Effect      string `json:"effect"`
}

// How an operation behaves when one of its jobs is delivered a second time.
const (
	// IdempotencyNatural: a second delivery is harmless by nature.
	IdempotencyNatural = "natural"
	// IdempotencyKey: the target filters duplicates by the job's idempotencyKey.
	IdempotencyKey = "key"
	// IdempotencyCheck: the worker checks before acting whether it already happened.
	IdempotencyCheck = "check"
	// IdempotencyNone: a second delivery can repeat the effect.
	IdempotencyNone = "none"
)

// What a repeated effect means.
const (
	// EffectReversible: a repeated effect can be undone or is tolerable.
	EffectReversible = "reversible"
	// EffectIrreversible: a repeated effect cannot be undone.
	EffectIrreversible = "irreversible"
)

// DuplicateRisk grades what a second delivery of op can do: "" for none, "notice" for
// a tolerable repeat, "warning" for an irreversible one, as
// ADR-0538 §3 grades it.
func (op Operation) DuplicateRisk() string {
	switch {
	case op.Idempotency != IdempotencyNone:
		return ""
	case op.Effect == EffectReversible:
		return "notice"
	default:
		return "warning"
	}
}

// Artifact identifies an executable runtime artifact by content digest.
type Artifact struct {
	Platform  string `json:"platform"`
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
}

// ParseJSON decodes and validates one Worker Type manifest. Unknown fields are
// rejected so package authors cannot believe Atlas is enforcing metadata it has
// silently ignored.
func ParseJSON(data []byte) (Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("decode worker type manifest: %w", err)
	}
	if err := ensureEOF(dec); err != nil {
		return Manifest{}, err
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode worker type manifest: multiple JSON values")
		}
		return fmt.Errorf("decode worker type manifest: %w", err)
	}
	return nil
}

// Validate checks the static Worker Type package contract without executing any
// package content or resolving runtime configuration.
func (m Manifest) Validate() error {
	if m.APIVersion != APIVersionV1Alpha1 {
		return fmt.Errorf("apiVersion must be %q", APIVersionV1Alpha1)
	}
	if m.Kind != KindWorkerType {
		return fmt.Errorf("kind must be %q", KindWorkerType)
	}
	if !workerTypeIDPattern.MatchString(m.Metadata.ID) {
		return fmt.Errorf("metadata.id %q must be a lower-case reverse-DNS identifier", m.Metadata.ID)
	}
	if _, err := parseVersion(m.Metadata.Version); err != nil {
		return fmt.Errorf("metadata.version: %w", err)
	}
	if strings.TrimSpace(m.Metadata.Name) == "" {
		return errors.New("metadata.name is required")
	}
	if _, err := parseConstraint(m.Spec.AtlasCompatibility); err != nil {
		return fmt.Errorf("spec.atlasCompatibility: %w", err)
	}
	if m.Spec.WorkerProtocol != WorkerProtocolV1 {
		return fmt.Errorf("spec.workerProtocol must be %q", WorkerProtocolV1)
	}
	if m.Spec.Runtime.Mode != RuntimeExternal && m.Spec.Runtime.Mode != RuntimeBundled {
		return fmt.Errorf("spec.runtime.mode must be %q or %q", RuntimeExternal, RuntimeBundled)
	}
	if strings.TrimSpace(m.Spec.ConfigurationSchema) == "" {
		return errors.New("spec.configurationSchema is required")
	}
	if strings.TrimSpace(m.Spec.ModelerTemplate) == "" {
		return errors.New("spec.modelerTemplate is required")
	}
	jobTypes := make(map[string]struct{}, len(m.Spec.JobTypes))
	if len(m.Spec.JobTypes) == 0 {
		return errors.New("spec.jobTypes must declare at least one job type")
	}
	for _, jt := range m.Spec.JobTypes {
		if strings.TrimSpace(jt) == "" {
			return errors.New("spec.jobTypes contains an empty job type")
		}
		if _, exists := jobTypes[jt]; exists {
			return fmt.Errorf("spec.jobTypes contains duplicate %q", jt)
		}
		jobTypes[jt] = struct{}{}
	}
	if len(m.Spec.Operations) == 0 {
		return errors.New("spec.operations must declare at least one operation")
	}
	operations := make(map[string]struct{}, len(m.Spec.Operations))
	for _, op := range m.Spec.Operations {
		if strings.TrimSpace(op.ID) == "" {
			return errors.New("spec.operations contains an empty operation id")
		}
		if _, exists := operations[op.ID]; exists {
			return fmt.Errorf("spec.operations contains duplicate id %q", op.ID)
		}
		operations[op.ID] = struct{}{}
		if _, exists := jobTypes[op.JobType]; !exists {
			return fmt.Errorf("spec.operations operation %q references undeclared job type %q", op.ID, op.JobType)
		}
		switch op.Idempotency {
		case IdempotencyNatural, IdempotencyKey, IdempotencyCheck, IdempotencyNone:
		case "":
			return fmt.Errorf("spec.operations operation %q must declare idempotency (natural, key, check or none): what a second delivery of one of its jobs does", op.ID)
		default:
			return fmt.Errorf("spec.operations operation %q idempotency %q must be natural, key, check or none", op.ID, op.Idempotency)
		}
		switch op.Effect {
		case EffectReversible, EffectIrreversible:
		case "":
			return fmt.Errorf("spec.operations operation %q must declare effect (reversible or irreversible): whether a repeated effect can be undone", op.ID)
		default:
			return fmt.Errorf("spec.operations operation %q effect %q must be reversible or irreversible", op.ID, op.Effect)
		}
		if err := validateCapabilityClaims(op.ID, op.Capabilities); err != nil {
			return err
		}
	}
	if len(bytes.TrimSpace(m.Spec.Configuration)) > 0 {
		if _, err := CompileConfiguration(m.Spec.Configuration); err != nil {
			return err
		}
	}
	if m.Spec.Artifact != nil && len(m.Spec.Artifacts) > 0 {
		return errors.New("spec.artifact and spec.artifacts are exclusive: name one platform or list them all")
	}
	if m.Spec.Runtime.Mode == RuntimeExternal && m.Spec.Artifact == nil && len(m.Spec.Artifacts) == 0 {
		return errors.New("spec.artifact or spec.artifacts is required for an external runtime")
	}
	if m.Spec.Artifact != nil {
		if err := validateArtifact("spec.artifact", *m.Spec.Artifact); err != nil {
			return err
		}
	}
	platforms := make(map[string]struct{}, len(m.Spec.Artifacts))
	for i, a := range m.Spec.Artifacts {
		if err := validateArtifact(fmt.Sprintf("spec.artifacts[%d]", i), a); err != nil {
			return err
		}
		if _, dup := platforms[a.Platform]; dup {
			return fmt.Errorf("spec.artifacts names platform %q twice", a.Platform)
		}
		platforms[a.Platform] = struct{}{}
	}
	return nil
}

// MediaTypeExecutable is the media type of a native executable artifact: one binary
// for one platform, launched as a process (ADR-0535 §1).
const MediaTypeExecutable = "application/vnd.atlas.worker.executable"

var platformPattern = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9]+$`)

func validateArtifact(field string, a Artifact) error {
	switch {
	case strings.TrimSpace(a.Platform) == "":
		return fmt.Errorf("%s.platform is required", field)
	case !platformPattern.MatchString(a.Platform):
		return fmt.Errorf("%s.platform %q must be os/arch, such as linux/amd64", field, a.Platform)
	case strings.TrimSpace(a.MediaType) == "":
		return fmt.Errorf("%s.mediaType is required", field)
	case !digestPattern.MatchString(a.Digest):
		return fmt.Errorf("%s.digest must be a lower-case sha256 digest", field)
	}
	return nil
}

// ArtifactFor returns the artifact the manifest declares for platform ("os/arch").
func (m Manifest) ArtifactFor(platform string) (Artifact, bool) {
	if a := m.Spec.Artifact; a != nil && a.Platform == platform {
		return *a, true
	}
	for _, a := range m.Spec.Artifacts {
		if a.Platform == platform {
			return a, true
		}
	}
	return Artifact{}, false
}

// CompatibleWith reports whether an Atlas semantic version satisfies the
// manifest's declared compatibility conjunction.
func (m Manifest) CompatibleWith(atlasVersion string) (bool, error) {
	version, err := parseVersion(atlasVersion)
	if err != nil {
		return false, fmt.Errorf("Atlas version: %w", err)
	}
	constraints, err := parseConstraint(m.Spec.AtlasCompatibility)
	if err != nil {
		return false, fmt.Errorf("spec.atlasCompatibility: %w", err)
	}
	for _, c := range constraints {
		if !c.matches(version) {
			return false, nil
		}
	}
	return true, nil
}

type version struct {
	major, minor, patch uint64
	pre                 []string
}

func parseVersion(raw string) (version, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return version{}, fmt.Errorf("%q is not a semantic version", raw)
	}
	core := raw
	if plus := strings.IndexByte(core, '+'); plus >= 0 {
		if plus == len(core)-1 || !identifierPattern.MatchString(core[plus+1:]) {
			return version{}, fmt.Errorf("%q is not a semantic version", raw)
		}
		core = core[:plus]
	}
	var pre []string
	if dash := strings.IndexByte(core, '-'); dash >= 0 {
		if dash == len(core)-1 || !identifierPattern.MatchString(core[dash+1:]) {
			return version{}, fmt.Errorf("%q is not a semantic version", raw)
		}
		pre = strings.Split(core[dash+1:], ".")
		for _, id := range pre {
			if id == "" || (isDigits(id) && len(id) > 1 && id[0] == '0') {
				return version{}, fmt.Errorf("%q is not a semantic version", raw)
			}
		}
		core = core[:dash]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}, fmt.Errorf("%q is not a semantic version", raw)
	}
	numbers := [3]uint64{}
	for i, part := range parts {
		if part == "" || !isDigits(part) || (len(part) > 1 && part[0] == '0') {
			return version{}, fmt.Errorf("%q is not a semantic version", raw)
		}
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return version{}, fmt.Errorf("%q is not a semantic version", raw)
		}
		numbers[i] = n
	}
	return version{major: numbers[0], minor: numbers[1], patch: numbers[2], pre: pre}, nil
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func (v version) compare(other version) int {
	for _, pair := range [][2]uint64{{v.major, other.major}, {v.minor, other.minor}, {v.patch, other.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(v.pre) == 0 && len(other.pre) == 0 {
		return 0
	}
	if len(v.pre) == 0 {
		return 1
	}
	if len(other.pre) == 0 {
		return -1
	}
	limit := len(v.pre)
	if len(other.pre) < limit {
		limit = len(other.pre)
	}
	for i := 0; i < limit; i++ {
		a, b := v.pre[i], other.pre[i]
		if a == b {
			continue
		}
		an, aNum := numericIdentifier(a)
		bn, bNum := numericIdentifier(b)
		switch {
		case aNum && bNum:
			if an < bn {
				return -1
			}
			return 1
		case aNum:
			return -1
		case bNum:
			return 1
		case a < b:
			return -1
		default:
			return 1
		}
	}
	if len(v.pre) < len(other.pre) {
		return -1
	}
	if len(v.pre) > len(other.pre) {
		return 1
	}
	return 0
}

func numericIdentifier(s string) (uint64, bool) {
	if !isDigits(s) {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

type constraint struct {
	op      string
	version version
}

func parseConstraint(raw string) ([]constraint, error) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return nil, errors.New("compatibility constraint is required")
	}
	out := make([]constraint, 0, len(fields))
	for _, field := range fields {
		op := "="
		value := field
		for _, candidate := range []string{">=", "<=", ">", "<", "="} {
			if strings.HasPrefix(field, candidate) {
				op = candidate
				value = strings.TrimPrefix(field, candidate)
				break
			}
		}
		v, err := parseVersion(value)
		if err != nil {
			return nil, fmt.Errorf("invalid term %q: %w", field, err)
		}
		out = append(out, constraint{op: op, version: v})
	}
	return out, nil
}

func (c constraint) matches(v version) bool {
	cmp := v.compare(c.version)
	switch c.op {
	case ">=":
		return cmp >= 0
	case "<=":
		return cmp <= 0
	case ">":
		return cmp > 0
	case "<":
		return cmp < 0
	default:
		return cmp == 0
	}
}
