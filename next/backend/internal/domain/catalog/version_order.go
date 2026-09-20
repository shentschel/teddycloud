package catalog

import "errors"

const maxOrderNamespaceLength = 128

var ErrInvalidVersionOrderEvidence = errors.New("invalid version order evidence")

// VersionOrderEvidence is an explicitly accepted position within one trusted
// ordering namespace. A zero value means that ordering is unknown.
type VersionOrderEvidence struct {
	namespace string
	position  uint64
	known     bool
}

// VersionOrdering is the typed outcome of comparing two immutable revisions.
type VersionOrdering uint8

const (
	VersionOrderingUnknown VersionOrdering = iota
	VersionOrderingBefore
	VersionOrderingSame
	VersionOrderingAfter
	VersionOrderingConflict
)

func NewVersionOrderEvidence(namespace string, position uint64) (VersionOrderEvidence, error) {
	if len(namespace) == 0 || len(namespace) > maxOrderNamespaceLength {
		return VersionOrderEvidence{}, ErrInvalidVersionOrderEvidence
	}
	for i := range len(namespace) {
		if namespace[i] < 0x21 || namespace[i] > 0x7e {
			return VersionOrderEvidence{}, ErrInvalidVersionOrderEvidence
		}
	}
	return VersionOrderEvidence{namespace: namespace, position: position, known: true}, nil
}

func UnknownVersionOrderEvidence() VersionOrderEvidence { return VersionOrderEvidence{} }

func (evidence VersionOrderEvidence) Known() bool       { return evidence.known }
func (evidence VersionOrderEvidence) Namespace() string { return evidence.namespace }
func (evidence VersionOrderEvidence) Position() uint64  { return evidence.position }

// CompareVersions uses only explicit order evidence. Numeric audio IDs, hashes,
// file times, and import position are intentionally unavailable to this rule.
func CompareVersions(left, right ContentVersion) VersionOrdering {
	if left.ID() == right.ID() {
		return VersionOrderingSame
	}
	leftOrder, rightOrder := left.OrderEvidence(), right.OrderEvidence()
	if !leftOrder.Known() || !rightOrder.Known() {
		return VersionOrderingUnknown
	}
	if leftOrder.Namespace() != rightOrder.Namespace() || leftOrder.Position() == rightOrder.Position() {
		return VersionOrderingConflict
	}
	if leftOrder.Position() < rightOrder.Position() {
		return VersionOrderingBefore
	}
	return VersionOrderingAfter
}
