package content

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"strings"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

const ImportEncodingV1 uint8 = 1

var (
	ErrInvalidImportKey     = errors.New("invalid import key")
	ErrInvalidImportCommand = errors.New("invalid import command")
)

type ImportKey struct{ value string }

func ParseImportKey(text string) (ImportKey, error) {
	if len(text) != 30 || text[:4] != "imp_" {
		return ImportKey{}, ErrInvalidImportKey
	}
	for i := 4; i < len(text); i++ {
		if !strings.ContainsRune("0123456789abcdefghjkmnpqrstvwxyz", rune(text[i])) {
			return ImportKey{}, ErrInvalidImportKey
		}
	}
	return ImportKey{text}, nil
}
func (k ImportKey) String() string { return k.value }
func (k ImportKey) IsZero() bool   { return k.value == "" }

// ImportCommand retains canonical original command bytes, not merely their hash.
// ImportKey is deliberately separate and does not change the fingerprint.
type ImportCommand struct {
	version     catalog.ContentVersion
	blob        BlobID
	size        uint64
	canonical   string
	fingerprint [32]byte
}

func NewImportCommand(version catalog.ContentVersion, blob BlobID, size uint64, profile uint16) (ImportCommand, error) {
	if version.ID().IsZero() || version.ContentID().IsZero() || version.Fingerprint().IsZero() ||
		version.Fingerprint().AudioID().Uint64() > math.MaxUint32 || blob.IsZero() ||
		size < MinTAFBytes || size > HardTAFBytes || profile != TAFProfileV1 {
		return ImportCommand{}, ErrInvalidImportCommand
	}
	command := ImportCommand{version: version, blob: blob, size: size}
	data := make([]byte, 0, 275)
	data = append(data, "TCIMPORT"...)
	data = append(data, ImportEncodingV1)
	data = append(data, version.ContentID().String()...)
	data = append(data, version.ID().String()...)
	data = append(data, blob.Algorithm())
	digest := blob.Digest()
	data = append(data, digest[:]...)
	data = binary.BigEndian.AppendUint64(data, size)
	data = binary.BigEndian.AppendUint16(data, profile)
	data = binary.BigEndian.AppendUint32(data, uint32(version.Fingerprint().AudioID().Uint64()))
	hash, err := hex.DecodeString(version.Fingerprint().Hash().String())
	if err != nil || len(hash) != 20 {
		return ImportCommand{}, ErrInvalidImportCommand
	}
	data = append(data, hash...)
	order := version.OrderEvidence()
	if !order.Known() {
		data = append(data, 0)
	} else {
		data = append(data, 1)
		data = binary.BigEndian.AppendUint16(data, uint16(len(order.Namespace())))
		data = append(data, order.Namespace()...)
		data = binary.BigEndian.AppendUint64(data, order.Position())
	}
	command.canonical = string(data)
	command.fingerprint = sha256.Sum256(data)
	return command, nil
}

// ParseImportCommand rejects alternate encodings instead of normalizing them.
func ParseImportCommand(data []byte) (ImportCommand, error) {
	if len(data) < 137 || len(data) > 275 || string(data[:8]) != "TCIMPORT" || data[8] != ImportEncodingV1 || data[69] != 1 {
		return ImportCommand{}, ErrInvalidImportCommand
	}
	contentID, err := identity.ParseContentID(string(data[9:39]))
	if err != nil {
		return ImportCommand{}, ErrInvalidImportCommand
	}
	versionID, err := identity.ParseContentVersionID(string(data[39:69]))
	if err != nil {
		return ImportCommand{}, ErrInvalidImportCommand
	}
	var digest [32]byte
	copy(digest[:], data[70:102])
	size := binary.BigEndian.Uint64(data[102:110])
	profile := binary.BigEndian.Uint16(data[110:112])
	audioID, err := catalog.NewAudioID(uint64(binary.BigEndian.Uint32(data[112:116])))
	if err != nil {
		return ImportCommand{}, ErrInvalidImportCommand
	}
	hash, err := catalog.ParseAudioHash(hex.EncodeToString(data[116:136]))
	if err != nil {
		return ImportCommand{}, ErrInvalidImportCommand
	}
	order := catalog.UnknownVersionOrderEvidence()
	switch data[136] {
	case 0:
		if len(data) != 137 {
			return ImportCommand{}, ErrInvalidImportCommand
		}
	case 1:
		if len(data) < 148 {
			return ImportCommand{}, ErrInvalidImportCommand
		}
		n := int(binary.BigEndian.Uint16(data[137:139]))
		if n < 1 || n > 128 || len(data) != 147+n {
			return ImportCommand{}, ErrInvalidImportCommand
		}
		order, err = catalog.NewVersionOrderEvidence(string(data[139:139+n]), binary.BigEndian.Uint64(data[139+n:]))
		if err != nil {
			return ImportCommand{}, ErrInvalidImportCommand
		}
	default:
		return ImportCommand{}, ErrInvalidImportCommand
	}
	fingerprint, err := catalog.NewAudioFingerprint(audioID, hash)
	if err != nil {
		return ImportCommand{}, ErrInvalidImportCommand
	}
	version, err := catalog.NewContentVersion(versionID, contentID, fingerprint, order)
	if err != nil {
		return ImportCommand{}, ErrInvalidImportCommand
	}
	command, err := NewImportCommand(version, NewBlobID(digest), size, profile)
	if err != nil || !bytes.Equal(command.CanonicalBytes(), data) {
		return ImportCommand{}, ErrInvalidImportCommand
	}
	return command, nil
}
func (c ImportCommand) IsZero() bool                    { return c.canonical == "" }
func (c ImportCommand) Version() catalog.ContentVersion { return c.version }
func (c ImportCommand) BlobID() BlobID                  { return c.blob }
func (c ImportCommand) CompleteBytes() uint64           { return c.size }
func (c ImportCommand) Profile() uint16 {
	if c.IsZero() {
		return 0
	}
	return TAFProfileV1
}
func (c ImportCommand) Encoding() uint8 {
	if c.IsZero() {
		return 0
	}
	return ImportEncodingV1
}
func (c ImportCommand) CanonicalBytes() []byte { return []byte(c.canonical) }
func (c ImportCommand) Fingerprint() [32]byte  { return c.fingerprint }
func (c ImportCommand) SameCommand(other ImportCommand) bool {
	return !c.IsZero() && c.canonical == other.canonical
}

// MatchesEnvelope verifies every expected envelope fact without discovering an
// editorial identity or allocating a version from uploaded bytes.
func (c ImportCommand) MatchesEnvelope(e TAFEnvelope) bool {
	hash := e.PayloadSHA1()
	return !c.IsZero() && e.Profile() == c.Profile() && e.BlobID() == c.blob && e.CompleteBytes() == c.size &&
		uint64(e.AudioID()) == c.version.Fingerprint().AudioID().Uint64() &&
		hex.EncodeToString(hash[:]) == c.version.Fingerprint().Hash().String()
}
