package catalog

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	domaincatalog "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
)

func baselineQueryFixture(t *testing.T) (fixture, Query, QueryResult) {
	t.Helper()
	f := newFixture(t)
	query := Query{Kind: GetVersion, Content: f.subject.ContentID(), Version: f.versions[0].ID(), Page: PageRequest{Limit: 128}}
	revision, err := domaincatalog.NewCatalogRevision(1)
	require(t, err)
	record := CatalogRecord{
		Content:  f.subject.ContentID(),
		Baseline: domaincatalog.NewContentFacts("Stored title", domaincatalog.NewProductIdentifiers(nil, nil)),
		Version:  f.versions[0],
		Media:    CatalogOnly,
	}
	return f, query, QueryResult{Kind: Found, Entries: []CatalogRecord{record}, Revision: revision, Complete: true, Inspected: 1, EncodedBytes: 1000}
}

func projectionForTest(t *testing.T, claim domaincatalog.CatalogClaim, suffix string) domaincatalog.CatalogField {
	t.Helper()
	id, err := evidence.ParseID("obs_" + suffix)
	require(t, err)
	return domaincatalog.CatalogField{Status: domaincatalog.CatalogFactAccepted, Alternatives: []domaincatalog.CatalogAlternative{{Claim: claim, Support: []evidence.ID{id}}}}
}

func TestCatalogQueryBaselineVisibleWithoutAcceptance(t *testing.T) {
	for _, state := range []CatalogReviewStatus{CatalogUnmanaged, CatalogManagedUnreviewed} {
		for _, kind := range []QueryKind{GetContent, GetVersion, FindVersionsByPair, FindVersionsByAudioID, ListVersions} {
			f, query, page := baselineQueryFixture(t)
			query.Kind = kind
			query.Version = page.Entries[0].Version.ID()
			switch kind {
			case GetContent, ListVersions:
				query.Version = f.subject.VersionID()
			case FindVersionsByPair:
				query.Version = f.subject.VersionID()
				query.Fingerprint = f.versions[0].Fingerprint()
			case FindVersionsByAudioID:
				query.Version = f.subject.VersionID()
				query.AudioID = f.versions[0].Fingerprint().AudioID()
			}
			if kind == GetContent {
				page.Entries[0].Version = domaincatalog.ContentVersion{}
				page.Entries[0].Media = MediaUnknown
			}
			page.Entries[0].Review = state
			page.Entries[0].ProtectedCustom = true
			f.tx.repository.page = page
			result, err := f.service.Query(context.Background(), query)
			require(t, err)
			if !reflect.DeepEqual(result, page) || result.Entries[0].Baseline.Title() != "Stored title" {
				t.Fatal("stored facts, state, protection or version context lost")
			}
			for _, field := range []domaincatalog.CatalogField{result.Entries[0].Title, result.Entries[0].Model, result.Entries[0].Article} {
				if _, accepted := field.Value(); accepted || field.Status != domaincatalog.CatalogFactUnknown {
					t.Fatal("baseline became an accepted projection")
				}
			}
		}
	}
}

func TestCatalogQueryBaselineOptionalValues(t *testing.T) {
	model, err := domaincatalog.ParseModelNumber("MODEL-1")
	require(t, err)
	article, err := domaincatalog.ParseArticleNumber("ARTICLE-2")
	require(t, err)
	for _, withModel := range []bool{false, true} {
		for _, withArticle := range []bool{false, true} {
			f, query, page := baselineQueryFixture(t)
			var m *domaincatalog.ModelNumber
			var a *domaincatalog.ArticleNumber
			if withModel {
				m = &model
			}
			if withArticle {
				a = &article
			}
			page.Entries[0].Baseline = domaincatalog.NewContentFacts("", domaincatalog.NewProductIdentifiers(m, a))
			f.tx.repository.page = page
			result, err := f.service.Query(context.Background(), query)
			require(t, err)
			product := result.Entries[0].Baseline.ProductIdentifiers()
			gotModel, hasModel := product.Model()
			gotArticle, hasArticle := product.Article()
			if hasModel != withModel || hasArticle != withArticle || hasModel && gotModel != model || hasArticle && gotArticle != article {
				t.Fatal("optional stored values collapsed or crossed")
			}
			if _, ok := result.Entries[0].Model.Value(); ok {
				t.Fatal("stored model accepted without review")
			}
			if _, ok := result.Entries[0].Article.Value(); ok {
				t.Fatal("stored article accepted without review")
			}
		}
	}
}

