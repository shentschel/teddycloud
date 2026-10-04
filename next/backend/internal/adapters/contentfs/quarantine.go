//go:build linux

package contentfs

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"golang.org/x/sys/unix"
)

const (
	maxQuarantineBytes   uint64 = 2 * 1024 * 1024 * 1024
	maxQuarantineEntries        = 128
)

// Quarantine performs a fresh full verification under the Store's exclusive
// operation/digest lease, then moves only corrupt regular bytes. Call within the
// common lifecycle gate (as with Inventory/Publish); no DB reference is changed.
// No earlier availability result or caller-supplied verification receipt is used.
func (s *Store) Quarantine(ctx context.Context, id content.BlobID, completeBytes uint64) (failure error) {
	if s == nil || ctx == nil || id.IsZero() || completeBytes < content.MinTAFBytes || completeBytes > content.HardTAFBytes {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.ranges.duration)
	defer cancel()
	if ctx.Err() != nil {
		return ErrCanceled
	}
	if !s.mu.TryLock() {
		return ErrBusy
	}
	defer s.mu.Unlock()
	if !s.initialized || s.closed || s.poisoned {
		return ErrUnavailable
	}
	if !s.capacityKnown {
		return ErrRetained
	}
	digest := id.String()[7:]
	parent, err := s.ops.open(s.blobs, "sha256/"+digest[:2]+"/"+digest[2:4], unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err == unix.ENOENT {
		return ErrMissing
	}
	if err != nil {
		return ErrUnavailable
	}
	defer func() {
		if unix.Close(parent) != nil {
			s.poisoned = true
			failure = ErrUnavailable
		}
	}()
	if s.checkDir(parent) != nil {
		return ErrUnavailable
	}
	name := digest + ".taf"
	fd, err := s.ops.open(parent, name, unix.O_RDONLY|unix.O_NONBLOCK, 0)
	if err == unix.ENOENT {
		return ErrMissing
	}
	if err != nil {
		return ErrUnavailable
	}
	defer func() {
		if unix.Close(fd) != nil {
			s.poisoned = true
			failure = ErrUnavailable
		}
	}()
	before, err := s.checkFile(fd, -1)
	if err != nil || before.Size < 0 {
		return ErrUnavailable
	}
	// Reuse B2a/A2's full envelope/digest verification. Keep a descriptor to
	// revalidate its identity and mutation fields before any rename.
	// A caller's incorrect size declaration alone must not authorize moving
	// healthy content. Verify the actual complete descriptor against BlobID.
	err = s.verify(ctx, parent, name, id, uint64(before.Size))
	if err == nil {
		return ErrInvalid
	}
	if err != ErrCorrupt {
		return err
	}
	after, err := s.checkFile(fd, -1)
	if err != nil || !unchangedStat(before, after) || !sameEntry(parent, name, fd) {
		return ErrUnavailable
	}
	if ctx.Err() != nil {
		return ErrCanceled
	}
	bytes := uint64(after.Size)
	if s.quarantineEntries >= maxQuarantineEntries || s.quarantineBytes > maxQuarantineBytes || bytes > maxQuarantineBytes-s.quarantineBytes {
		return ErrCapacity
	}
	if s.ops.check("quarantine-verified") != nil {
		return ErrUnavailable
	}
	s.invalidateInventory()
	if s.poisoned {
		return ErrUnavailable
	}
	for attempt := 0; attempt < 3; attempt++ {
		var entropy [16]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return ErrUnavailable
		}
		dest := "q-" + hex.EncodeToString(entropy[:]) + ".taf"
		if ctx.Err() != nil {
			return ErrCanceled
		}
		current, err := s.checkFile(fd, -1)
		if err != nil || !unchangedStat(after, current) || !sameEntry(parent, name, fd) {
			return ErrUnavailable
		}
		err = s.ops.rename(parent, name, s.quarantine, dest)
		if err == unix.EEXIST {
			continue
		}
		if err != nil {
			return ErrUnavailable
		}
		s.quarantineEntries++
		s.quarantineBytes += bytes
		// Flush both even if the first fails; never acknowledge uncertain placement.
		sourceErr := s.ops.sync(parent, "quarantine-source-sync")
		destErr := s.ops.sync(s.quarantine, "quarantine-dest-sync")
		if sourceErr != nil || destErr != nil || !sameEntry(s.quarantine, dest, fd) {
			return ErrUncertain
		}
		if ctx.Err() != nil {
			return ErrCanceled
		}
		return nil
	}
	return ErrUnavailable
}
