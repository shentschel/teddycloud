package catalog

import (
	"crypto/sha256"
	"sort"
)

// CatalogBaseline is mechanical evidence of stored facts, without authorship,
// classification, accepted support or inferred order.
type CatalogBaseline struct {
	content  Content
	versions []ContentVersion
	raw      string
}

func NewCatalogBaseline(content Content, versions []ContentVersion) (CatalogBaseline, error) {
	if content.ID().IsZero() {
		return CatalogBaseline{}, ErrInvalidCatalog
	}
	if err := checkCatalogText(content.Facts().Title(), TextBytes, true); err != nil {
		return CatalogBaseline{}, err
	}
	if err := CheckLimit(RetainedVersions, 0, uint64(len(versions))); err != nil {
		return CatalogBaseline{}, err
	}
	for i, version := range versions {
		if version.ContentID() != content.ID() || version.ID().IsZero() || version.Fingerprint().IsZero() {
			return CatalogBaseline{}, ErrCatalogSubjectMismatch
		}
		for _, earlier := range versions[:i] {
			if earlier.ID() == version.ID() {
				return CatalogBaseline{}, ErrVersionIdentityConflict
			}
		}
	}
	versions = append([]ContentVersion(nil), versions...)
	sort.Slice(versions, func(i, j int) bool { return versions[i].ID().String() < versions[j].ID().String() })
	e := catalogEncoder{data: make([]byte, 0)}
	e.text("pi08-baseline/1")
	e.text(content.ID().String())
	e.text(content.Facts().Title())
	model, hasModel := content.Facts().ProductIdentifiers().Model()
	article, hasArticle := content.Facts().ProductIdentifiers().Article()
	if hasModel {
		e.number(1)
		e.text(model.String())
	} else {
		e.number(0)
	}
	if hasArticle {
		e.number(1)
		e.text(article.String())
	} else {
		e.number(0)
	}
	e.number(uint64(len(versions)))
	for _, version := range versions {
		e.text(version.ID().String())
		e.text(version.ContentID().String())
		e.number(version.Fingerprint().AudioID().Uint64())
		e.text(version.Fingerprint().Hash().String())
		e.text(version.OrderEvidence().Namespace())
		e.number(version.OrderEvidence().Position())
		if version.OrderEvidence().Known() {
			e.number(1)
		} else {
			e.number(0)
		}
	}
	if e.err != nil {
		return CatalogBaseline{}, e.err
	}
	if err := CheckLimit(RecordBytes, 0, uint64(len(e.data))); err != nil {
		return CatalogBaseline{}, err
	}
	return CatalogBaseline{content: content, versions: versions, raw: string(e.data)}, nil
}

func (b CatalogBaseline) Content() Content { return b.content }
func (b CatalogBaseline) Versions() []ContentVersion {
	return append([]ContentVersion(nil), b.versions...)
}
func (b CatalogBaseline) Bytes() []byte { return []byte(b.raw) }
func (b CatalogBaseline) SHA256() [32]byte {
	if b.raw == "" {
		return [32]byte{}
	}
	return sha256.Sum256([]byte(b.raw))
}

// Adoption uses a separately bound baseline source and checks current review
// authority on every invocation, including exact replay.
type CatalogAdoptionCommand struct {
	ID             CatalogCommandID
	Subject        CatalogSubject
	Source         SourceRegistration
	Expected       CatalogRevision
	BaselineSHA256 [32]byte
	Reason         string
}

func (c CatalogAdoptionCommand) Canonical(capability ReviewCapability) ([]byte, error) {
	if err := capability.Authorize(c.Source, TitleClaimKey); err != nil {
		return nil, err
	}
	if c.Source.Spec().Acquisition != BaselineSnapshot {
		return nil, ErrCatalogUnauthorized
	}
	if c.ID.IsZero() || c.Subject.ContentID().IsZero() || !c.Subject.VersionID().IsZero() || c.Expected.Uint64() == 0 || c.BaselineSHA256 == [32]byte{} {
		return nil, ErrInvalidCatalog
	}
	if err := checkCatalogText(c.Reason, TextBytes, false); err != nil {
		return nil, err
	}
	e := catalogEncoder{data: make([]byte, 0)}
	e.text("pi08-baseline-adoption/1")
	e.text(c.ID.String())
	e.text(c.Subject.ContentID().String())
	e.source(c.Source)
	e.text(capability.Reviewer().String())
	e.number(c.Expected.Uint64())
	e.text(string(c.BaselineSHA256[:]))
	e.text(c.Reason)
	return e.data, e.err
}
