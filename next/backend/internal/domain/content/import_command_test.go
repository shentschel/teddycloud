package content

import (
	"bytes"
	"encoding/hex"
	"math"
	"strings"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

func commandVersion(t *testing.T, order catalog.VersionOrderEvidence, audio uint64, hash string) catalog.ContentVersion {
	t.Helper()
	cid, err := identity.ParseContentID("cnt_" + strings.Repeat("0", 26))
	if err != nil {
		t.Fatal(err)
	}
	vid, err := identity.ParseContentVersionID("ver_" + strings.Repeat("1", 26))
	if err != nil {
		t.Fatal(err)
	}
	aid, err := catalog.NewAudioID(audio)
	if err != nil {
		t.Fatal(err)
	}
	ah, err := catalog.ParseAudioHash(hash)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := catalog.NewAudioFingerprint(aid, ah)
	if err != nil {
		t.Fatal(err)
	}
	v, err := catalog.NewContentVersion(vid, cid, fp, order)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestImportCommandGolden(t *testing.T) {
	// Manually laid out fields, independent of the production encoder. Hashes
	// independently computed from these literal bytes; IDs are synthetic zeros/ones.
	const unknownHex = "5443494d504f525401636e745f30303030303030303030303030303030303030303030303030307665725f31313131313131313131313131313131313131313131313131310100000000000000000000000000000000000000000000000000000000000000000000000000001001000101020304111111111111111111111111111111111111111100"
	const knownHex = "5443494d504f525401636e745f30303030303030303030303030303030303030303030303030307665725f313131313131313131313131313131313131313131313131313101000000000000000000000000000000000000000000000000000000000000000000000000000010010001010203041111111111111111111111111111111111111111010001780102030405060708"
	known, _ := catalog.NewVersionOrderEvidence("x", 0x0102030405060708)
	for _, tc := range []struct {
		name, raw, hash string
		order           catalog.VersionOrderEvidence
		length          int
	}{
		{"unknown", unknownHex, "ac0ab7362399b0abb3bed6f31aa19316c0bfa3854e6d85c08da7cd4d5bff4cbe", catalog.UnknownVersionOrderEvidence(), 137},
		{"known", knownHex, "f0bea9ef174776e60eb91d54a2e95797a15b104d753d334a7913050561388548", known, 148},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want, err := hex.DecodeString(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			v := commandVersion(t, tc.order, 0x01020304, strings.Repeat("11", 20))
			c, err := NewImportCommand(v, NewBlobID([32]byte{}), 4097, 1)
			if err != nil || len(c.CanonicalBytes()) != tc.length || !bytes.Equal(c.CanonicalBytes(), want) {
				t.Fatalf("golden mismatch %x %v", c.CanonicalBytes(), err)
			}
			h := c.Fingerprint()
			if hex.EncodeToString(h[:]) != tc.hash {
				t.Fatal("fingerprint mismatch")
			}
			parsed, err := ParseImportCommand(want)
			if err != nil || !parsed.SameCommand(c) || parsed.Version() != v || parsed.Fingerprint() != c.Fingerprint() {
				t.Fatal("round trip", err)
			}
			want[0] ^= 1
			c.CanonicalBytes()[0] ^= 1
			if parsed.CanonicalBytes()[0] != 'T' || c.CanonicalBytes()[0] != 'T' {
				t.Fatal("bytes not independently retained")
			}
		})
	}
	for _, position := range []uint64{0, math.MaxUint64} {
		order, _ := catalog.NewVersionOrderEvidence(strings.Repeat("~", 128), position)
		c, err := NewImportCommand(commandVersion(t, order, math.MaxUint32, strings.Repeat("00", 20)), NewBlobID([32]byte{}), HardTAFBytes, 1)
		if err != nil || len(c.CanonicalBytes()) != 275 {
			t.Fatal(err)
		}
		parsed, err := ParseImportCommand(c.CanonicalBytes())
		if err != nil || !parsed.SameCommand(c) {
			t.Fatal(err)
		}
	}
}

func TestImportCommandRejectsMalformed(t *testing.T) {
	v := commandVersion(t, catalog.UnknownVersionOrderEvidence(), 1, strings.Repeat("00", 20))
	c, _ := NewImportCommand(v, NewBlobID([32]byte{}), 4097, 1)
	for _, mutate := range []func([]byte) []byte{
		func(d []byte) []byte { return d[:136] }, func(d []byte) []byte { return append(d, 0) },
		func(d []byte) []byte { d[0] = 'X'; return d }, func(d []byte) []byte { d[8] = 2; return d },
		func(d []byte) []byte { d[9] = 'v'; return d }, func(d []byte) []byte { d[13] = 'I'; return d },
		func(d []byte) []byte { d[39] = 'c'; return d }, func(d []byte) []byte { d[69] = 2; return d },
		func(d []byte) []byte { d[110] = 1; return d }, func(d []byte) []byte { d[111] = 2; return d },
		func(d []byte) []byte { d[109] = 0; return d }, func(d []byte) []byte { d[102] = 1; return d },
		func(d []byte) []byte { d[115] = 0; return d }, func(d []byte) []byte { d[136] = 2; return d },
		func(d []byte) []byte { d[136] = 1; return d },
	} {
		if _, err := ParseImportCommand(mutate(c.CanonicalBytes())); err != ErrInvalidImportCommand {
			t.Fatal("accepted mutation", err)
		}
	}
	known, _ := catalog.NewVersionOrderEvidence("x", 0)
	k, _ := NewImportCommand(commandVersion(t, known, 1, strings.Repeat("00", 20)), NewBlobID([32]byte{}), 4097, 1)
	for _, mutate := range []func([]byte) []byte{
		func(d []byte) []byte { d[138] = 0; return d }, func(d []byte) []byte { d[138] = 129; return d },
		func(d []byte) []byte { d[139] = ' '; return d }, func(d []byte) []byte { d[139] = 0x80; return d },
		func(d []byte) []byte { return d[:len(d)-1] }, func(d []byte) []byte { return append(d, 0) },
	} {
		if _, err := ParseImportCommand(mutate(k.CanonicalBytes())); err != ErrInvalidImportCommand {
			t.Fatal("accepted order mutation", err)
		}
	}
	for _, tc := range []struct {
		v       catalog.ContentVersion
		id      BlobID
		size    uint64
		profile uint16
	}{
		{catalog.ContentVersion{}, NewBlobID([32]byte{}), 4097, 1}, {v, BlobID{}, 4097, 1},
		{v, NewBlobID([32]byte{}), 4096, 1}, {v, NewBlobID([32]byte{}), HardTAFBytes + 1, 1},
		{v, NewBlobID([32]byte{}), 4097, 0}, {v, NewBlobID([32]byte{}), 4097, 2},
		{commandVersion(t, catalog.UnknownVersionOrderEvidence(), uint64(math.MaxUint32)+1, strings.Repeat("00", 20)), NewBlobID([32]byte{}), 4097, 1},
	} {
		if _, err := NewImportCommand(tc.v, tc.id, tc.size, tc.profile); err != ErrInvalidImportCommand {
			t.Fatal(err)
		}
	}
	if !(ImportCommand{}).IsZero() || (ImportCommand{}).Encoding() != 0 || (ImportCommand{}).Profile() != 0 || (ImportCommand{}).SameCommand(ImportCommand{}) {
		t.Fatal("zero command")
	}
}

func TestImportKeyAndEnvelopeBinding(t *testing.T) {
	for _, key := range []string{"imp_" + strings.Repeat("0", 26), "imp_" + strings.Repeat("z", 26)} {
		k, err := ParseImportKey(key)
		if err != nil || k.String() != key || k.IsZero() {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"", "imp_" + strings.Repeat("0", 25), "imp_" + strings.Repeat("0", 27), "imp_" + strings.Repeat("i", 26), "IMP_" + strings.Repeat("0", 26), "imp_" + strings.Repeat("0", 25) + "/", "imp_" + strings.Repeat("0", 25) + "\x00", "imp_" + strings.Repeat("0", 25) + " "} {
		if _, err := ParseImportKey(key); err != ErrInvalidImportKey {
			t.Fatal("accepted key", err)
		}
	}
	e, err := validateBytes(testFile([]byte("abc"), nil))
	if err != nil {
		t.Fatal(err)
	}
	h := e.PayloadSHA1()
	v := commandVersion(t, catalog.UnknownVersionOrderEvidence(), uint64(e.AudioID()), hex.EncodeToString(h[:]))
	c, err := NewImportCommand(v, e.BlobID(), e.CompleteBytes(), e.Profile())
	if err != nil || !c.MatchesEnvelope(e) {
		t.Fatal(err)
	}
	other, err := validateBytes(testFile([]byte("abd"), nil))
	if err != nil || c.MatchesEnvelope(other) || c.MatchesEnvelope(TAFEnvelope{}) {
		t.Fatal("false binding", err)
	}
	// Changing declared complete facts does not change the independently supplied
	// editorial identities; it changes the retained command/fingerprint.
	changed, _ := NewImportCommand(v, e.BlobID(), e.CompleteBytes()+1, 1)
	if changed.SameCommand(c) || changed.Fingerprint() == c.Fingerprint() || changed.Version().ID() != c.Version().ID() {
		t.Fatal("identity/command conflation")
	}
}
