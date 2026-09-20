package catalog

import (
	"errors"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

var (
	// ErrZeroContentID means a Content aggregate lacks allocated identity.
	ErrZeroContentID = errors.New("content identity is required")
	// ErrZeroContentVersionID means a revision lacks allocated identity.
	ErrZeroContentVersionID = errors.New("content version identity is required")
	// ErrVersionIdentityConflict means one version ID described different facts.
	ErrVersionIdentityConflict = errors.New("content version identity conflict")
)

// ContentFacts are mutable-by-replacement editorial observations. They never
// participate in Content identity equality.
type ContentFacts struct {
	title   string
	product ProductIdentifiers
}

// Content is an editorial work identified only by ContentID.
type Content struct {
	id    identity.ContentID
	facts ContentFacts
}

// ContentVersion is an immutable playable revision. Its unexported fields have
// no mutation methods; corrections create another value/version record.
type ContentVersion struct {
	id          identity.ContentVersionID
	contentID   identity.ContentID
	fingerprint AudioFingerprint
	order       VersionOrderEvidence
}

func NewContentFacts(title string, product ProductIdentifiers) ContentFacts {
	return ContentFacts{title: title, product: product}
}

func NewContent(id identity.ContentID, facts ContentFacts) (Content, error) {
	if id.IsZero() {
		return Content{}, ErrZeroContentID
	}
	return Content{id: id, facts: facts}, nil
}

func NewContentVersion(
	id identity.ContentVersionID,
	contentID identity.ContentID,
	fingerprint AudioFingerprint,
	order VersionOrderEvidence,
) (ContentVersion, error) {
	if id.IsZero() {
		return ContentVersion{}, ErrZeroContentVersionID
	}
	if contentID.IsZero() {
		return ContentVersion{}, ErrZeroContentID
	}
	if fingerprint.IsZero() {
		return ContentVersion{}, ErrInvalidFingerprint
	}
	return ContentVersion{
		id:          id,
		contentID:   contentID,
		fingerprint: fingerprint,
		order:       order,
	}, nil
}

func (content Content) ID() identity.ContentID { return content.id }
func (content Content) Facts() ContentFacts    { return content.facts }
func (content Content) SameIdentity(other Content) bool {
	return content.id == other.id
}

func (facts ContentFacts) Title() string                          { return facts.title }
func (facts ContentFacts) ProductIdentifiers() ProductIdentifiers { return facts.product }

func (version ContentVersion) ID() identity.ContentVersionID       { return version.id }
func (version ContentVersion) ContentID() identity.ContentID       { return version.contentID }
func (version ContentVersion) Fingerprint() AudioFingerprint       { return version.fingerprint }
func (version ContentVersion) OrderEvidence() VersionOrderEvidence { return version.order }
func (version ContentVersion) SameIdentity(other ContentVersion) bool {
	return version.id == other.id
}
