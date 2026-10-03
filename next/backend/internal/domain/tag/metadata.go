package tag

import (
	"errors"
	"math"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// FactKey is the closed registry vocabulary, independent of permissions.
type FactKey = retainedFactKey

const (
	ProtocolValid FactKey = retainedProtocolValid
	Claimed       FactKey = retainedClaimed
	CloudAuth     FactKey = retainedCloudAuth
	Owned         FactKey = retainedOwned
)

func ParseFactKey(text string) (FactKey, error) {
	key, ok := retainedKeyForClaim(text)
	if !ok {
		return 0, ErrInvalidMetadata
	}
	return key, nil
}
func (key retainedFactKey) String() string {
	switch key {
	case ProtocolValid:
		return "protocol_valid"
	case Claimed:
		return "claimed"
	case CloudAuth:
		return "cloud_auth"
	case Owned:
		return "owned"
	default:
		return ""
	}
}

var (
	ErrInvalidMetadata  = errors.New("invalid tag metadata")
	ErrRevisionConflict = errors.New("tag revision conflict")
	ErrEvidenceConflict = errors.New("tag evidence conflict")
)

type Resolution struct {
	ID      DecisionID
	Support []evidence.ID
}

type MetadataChange struct {
	ExpectedRevision Revision
	Key              FactKey
	Observations     []evidence.Observation
	Resolution       *Resolution
}

// StoredObservation and StoredDecision are bounded persistence inputs.
type StoredObservation struct {
	Observation        evidence.Observation
	IntroducedRevision Revision
}
type StoredDecision struct {
	ID               DecisionID
	Key              FactKey
	ExpectedRevision Revision
	ResultRevision   Revision
	Support          []evidence.ID
}

// VisitHistory exposes immutable scalars and indexed support, never owned slices.
func (t Tag) VisitHistory(observe func(evidence.Observation, Revision) error, decide func(DecisionID, FactKey, Revision, Revision, int, func(int) evidence.ID) error) error {
	for _, o := range t.retained.view.observations {
		if err := observe(o.observation, o.introducedRevision); err != nil {
			return err
		}
	}
	for _, d := range t.retained.view.decisions {
		if err := decide(d.id, d.key, d.expectedRevision, d.resultRevision, len(d.selected), func(i int) evidence.ID { return d.selected[i] }); err != nil {
			return err
		}
	}
	return nil
}

// HistorySize counts actual fields before aggregate materialization. Its
// lengths are derived by the same encoder used by the retained constructor.
type HistorySize struct {
	base             int64
	observations     int
	decisions        int
	links            int
	observationBytes int64
	decisionBytes    int64
	active           int
}

func NewHistorySize(id identity.TagID, uid UID, revision Revision) (HistorySize, error) {
	n, err := encodedLen(retainedTagView{id: id, uid: uid, revision: revision})
	return HistorySize{base: n}, err
}

// EncodedSize traverses this immutable aggregate without copying history.
func (t Tag) EncodedSize() (int64, error) { return encodedLen(t.retained.view) }

func (s *HistorySize) AddObservation(id identity.TagID, revision Revision, o evidence.Observation, introduced Revision) error {
	if s.observations >= MaxRetainedObservations {
		return ErrHistoryLimit
	}
	sink := countSink{limit: MaxEncodedTagBytes}
	row := [1]retainedObservationView{{observation: o, introducedRevision: introduced}}
	if err := writeObservations(&sink, retainedTagView{id: id, revision: revision, observations: row[:]}); err != nil {
		return err
	}
	n := sink.written - 2
	if s.observations > 0 {
		n++
	}
	next, ok := checkedAddInt64(s.observationBytes, n, MaxEncodedTagBytes)
	if !ok {
		return ErrEncodedTagLimit
	}
	s.observationBytes = next
	s.observations++
	return nil
}
func (s *HistorySize) AddDecision(id DecisionID, key FactKey, expected, result, revision Revision, supportCount int) error {
	if s.decisions >= MaxRetainedDecisions || supportCount < 1 || supportCount > MaxDecisionSupportLinks || supportCount > MaxRetainedDecisionSupportLinks-s.links {
		return ErrHistoryLimit
	}
	if !validDecisionID(id) || key >= retainedFactKeyCount || expected <= 0 || expected == Revision(math.MaxInt64) || result != expected+1 || result > revision {
		return ErrInvalidMetadata
	}
	// [id,key,expected,result,[support...]], including all quotes and commas.
	n := int64(2+32+1+decimalWidth(expected)+decimalWidth(result)+4+2) + int64(supportCount)*32 + int64(supportCount-1)
	if s.decisions > 0 {
		n++
	}
	next, ok := checkedAddInt64(s.decisionBytes, n, MaxEncodedTagBytes)
	if !ok {
		return ErrEncodedTagLimit
	}
	s.decisionBytes = next
	s.decisions++
	s.links += supportCount
	return nil
}
func decimalWidth(r Revision) int {
	n := 1
	for r >= 10 {
		n++
		r /= 10
	}
	return n
}
func (s *HistorySize) AddActive(count int) error {
	if count < 0 || count > MaxRetainedObservations-s.active {
		return ErrHistoryLimit
	}
	// Each nonempty ID array replaces an empty array.
	n := int64(count) * 32
	if count > 0 {
		n += int64(count - 1)
	}
	next, ok := checkedAddInt64(s.base, n, MaxEncodedTagBytes)
	if !ok {
		return ErrEncodedTagLimit
	}
	s.base = next
	s.active += count
	return nil
}
func (s HistorySize) Finish() (int64, error) {
	if s.active > s.observations {
		return 0, ErrInvalidMetadata
	}
	n, ok := checkedAddInt64(s.base, s.observationBytes, MaxEncodedTagBytes)
	if !ok {
		return 0, ErrEncodedTagLimit
	}
	n, ok = checkedAddInt64(n, s.decisionBytes, MaxEncodedTagBytes)
	if !ok {
		return 0, ErrEncodedTagLimit
	}
	return n, nil
}

// RestoreMetadata admits fields/bytes before taking any aggregate copies.
func RestoreMetadata(id identity.TagID, uid UID, revision Revision, observations []StoredObservation, decisions []StoredDecision) (Tag, error) {
	if len(observations) > MaxRetainedObservations || len(decisions) > MaxRetainedDecisions {
		return Tag{}, ErrHistoryLimit
	}
	size, err := NewHistorySize(id, uid, revision)
	if err != nil {
		return Tag{}, err
	}
	for _, o := range observations {
		if err := size.AddObservation(id, revision, o.Observation, o.IntroducedRevision); err != nil {
			return Tag{}, err
		}
	}
	for _, d := range decisions {
		if err := size.AddDecision(d.ID, d.Key, d.ExpectedRevision, d.ResultRevision, revision, len(d.Support)); err != nil {
			return Tag{}, err
		}
	}
	var counts [4]int
	for _, o := range observations {
		key, ok := retainedKeyForClaim(o.Observation.Claim().Key)
		if !ok {
			return Tag{}, ErrInvalidMetadata
		}
		if storageActive(o, key, decisions) {
			counts[key]++
		}
	}
	for _, n := range counts {
		if err := size.AddActive(n); err != nil {
			return Tag{}, err
		}
	}
	if _, err := size.Finish(); err != nil {
		return Tag{}, err
	}
	view := retainedTagView{id: id, uid: uid, revision: revision}
	view.observations = make([]retainedObservationView, len(observations))
	view.decisions = make([]retainedDecisionView, len(decisions))
	for i, o := range observations {
		view.observations[i] = retainedObservationView{o.Observation, o.IntroducedRevision}
		key, _ := retainedKeyForClaim(o.Observation.Claim().Key)
		if storageActive(o, key, decisions) {
			view.facts[key].active = append(view.facts[key].active, o.Observation.ID())
			addProjectedValue(&view.facts[key].state, o.Observation.Claim().Value)
		}
	}
	for i, d := range decisions {
		view.decisions[i] = retainedDecisionView{d.ID, d.Key, d.ExpectedRevision, d.ResultRevision, d.Support}
	}
	return newTagFromRetainedView(view)
}
func storageActive(o StoredObservation, key FactKey, decisions []StoredDecision) bool {
	if o.Observation.Review() != evidence.Accepted {
		return false
	}
	var latest *StoredDecision
	for i := range decisions {
		d := &decisions[i]
		if d.Key == key && (latest == nil || d.ResultRevision > latest.ResultRevision) {
			latest = d
		}
	}
	if latest == nil || o.IntroducedRevision > latest.ResultRevision {
		return true
	}
	for _, id := range latest.Support {
		if id == o.Observation.ID() {
			return true
		}
	}
	return false
}
func addProjectedValue(state *MetadataState, value string) {
	want := MetadataObservedFalse
	if value == "true" {
		want = MetadataObservedTrue
	}
	if *state == MetadataUnknown {
		*state = want
	} else if *state != want {
		*state = MetadataConflict
	}
}

// ApplyMetadata checks immutable command identities before stale revisions,
// and preflights base plus delta before allocating a proposed aggregate.
func (t Tag) ApplyMetadata(c MetadataChange) (Tag, bool, error) {
	if len(c.Observations) > MaxNewObservationsPerCommand || (c.Resolution != nil && len(c.Resolution.Support) > MaxDecisionSupportLinks) {
		return Tag{}, false, ErrHistoryLimit
	}
	if t.ID().IsZero() || c.ExpectedRevision <= 0 || c.ExpectedRevision == Revision(math.MaxInt64) || c.Key >= retainedFactKeyCount || (len(c.Observations) == 0 && c.Resolution == nil) {
		return Tag{}, false, ErrInvalidMetadata
	}
	if c.Resolution != nil && (!validDecisionID(c.Resolution.ID) || len(c.Resolution.Support) == 0) {
		return Tag{}, false, ErrInvalidMetadata
	}
	for i, o := range c.Observations {
		row := [1]retainedObservationView{{o, c.ExpectedRevision + 1}}
		sink := countSink{limit: MaxEncodedTagBytes}
		if err := writeObservations(&sink, retainedTagView{id: t.ID(), revision: c.ExpectedRevision + 1, observations: row[:]}); err != nil {
			return Tag{}, false, err
		}
		if o.Claim().Key != c.Key.String() {
			return Tag{}, false, ErrInvalidMetadata
		}
		for j := 0; j < i; j++ {
			if c.Observations[j].ID() == o.ID() {
				return Tag{}, false, ErrEvidenceConflict
			}
		}
	}
	existing := 0
	for _, o := range c.Observations {
		for _, old := range t.retained.view.observations {
			if old.observation.ID() == o.ID() {
				if !equalRetainedObservation(old, retainedObservationView{o, c.ExpectedRevision + 1}) {
					return Tag{}, false, ErrEvidenceConflict
				}
				existing++
				break
			}
		}
	}
	decisionExists := false
	if c.Resolution != nil {
		for i, id := range c.Resolution.Support {
			if !validEvidenceID(id) {
				return Tag{}, false, ErrInvalidMetadata
			}
			for j := 0; j < i; j++ {
				if c.Resolution.Support[j] == id {
					return Tag{}, false, ErrEvidenceConflict
				}
			}
		}
		for _, d := range t.retained.view.decisions {
			if d.id == c.Resolution.ID {
				if d.key != c.Key || d.expectedRevision != c.ExpectedRevision || !sameSupport(d.selected, c.Resolution.Support) {
					return Tag{}, false, ErrEvidenceConflict
				}
				decisionExists = true
			}
		}
	}
	if existing > 0 || decisionExists {
		if existing != len(c.Observations) || (c.Resolution != nil && !decisionExists) {
			return Tag{}, false, ErrEvidenceConflict
		}
		// A subset is not a replay of the original atomic observation command.
		count := 0
		for _, o := range t.retained.view.observations {
			if o.introducedRevision == c.ExpectedRevision+1 {
				count++
			}
		}
		hasDecision := false
		for _, d := range t.retained.view.decisions {
			if d.resultRevision == c.ExpectedRevision+1 {
				hasDecision = true
			}
		}
		if count != len(c.Observations) || hasDecision != (c.Resolution != nil) {
			return Tag{}, false, ErrEvidenceConflict
		}
		return t, false, nil
	}
	if c.ExpectedRevision != t.Revision() {
		return Tag{}, false, ErrRevisionConflict
	}
	if c.Resolution != nil {
		if err := t.validateSupportMembership(c); err != nil {
			return Tag{}, false, err
		}
	}
	size, err := NewHistorySize(t.ID(), t.UID(), t.Revision()+1)
	if err != nil {
		return Tag{}, false, err
	}
	if err := t.VisitHistory(func(o evidence.Observation, r Revision) error {
		return size.AddObservation(t.ID(), t.Revision()+1, o, r)
	}, func(id DecisionID, k FactKey, e, r Revision, n int, _ func(int) evidence.ID) error {
		return size.AddDecision(id, k, e, r, t.Revision()+1, n)
	}); err != nil {
		return Tag{}, false, err
	}
	for _, o := range c.Observations {
		if err := size.AddObservation(t.ID(), t.Revision()+1, o, t.Revision()+1); err != nil {
			return Tag{}, false, err
		}
	}
	if c.Resolution != nil {
		if err := size.AddDecision(c.Resolution.ID, c.Key, c.ExpectedRevision, c.ExpectedRevision+1, t.Revision()+1, len(c.Resolution.Support)); err != nil {
			return Tag{}, false, err
		}
	}
	for key := FactKey(0); key < retainedFactKeyCount; key++ {
		count := len(t.retained.view.facts[key].active)
		if key == c.Key {
			if c.Resolution != nil {
				count = len(c.Resolution.Support)
			} else {
				for _, o := range c.Observations {
					if o.Review() == evidence.Accepted {
						count++
					}
				}
			}
		}
		if err := size.AddActive(count); err != nil {
			return Tag{}, false, err
		}
	}
	if _, err := size.Finish(); err != nil {
		return Tag{}, false, err
	}
	var resolutionSupport []evidence.Observation
	if c.Resolution != nil {
		resolutionSupport, err = t.resolveSupport(c)
		if err != nil {
			return Tag{}, false, err
		}
	}
	view := t.retained.view
	view.revision++
	view.observations = append(append([]retainedObservationView(nil), view.observations...), make([]retainedObservationView, len(c.Observations))...)
	for i, o := range c.Observations {
		view.observations[len(view.observations)-len(c.Observations)+i] = retainedObservationView{o, view.revision}
	}
	if c.Resolution != nil {
		view.decisions = append(append([]retainedDecisionView(nil), view.decisions...), retainedDecisionView{c.Resolution.ID, c.Key, c.ExpectedRevision, view.revision, c.Resolution.Support})
		view.facts[c.Key] = retainedFactView{active: c.Resolution.Support}
		for _, o := range resolutionSupport {
			addProjectedValue(&view.facts[c.Key].state, o.Claim().Value)
		}
	} else {
		fact := view.facts[c.Key]
		fact.active = append([]evidence.ID(nil), fact.active...)
		for _, o := range c.Observations {
			if o.Review() == evidence.Accepted {
				fact.active = append(fact.active, o.ID())
				addProjectedValue(&fact.state, o.Claim().Value)
			}
		}
		view.facts[c.Key] = fact
	}
	result, err := newTagFromRetainedView(view)
	return result, err == nil, err
}

// validateSupportMembership is allocation-free and runs before size admission,
// so foreign support reports a semantic conflict without constructing history.
func (t Tag) validateSupportMembership(c MetadataChange) error {
	for _, id := range c.Resolution.Support {
		found := false
		for _, old := range t.retained.view.observations {
			if old.observation.ID() == id && old.observation.Claim().Key == c.Key.String() && old.observation.Review() == evidence.Accepted {
				found = true
				break
			}
		}
		if !found {
			for _, added := range c.Observations {
				if added.ID() == id && added.Claim().Key == c.Key.String() && added.Review() == evidence.Accepted {
					found = true
					break
				}
			}
		}
		if !found {
			return ErrEvidenceConflict
		}
	}
	return nil
}

// resolveSupport materializes only after complete count/byte admission.
func (t Tag) resolveSupport(c MetadataChange) ([]evidence.Observation, error) {
	support := make([]evidence.Observation, 0, len(c.Resolution.Support))
	for _, id := range c.Resolution.Support {
		for _, old := range t.retained.view.observations {
			if old.observation.ID() == id {
				support = append(support, old.observation)
				break
			}
		}
		if len(support) == 0 || support[len(support)-1].ID() != id {
			for _, added := range c.Observations {
				if added.ID() == id {
					support = append(support, added)
					break
				}
			}
		}
	}
	if _, err := evidence.Accept(support[0].Claim(), support); err != nil {
		return nil, ErrEvidenceConflict
	}
	return support, nil
}

func sameSupport(a, b []evidence.ID) bool {
	if len(a) != len(b) {
		return false
	}
	for _, id := range a {
		found := false
		for _, other := range b {
			if id == other {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// IsMetadataSuccessor fences direct repository callers as well as the service:
// no history or identity may be replaced by a caller-constructed aggregate.
func (t Tag) IsMetadataSuccessor(next Tag) bool {
	if t.ID() != next.ID() || t.UID() != next.UID() || t.Revision() == Revision(math.MaxInt64) || next.Revision() != t.Revision()+1 {
		return false
	}
	for _, o := range t.retained.view.observations {
		found := false
		for _, n := range next.retained.view.observations {
			if equalRetainedObservation(o, n) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, d := range t.retained.view.decisions {
		found := false
		for _, n := range next.retained.view.decisions {
			if equalRetainedDecision(d, n) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