func TestCatalogQueryReviewedAndConflictedProjections(t *testing.T) {
	f, query, page := baselineQueryFixture(t)
	a := f.observation(t, payload, "A")
	b := f.observation(t, "1123456789abcdefghjkmnpqrs", "B")
	stage := f.command(t)
	stage.Observations = []domaincatalog.CatalogObservation{b, a}
	_, err := f.service.RecordObservations(context.Background(), stage)
	require(t, err)
	accept := f.command(t)
	accept.Key, accept.Support = domaincatalog.TitleClaimKey, []evidence.ID{a.ID()}
	accepted, err := f.service.AcceptSupport(context.Background(), accept)
	require(t, err)
	page.Entries[0].Review = CatalogReviewed
	page.Entries[0].Title = accepted.Decision.Field
	f.tx.repository.page = page
	result, err := f.service.Query(context.Background(), query)
	require(t, err)
	value, ok := result.Entries[0].Title.Value()
	if !ok || value.Text() != "A" || result.Entries[0].Baseline.Title() != "Stored title" {
		t.Fatal("reviewed correction overwrote baseline or lost supported value")
	}
	accept = f.command(t)
	accept.Key, accept.Support = domaincatalog.TitleClaimKey, []evidence.ID{b.ID()}
	conflicted, err := f.service.AcceptSupport(context.Background(), accept)
	require(t, err)
	page.Entries[0].Title = conflicted.Decision.Field
	f.tx.repository.page = page
	if _, err := f.service.Query(context.Background(), query); !errors.Is(err, ErrRepositoryUnavailable) {
		t.Fatal("conflicted field advertised as Found")
	}
	for _, kind := range []ResultKind{Conflicted, Incomplete} {
		page.Kind, page.Complete = kind, kind != Incomplete
		f.tx.repository.page = page
		result, err = f.service.Query(context.Background(), query)
		require(t, err)
		if _, ok := result.Entries[0].Title.Value(); ok || !reflect.DeepEqual(result, page) {
			t.Fatal("conflict selected a winner or dropped support")
		}
	}
}

