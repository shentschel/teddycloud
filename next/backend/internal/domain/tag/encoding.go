package tag

import (
	"io"
	"time"
	"unicode/utf8"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

type tregSink struct {
	writer  io.Writer
	written int64
	limit   int64
}

type countSink = tregSink
type writerSink = tregSink

func (sink *tregSink) raw(value string) error {
	next, ok := checkedAddInt64(sink.written, int64(len(value)), sink.limit)
	if !ok {
		return ErrEncodedTagLimit
	}
	if sink.writer == nil {
		sink.written = next
		return nil
	}
	written, err := sink.writer.Write([]byte(value))
	sink.written += int64(written)
	if err != nil {
		return err
	}
	if written != len(value) {
		return io.ErrShortWrite
	}
	return nil
}

func (sink *tregSink) byte(value byte) error {
	next, ok := checkedAddInt64(sink.written, 1, sink.limit)
	if !ok {
		return ErrEncodedTagLimit
	}
	if sink.writer == nil {
		sink.written = next
		return nil
	}
	buffer := [1]byte{value}
	written, err := sink.writer.Write(buffer[:])
	sink.written += int64(written)
	if err != nil {
		return err
	}
	if written != 1 {
		return io.ErrShortWrite
	}
	return nil
}

// encodedLen derives its result from borrowed fields before aggregate copies.
// Field encodability is not validation of B1 transitions.
func encodedLen(view retainedTagView) (int64, error) {
	if err := checkRetainedViewCounts(view); err != nil {
		return 0, err
	}
	sink := countSink{limit: MaxEncodedTagBytes}
	if err := traverseTREG1(view, &sink); err != nil {
		return 0, err
	}
	return sink.written, nil
}

// writeTREG1 preflights all fields and bytes before cloning, sorting or writing.
// A writer failure can report partial bytes, but never a successful truncation.
func writeTREG1(view retainedTagView, writer io.Writer) (int64, error) {
	if writer == nil {
		return 0, ErrInvalidRetainedEncoding
	}
	want, err := encodedLen(view)
	if err != nil {
		return 0, err
	}
	canonical, err := canonicalEncodingView(view)
	if err != nil {
		return 0, err
	}
	sink := writerSink{writer: writer, limit: MaxEncodedTagBytes}
	if err := traverseTREG1(canonical, &sink); err != nil {
		return sink.written, err
	}
	if sink.written != want {
		return sink.written, ErrInvalidRetainedEncoding
	}
	return sink.written, nil
}

func traverseTREG1(view retainedTagView, sink *tregSink) error {
	if !validTagID(view.id) || view.revision <= 0 {
		return ErrInvalidRetainedEncoding
	}
	if err := sink.byte('['); err != nil {
		return err
	}
	if err := writeQuotedRaw(sink, "TREG"); err != nil {
		return err
	}
	if err := separator(sink); err != nil {
		return err
	}
	if err := sink.byte('1'); err != nil {
		return err
	}
	if err := separator(sink); err != nil {
		return err
	}
	if err := writeQuotedRaw(sink, view.id.String()); err != nil {
		return err
	}
	if err := separator(sink); err != nil {
		return err
	}
	if err := writeUID(sink, view.uid); err != nil {
		return err
	}
	if err := separator(sink); err != nil {
		return err
	}
	if err := writeRevision(sink, view.revision); err != nil {
		return err
	}
	if err := separator(sink); err != nil {
		return err
	}
	if err := writeFacts(sink, view.facts); err != nil {
		return err
	}
	if err := separator(sink); err != nil {
		return err
	}
	if err := writeObservations(sink, view); err != nil {
		return err
	}
	if err := separator(sink); err != nil {
		return err
	}
	if err := writeDecisions(sink, view); err != nil {
		return err
	}
	return sink.byte(']')
}

func writeFacts(sink *tregSink, facts [retainedFactKeyCount]retainedFactView) error {
	if err := sink.byte('['); err != nil {
		return err
	}
	for key := retainedFactKey(0); key < retainedFactKeyCount; key++ {
		if key > 0 {
			if err := separator(sink); err != nil {
				return err
			}
		}
		fact := facts[key]
		state, stateOK := treg1MetadataState(fact.state)
		if !stateOK {
			return ErrInvalidRetainedEncoding
		}
		if err := sink.byte('['); err != nil {
			return err
		}
		if err := sink.byte('0' + state); err != nil {
			return err
		}
		if err := separator(sink); err != nil {
			return err
		}
		if err := writeEvidenceIDs(sink, fact.active); err != nil {
			return err
		}
		if err := sink.byte(']'); err != nil {
			return err
		}
	}
	return sink.byte(']')
}

func writeObservations(sink *tregSink, view retainedTagView) error {
	if err := sink.byte('['); err != nil {
		return err
	}
	for index, retained := range view.observations {
		if index > 0 {
			if err := separator(sink); err != nil {
				return err
			}
		}
		observation := retained.observation
		claim := observation.Claim()
		key, keyOK := retainedKeyForClaim(claim.Key)
		value, valueOK := retainedBoolForClaim(claim.Value)
		confidence, confidenceOK := treg1Confidence(observation.Confidence())
		review, reviewOK := treg1Review(observation.Review())
		if !validEvidenceID(observation.ID()) || retained.introducedRevision < 2 ||
			retained.introducedRevision > view.revision || claim.Subject != view.id.String() ||
			!keyOK || !valueOK || !validSource(observation.Source()) ||
			!validObservedAt(observation.ObservedAt()) || !confidenceOK || !reviewOK {
			return ErrInvalidRetainedEncoding
		}
		if err := sink.byte('['); err != nil {
			return err
		}
		if err := writeQuotedRaw(sink, observation.ID().String()); err != nil {
			return err
		}
		if err := separator(sink); err != nil {
			return err
		}
		if err := writeRevision(sink, retained.introducedRevision); err != nil {
			return err
		}
		if err := separator(sink); err != nil {
			return err
		}
		if err := writeQuotedRaw(sink, claim.Subject); err != nil {
			return err
		}
		if err := separator(sink); err != nil {
			return err
		}
		if err := sink.byte('0' + byte(key)); err != nil {
			return err
		}
		if err := separator(sink); err != nil {
			return err
		}
		if err := sink.byte('0' + value); err != nil {
			return err
		}
		source := observation.Source()
		if err := writeSourceFields(sink, source); err != nil {
			return err
		}
		if err := separator(sink); err != nil {
			return err
		}
		if err := writeTimestamp(sink, observation.ObservedAt()); err != nil {
			return err
		}
		if err := separator(sink); err != nil {
			return err
		}
		if err := sink.byte('0' + confidence); err != nil {
			return err
		}
		if err := separator(sink); err != nil {
			return err
		}
		if err := sink.byte('0' + review); err != nil {
			return err
		}
		if err := sink.byte(']'); err != nil {
			return err
		}
	}
	return sink.byte(']')
}

func treg1MetadataState(state MetadataState) (byte, bool) {
	switch state {
	case MetadataUnknown:
		return 0, true
	case MetadataObservedTrue:
		return 1, true
	case MetadataObservedFalse:
		return 2, true
	case MetadataConflict:
		return 3, true
	default:
		return 0, false
	}
}

func treg1Confidence(confidence evidence.Confidence) (byte, bool) {
	switch confidence {
	case evidence.ConfidenceUnknown:
		return 0, true
	case evidence.Tentative:
		return 1, true
	case evidence.Corroborated:
		return 2, true
	default:
		return 0, false
	}
}

func treg1Review(review evidence.Review) (byte, bool) {
	switch review {
	case evidence.Pending:
		return 0, true
	case evidence.Accepted:
		return 1, true
	case evidence.Disputed:
		return 2, true
	case evidence.Rejected:
		return 3, true
	default:
		return 0, false
	}
}

func writeSourceFields(sink *tregSink, source evidence.Source) error {
	for _, field := range [3]string{source.Name(), source.Revision(), source.Record()} {
		if err := separator(sink); err != nil {
			return err
		}
		if err := writeEscapedSource(sink, field); err != nil {
			return err
		}
	}
	return nil
}

func writeDecisions(sink *tregSink, view retainedTagView) error {
	if err := sink.byte('['); err != nil {
		return err
	}
	for index, decision := range view.decisions {
		if index > 0 {
			if err := separator(sink); err != nil {
				return err
			}
		}
		if !validDecisionID(decision.id) || decision.key >= retainedFactKeyCount ||
			decision.expectedRevision <= 0 || decision.expectedRevision == Revision(^uint64(0)>>1) ||
			decision.resultRevision != decision.expectedRevision+1 || decision.resultRevision > view.revision {
			return ErrInvalidRetainedEncoding
		}
		if err := sink.byte('['); err != nil {
			return err
		}
		if err := writeQuotedRaw(sink, decision.id.String()); err != nil {
			return err
		}
		if err := separator(sink); err != nil {
			return err
		}
		if err := sink.byte('0' + byte(decision.key)); err != nil {
			return err
		}
		if err := separator(sink); err != nil {
			return err
		}
		if err := writeRevision(sink, decision.expectedRevision); err != nil {
			return err
		}
		if err := separator(sink); err != nil {
			return err
		}
		if err := writeRevision(sink, decision.resultRevision); err != nil {
			return err
		}
		if err := separator(sink); err != nil {
			return err
		}
		if err := writeEvidenceIDs(sink, decision.selected); err != nil {
			return err
		}
		if err := sink.byte(']'); err != nil {
			return err
		}
	}
	return sink.byte(']')
}

func writeEvidenceIDs(sink *tregSink, ids []evidence.ID) error {
	if err := sink.byte('['); err != nil {
		return err
	}
	for index, id := range ids {
		if !validEvidenceID(id) {
			return ErrInvalidRetainedEncoding
		}
		if index > 0 {
			if err := separator(sink); err != nil {
				return err
			}
		}
		if err := writeQuotedRaw(sink, id.String()); err != nil {
			return err
		}
	}
	return sink.byte(']')
}

func writeEscapedSource(sink *tregSink, value string) error {
	if !evidence.ValidText(value, 128) {
		return ErrInvalidRetainedEncoding
	}
	if err := sink.byte('"'); err != nil {
		return err
	}
	start := 0
	for index, runeValue := range value {
		escaped := ""
		switch runeValue {
		case '"':
			escaped = `\"`
		case '\\':
			escaped = `\\`
		case '<':
			escaped = `\u003c`
		case '>':
			escaped = `\u003e`
		case '&':
			escaped = `\u0026`
		case '\u2028':
			escaped = `\u2028`
		case '\u2029':
			escaped = `\u2029`
		}
		if escaped == "" {
			continue
		}
		if err := sink.raw(value[start:index]); err != nil {
			return err
		}
		if err := sink.raw(escaped); err != nil {
			return err
		}
		start = index + utf8.RuneLen(runeValue)
	}
	if err := sink.raw(value[start:]); err != nil {
		return err
	}
	return sink.byte('"')
}

func writeTimestamp(sink *tregSink, value time.Time) error {
	if !validObservedAt(value) {
		return ErrInvalidRetainedEncoding
	}
	utc := value.Round(0).UTC()
	if err := sink.byte('"'); err != nil {
		return err
	}
	parts := []struct {
		value int
		width int
		after byte
	}{
		{utc.Year(), 4, '-'},
		{int(utc.Month()), 2, '-'},
		{utc.Day(), 2, 'T'},
		{utc.Hour(), 2, ':'},
		{utc.Minute(), 2, ':'},
		{utc.Second(), 2, '.'},
		{utc.Nanosecond(), 9, 'Z'},
	}
	for _, part := range parts {
		if err := writeFixedUnsigned(sink, part.value, part.width); err != nil {
			return err
		}
		if err := sink.byte(part.after); err != nil {
			return err
		}
	}
	return sink.byte('"')
}

func writeFixedUnsigned(sink *tregSink, value, width int) error {
	divisor := 1
	for range width - 1 {
		divisor *= 10
	}
	for divisor > 0 {
		if err := sink.byte(byte('0' + value/divisor%10)); err != nil {
			return err
		}
		divisor /= 10
	}
	return nil
}

func writeRevision(sink *tregSink, revision Revision) error {
	if revision <= 0 {
		return ErrInvalidRetainedEncoding
	}
	value := int64(revision)
	divisor := int64(1)
	for divisor <= value/10 {
		divisor *= 10
	}
	for divisor > 0 {
		if err := sink.byte(byte('0' + value/divisor%10)); err != nil {
			return err
		}
		divisor /= 10
	}
	return nil
}

func writeQuotedRaw(sink *tregSink, value string) error {
	if err := sink.byte('"'); err != nil {
		return err
	}
	if err := sink.raw(value); err != nil {
		return err
	}
	return sink.byte('"')
}

func writeUID(sink *tregSink, uid UID) error {
	const hexadecimal = "0123456789ABCDEF"
	value := uid.Bytes()
	if err := sink.byte('"'); err != nil {
		return err
	}
	for index, item := range value {
		if index > 0 {
			if err := sink.byte(':'); err != nil {
				return err
			}
		}
		if err := sink.byte(hexadecimal[item>>4]); err != nil {
			return err
		}
		if err := sink.byte(hexadecimal[item&0x0f]); err != nil {
			return err
		}
	}
	return sink.byte('"')
}
func separator(sink *tregSink) error { return sink.byte(',') }

func validTagID(id identity.TagID) bool {
	if id.IsZero() {
		return false
	}
	parsed, err := identity.ParseTagID(id.String())
	return err == nil && parsed == id
}

func validEvidenceID(id evidence.ID) bool {
	if id.IsZero() {
		return false
	}
	parsed, err := evidence.ParseID(id.String())
	return err == nil && parsed == id
}

func validDecisionID(id DecisionID) bool {
	if id.IsZero() {
		return false
	}
	parsed, err := ParseDecisionID(id.String())
	return err == nil && parsed == id
}

func validSource(source evidence.Source) bool {
	return evidence.ValidText(source.Name(), 128) &&
		evidence.ValidText(source.Revision(), 128) &&
		evidence.ValidText(source.Record(), 128)
}

func validObservedAt(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	year := value.Round(0).UTC().Year()
	return year >= 0 && year <= 9999
}

func retainedKeyForClaim(value string) (retainedFactKey, bool) {
	switch value {
	case "protocol_valid":
		return retainedProtocolValid, true
	case "claimed":
		return retainedClaimed, true
	case "cloud_auth":
		return retainedCloudAuth, true
	case "owned":
		return retainedOwned, true
	default:
		return 0, false
	}
}

func retainedBoolForClaim(value string) (byte, bool) {
	switch value {
	case "false":
		return 0, true
	case "true":
		return 1, true
	default:
		return 0, false
	}
}
