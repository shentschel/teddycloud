package tag

import (
	"errors"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
)

func TestValidateRetainedSemanticsCompleteAndRegistration(t *testing.T) {
	if err := validateRetainedSemantics(completeRetainedView(t)); err != nil {
		t.Fatalf("complete retained view rejected: %v", err)
	}

	registration := retainedTagView{
		id:       mustTagIDNumber(t, 20),
		uid:      UIDFromBytes([8]byte{1, 2, 3, 4, 5, 6, 7, 8}),
		revision: InitialRevision,
	}
	if err := validateRetainedSemantics(registration); err != nil {
		t.Fatalf("empty registration rejected: %v", err)
	}
}

func TestValidateRetainedSemanticsRejectsInvalidSupportReferences(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*retainedTagView)
	}{
		{
			name: "dangling",
			mutate: func(view *retainedTagView) {
				view.decisions[0].selected = []evidence.ID{mustObservationIDNumber(t, 99)}
			},
		},
		{
			name: "cross key",
			mutate: func(view *retainedTagView) {
				view.decisions[0].selected = []evidence.ID{view.observations[1].observation.ID()}
			},
		},
		{
			name: "not accepted",
			mutate: func(view *retainedTagView) {
				view.decisions[0].selected = []evidence.ID{view.observations[4].observation.ID()}
			},
		},
		{
			name: "introduced after decision",
			mutate: func(view *retainedTagView) {
				view.decisions[0].key = retainedCloudAuth
				view.decisions[0].expectedRevision = 2
				view.decisions[0].resultRevision = 3
				view.decisions[0].selected = []evidence.ID{view.observations[2].observation.ID()}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			view := completeRetainedView(t)
			test.mutate(&view)
			if err := validateRetainedSemantics(view); !errors.Is(err, ErrInvalidRetainedEncoding) {
				t.Fatalf("error = %v, want generic invalid retained encoding", err)
			}
		})
	}
}

func TestValidateRetainedSemanticsRejectsInvalidHistoryOrderAndDuplicates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*retainedTagView)
	}{
		{
			name: "duplicate observation id",
			mutate: func(view *retainedTagView) {
				view.observations = append(view.observations, view.observations[0])
			},
		},
		{
			name: "duplicate decision id",
			mutate: func(view *retainedTagView) {
				view.revision = 6
				duplicate := view.decisions[0]
				duplicate.expectedRevision = 5
				duplicate.resultRevision = 6
				view.decisions = append(view.decisions, duplicate)
			},
		},
		{
			name: "duplicate decision revision",
			mutate: func(view *retainedTagView) {
				duplicate := view.decisions[0]
				duplicate.id = mustDecisionIDNumber(t, 1)
				view.decisions = append(view.decisions, duplicate)
			},
		},
		{
			name: "missing revision history",
			mutate: func(view *retainedTagView) {
				view.revision = 6
			},
		},
		{
			name: "mixed keys in one revision",
			mutate: func(view *retainedTagView) {
				view.observations[1].introducedRevision = 2
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			view := completeRetainedView(t)
			test.mutate(&view)
			if err := validateRetainedSemantics(view); !errors.Is(err, ErrInvalidRetainedEncoding) {
				t.Fatalf("error = %v, want generic invalid retained encoding", err)
			}
		})
	}
}

func TestValidateRetainedSemanticsRejectsWrongActiveProjection(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*retainedTagView)
	}{
		{
			name: "wrong active set",
			mutate: func(view *retainedTagView) {
				view.facts[retainedCloudAuth].active = view.facts[retainedCloudAuth].active[:1]
			},
		},
		{
			name: "wrong state",
			mutate: func(view *retainedTagView) {
				view.facts[retainedCloudAuth].state = MetadataObservedTrue
			},
		},
		{
			name: "duplicate active support",
			mutate: func(view *retainedTagView) {
				id := view.facts[retainedProtocolValid].active[0]
				view.facts[retainedProtocolValid].active = []evidence.ID{id, id}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			view := completeRetainedView(t)
			test.mutate(&view)
			if err := validateRetainedSemantics(view); !errors.Is(err, ErrInvalidRetainedEncoding) {
				t.Fatalf("error = %v, want generic invalid retained encoding", err)
			}
		})
	}
}

func TestValidateRetainedSemanticsLaterContraryAcceptedObservationRestoresConflict(t *testing.T) {
	view := completeRetainedView(t)
	source, err := evidence.NewSource("later", "r2", "record")
	if err != nil {
		t.Fatal(err)
	}
	later := mustObservation(
		t,
		99,
		view.id,
		"protocol_valid",
		"false",
		source,
		evidence.ConfidenceUnknown,
		evidence.Accepted,
		time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC),
	)
	view.revision = 6
	view.observations = append(view.observations, retainedObservationView{observation: later, introducedRevision: 6})
	view.facts[retainedProtocolValid] = retainedFactView{
		state:  MetadataConflict,
		active: []evidence.ID{view.observations[0].observation.ID(), later.ID()},
	}
	if err := validateRetainedSemantics(view); err != nil {
		t.Fatalf("later contrary accepted evidence rejected: %v", err)
	}
}

func TestValidateRetainedSemanticsByteLimitWinsBeforeSemanticValidation(t *testing.T) {
	view := boundaryRetainedView(t, 485_586, 0)
	view.decisions[0].selected[1] = view.decisions[0].selected[0]
	if err := validateRetainedSemantics(view); !errors.Is(err, ErrEncodedTagLimit) {
		t.Fatalf("error = %v, want byte-limit admission error", err)
	}
}