func TestCatalogQueryInvalidRecordCombinations(t *testing.T) {
	claim, err := domaincatalog.TitleClaim("Reviewed")
	require(t, err)
	other, err := domaincatalog.TitleClaim("Different")
	require(t, err)
	model, err := domaincatalog.ParseModelNumber("MODEL-1")
	require(t, err)
	modelClaim, err := domaincatalog.ModelClaim(model)
	require(t, err)
	cases := []struct {
		name   string
		change func(*CatalogRecord)
	}{
		{"unknown-state", func(r *CatalogRecord) { r.Review = CatalogReviewed + 1 }},
		{"unmanaged-accepted", func(r *CatalogRecord) { r.Review = CatalogUnmanaged }},
		{"unreviewed-accepted", func(r *CatalogRecord) {
			r.Review = CatalogManagedUnreviewed
			r.Title = projectionForTest(t, claim, payload)
		}},
		{"reviewed-empty", func(r *CatalogRecord) { r.Title = domaincatalog.CatalogField{} }},
		{"unmanaged-conflict", func(r *CatalogRecord) {
			r.Review = CatalogUnmanaged
			r.Title.Status = domaincatalog.CatalogFactConflict
			r.Title.Alternatives = append(r.Title.Alternatives, projectionForTest(t, other, "1123456789abcdefghjkmnpqrs").Alternatives[0])
		}},
		{"unreviewed-conflict", func(r *CatalogRecord) {
			r.Review = CatalogManagedUnreviewed
			r.Title.Status = domaincatalog.CatalogFactConflict
			r.Title.Alternatives = append(r.Title.Alternatives, projectionForTest(t, other, "1123456789abcdefghjkmnpqrs").Alternatives[0])
		}},
		{"unknown-with-alternatives", func(r *CatalogRecord) { r.Title.Status = domaincatalog.CatalogFactUnknown }},
		{"unknown-field-status", func(r *CatalogRecord) { r.Title.Status = domaincatalog.CatalogFactConflict + 1 }},
		{"accepted-empty", func(r *CatalogRecord) { r.Title.Alternatives = nil }},
		{"accepted-multiple", func(r *CatalogRecord) {
			r.Title.Alternatives = append(r.Title.Alternatives, projectionForTest(t, other, "1123456789abcdefghjkmnpqrs").Alternatives[0])
		}},
		{"conflict-single", func(r *CatalogRecord) { r.Title.Status = domaincatalog.CatalogFactConflict }},
		{"wrong-claim-key", func(r *CatalogRecord) { r.Title.Alternatives[0].Claim = modelClaim }},
		{"zero-claim", func(r *CatalogRecord) { r.Title.Alternatives[0].Claim = domaincatalog.CatalogClaim{} }},
		{"missing-support", func(r *CatalogRecord) { r.Title.Alternatives[0].Support = nil }},
		{"zero-support", func(r *CatalogRecord) { r.Title.Alternatives[0].Support[0] = evidence.ID{} }},
		{"duplicate-support", func(r *CatalogRecord) {
			r.Title.Alternatives[0].Support = append(r.Title.Alternatives[0].Support, r.Title.Alternatives[0].Support[0])
		}},
		{"shared-support", func(r *CatalogRecord) { r.Model = projectionForTest(t, modelClaim, payload) }},
		{"duplicate-alternatives", func(r *CatalogRecord) {
			r.Title.Status = domaincatalog.CatalogFactConflict
			r.Title.Alternatives = append(r.Title.Alternatives, r.Title.Alternatives[0])
		}},
		{"unsorted-alternatives", func(r *CatalogRecord) {
			r.Title.Status = domaincatalog.CatalogFactConflict
			r.Title.Alternatives = append(projectionForTest(t, other, "1123456789abcdefghjkmnpqrs").Alternatives, r.Title.Alternatives...)
		}},
		{"unsorted-support", func(r *CatalogRecord) {
			r.Title.Alternatives[0].Support = append(projectionForTest(t, other, "1123456789abcdefghjkmnpqrs").Alternatives[0].Support, r.Title.Alternatives[0].Support...)
		}},
		{"invalid-stored-text", func(r *CatalogRecord) { r.Baseline = domaincatalog.NewContentFacts("bad\x00title", domaincatalog.ProductIdentifiers{}) }},
		{"invalid-stored-utf8", func(r *CatalogRecord) { r.Baseline = domaincatalog.NewContentFacts(string([]byte{0xff}), domaincatalog.ProductIdentifiers{}) }},
		{"present-zero-model", func(r *CatalogRecord) {
			r.Baseline = domaincatalog.NewContentFacts("Stored", domaincatalog.NewProductIdentifiers(&domaincatalog.ModelNumber{}, nil))
		}},
		{"present-zero-article", func(r *CatalogRecord) {
			r.Baseline = domaincatalog.NewContentFacts("Stored", domaincatalog.NewProductIdentifiers(nil, &domaincatalog.ArticleNumber{}))
		}},
		{"missing-version", func(r *CatalogRecord) { r.Version = domaincatalog.ContentVersion{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, query, page := baselineQueryFixture(t)
			page.Entries[0].Review = CatalogReviewed
			page.Entries[0].Title = projectionForTest(t, claim, payload)
			tc.change(&page.Entries[0])
			f.tx.repository.page = page
			result, err := f.service.Query(context.Background(), query)
			if !errors.Is(err, ErrRepositoryUnavailable) || !reflect.DeepEqual(result, QueryResult{}) {
				t.Fatalf("invalid record escaped: result=%v err=%v", result.Kind, err)
			}
		})
	}
}

func TestCatalogQueryBaselineBoundsAndOrdering(t *testing.T) {
	f, query, page := baselineQueryFixture(t)
	for _, size := range []uint64{domaincatalog.TextBytes.Maximum(), domaincatalog.TextBytes.Maximum() + 1} {
		page.Entries[0].Baseline = domaincatalog.NewContentFacts(strings.Repeat("a", int(size)), domaincatalog.ProductIdentifiers{})
		f.tx.repository.page = page
		_, err := f.service.Query(context.Background(), query)
		if size == domaincatalog.TextBytes.Maximum() {
			require(t, err)
		} else if !errors.Is(err, ErrLimitExceeded) {
			t.Fatal("oversized baseline accepted")
		}
	}
	page.Entries[0].Baseline = domaincatalog.ContentFacts{}
	query.Kind, query.Version = ListVersions, f.subject.VersionID()
	second := page.Entries[0]
	second.Version = f.versions[1]
	page.Entries = append(page.Entries, second)
	page.Inspected = 2
	f.tx.repository.page = page
	_, err := f.service.Query(context.Background(), query)
	require(t, err)
	page.Entries[0], page.Entries[1] = page.Entries[1], page.Entries[0]
	f.tx.repository.page = page
	if _, err := f.service.Query(context.Background(), query); !errors.Is(err, ErrRepositoryUnavailable) {
		t.Fatal("unsorted baseline page accepted")
	}
}

func TestCatalogQueryReviewedOptionalProjections(t *testing.T) {
	f, query, page := baselineQueryFixture(t)
	model, err := domaincatalog.ParseModelNumber("MODEL-1")
	require(t, err)
	article, err := domaincatalog.ParseArticleNumber("ARTICLE-2")
	require(t, err)
	modelClaim, err := domaincatalog.ModelClaim(model)
	require(t, err)
	articleClaim, err := domaincatalog.ArticleClaim(article)
	require(t, err)
	page.Entries[0].Review = CatalogReviewed
	page.Entries[0].Model = projectionForTest(t, modelClaim, payload)
	page.Entries[0].Article = projectionForTest(t, articleClaim, "1123456789abcdefghjkmnpqrs")
	f.tx.repository.page = page
	result, err := f.service.Query(context.Background(), query)
	require(t, err)
	if _, ok := result.Entries[0].Title.Value(); ok {
		t.Fatal("partial review fabricated title acceptance")
	}
	if _, ok := result.Entries[0].Model.Value(); !ok {
		t.Fatal("reviewed model lost")
	}
	if _, ok := result.Entries[0].Article.Value(); !ok {
		t.Fatal("reviewed article lost")
	}
	if _, present := result.Entries[0].Baseline.ProductIdentifiers().Model(); present {
		t.Fatal("projection changed stored baseline")
	}
}

func TestCatalogQueryProjectionBoundsAndContentShape(t *testing.T) {
	f, query, page := baselineQueryFixture(t)
	claim, err := domaincatalog.TitleClaim("Reviewed")
	require(t, err)
	page.Entries[0].Review = CatalogReviewed
	page.Entries[0].Title = projectionForTest(t, claim, payload)
	page.Entries[0].Title.Alternatives[0].Support = make([]evidence.ID, domaincatalog.RetainedSupportLinks.Maximum()+1)
	f.tx.repository.page = page
	if _, err := f.service.Query(context.Background(), query); !errors.Is(err, ErrLimitExceeded) {
		t.Fatal("support bound ignored")
	}
	page.Entries[0].Title.Alternatives = make([]domaincatalog.CatalogAlternative, domaincatalog.RetainedObservations.Maximum()+1)
	f.tx.repository.page = page
	if _, err := f.service.Query(context.Background(), query); !errors.Is(err, ErrLimitExceeded) {
		t.Fatal("alternative bound ignored")
	}
	page.Entries[0].Title = projectionForTest(t, claim, payload)
	query.Kind, query.Version = GetContent, f.subject.VersionID()
	f.tx.repository.page = page
	if _, err := f.service.Query(context.Background(), query); !errors.Is(err, ErrRepositoryUnavailable) {
		t.Fatal("Content query carried a version")
	}
	page.Entries[0].Version = domaincatalog.ContentVersion{}
	f.tx.repository.page = page
	if _, err := f.service.Query(context.Background(), query); !errors.Is(err, ErrRepositoryUnavailable) {
		t.Fatal("Content query claimed version media")
	}
	page.Entries[0].Media = MediaUnknown
	f.tx.repository.page = page
	_, err = f.service.Query(context.Background(), query)
	require(t, err)
}
