//go:build linux

package contentfs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"golang.org/x/sys/unix"
)

type inventoryDirectory struct {
	file *os.File
	path string
}
type inventoryScan struct {
	stack                           []inventoryDirectory
	phase                           int
	cursor                          string
	last                            time.Time
	stageEntries, quarantineEntries int
	stageBytes, quarantineBytes     uint64
	diagnostics                     bool
	idle                            *time.Timer
}

// OpenForInventory permits observation of retained entries after restart. Media
// mutations remain disabled until a complete clean bounded inventory reconciles
// capacity. Ordinary Open retains its fail-closed ErrRetained startup behavior.
func openForInventory(ctx context.Context, root string, options content.TAFOptions) (*Store, error) {
	return openInventoryStore(ctx, root, options, syscalls{}, true)
}

// Inventory accepts only a Store-generated, single-use in-memory cursor. A
// partial page holds at most four directory iterators; no global sort/list.
func (s *Store) Inventory(ctx context.Context, cursor string, pageSize int, references ReferenceLookup) (page InventoryPage, failure error) {
	if s == nil || ctx == nil || references == nil || len(cursor) > MaxInventoryCursor || pageSize < 0 || pageSize > MaxInventoryPage {
		return page, ErrInvalid
	}
	if pageSize == 0 {
		pageSize = DefaultInventoryPage
	}
	if ctx.Err() != nil {
		return page, ErrCanceled
	}
	if !s.mu.TryLock() {
		return page, ErrBusy
	}
	defer s.mu.Unlock()
	if !s.initialized || s.closed || s.poisoned {
		return page, ErrUnavailable
	}
	if s.scan != nil && time.Since(s.scan.last) >= InventoryIdleExpiry {
		s.invalidateInventory()
	}
	if s.poisoned {
		return page, ErrUnavailable
	}
	if cursor == "" {
		if s.scan != nil {
			return page, ErrBusy
		}
		s.scan = &inventoryScan{}
	} else if s.scan == nil || cursor != s.scan.cursor {
		return page, ErrCursor
	}
	if s.scan.idle != nil {
		s.scan.idle.Stop()
	}
	defer func() {
		if failure != nil {
			s.invalidateInventory()
		}
	}()
	bounded, cancel := context.WithTimeout(ctx, InventoryDuration)
	defer cancel()
	deadline, _ := bounded.Deadline()
	expired := func() bool { return bounded.Err() != nil || !time.Now().Before(deadline) }
	scan := s.scan
	for page.Inspected < MaxInventoryInspected && len(page.Entries) < pageSize {
		if ctx.Err() != nil {
			return InventoryPage{}, ErrCanceled
		}
		if expired() {
			break
		}
		if s.ops.check("inventory-entry") != nil {
			return InventoryPage{}, ErrUnavailable
		}
		if expired() {
			break
		}
		if len(scan.stack) == 0 {
			if scan.phase == 4 {
				s.stageEntries, s.stageBytes = scan.stageEntries, scan.stageBytes
				s.quarantineEntries, s.quarantineBytes = scan.quarantineEntries, scan.quarantineBytes
				s.capacityKnown = !scan.diagnostics && scan.stageEntries <= maxStageEntries && scan.stageBytes <= maxStageBytes && scan.quarantineEntries <= maxQuarantineEntries && scan.quarantineBytes <= maxQuarantineBytes
				s.invalidateInventory()
				page.Complete = true
				return page, nil
			}
			fd := []int{s.root, s.staging, s.quarantine, s.blobs}[scan.phase]
			path := []string{"", "staging", "quarantine", "blobs"}[scan.phase]
			scan.phase++
			if err := s.pushInventory(fd, ".", path); err != nil {
				return InventoryPage{}, err
			}
		}
		dir := &scan.stack[len(scan.stack)-1]
		names, err := dir.file.Readdirnames(1)
		if err == io.EOF {
			if dir.file.Close() != nil {
				s.poisoned = true
				return InventoryPage{}, ErrUnavailable
			}
			scan.stack = scan.stack[:len(scan.stack)-1]
			continue
		}
		if err != nil || len(names) != 1 {
			return InventoryPage{}, ErrUnavailable
		}
		page.Inspected++
		entry, emit, err := s.inspectInventory(bounded, *dir, names[0], references)
		if err != nil {
			return InventoryPage{}, err
		}
		if emit {
			page.Entries = append(page.Entries, entry)
		}
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return InventoryPage{}, ErrUnavailable
	}
	scan.cursor = hex.EncodeToString(entropy[:])
	scan.last = time.Now()
	page.Cursor = scan.cursor
	token := scan.cursor
	scan.idle = time.AfterFunc(InventoryIdleExpiry, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.scan == scan && scan.cursor == token && time.Since(scan.last) >= InventoryIdleExpiry {
			s.invalidateInventory()
		}
	})
	return page, nil
}

func (s *platformStore) pushInventory(parent int, name, path string) error {
	if len(s.scan.stack) >= 4 {
		return ErrUnavailable
	}
	fd, err := s.ops.open(parent, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return ErrUnavailable
	}
	if s.checkDir(fd) != nil {
		unix.Close(fd)
		return ErrUnavailable
	}
	s.scan.stack = append(s.scan.stack, inventoryDirectory{os.NewFile(uintptr(fd), "inventory"), path})
	return nil
}

func (s *platformStore) inspectInventory(ctx context.Context, dir inventoryDirectory, name string, references ReferenceLookup) (InventoryEntry, bool, error) {
	entry := InventoryEntry{Kind: UnexpectedEntry}
	var st unix.Stat_t
	if unix.Fstatat(int(dir.file.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
		return entry, false, ErrUnavailable
	}
	if st.Size > 0 {
		entry.Bytes = uint64(st.Size)
	}
	regular := st.Mode&unix.S_IFMT == unix.S_IFREG && st.Nlink == 1 && st.Uid == uint32(os.Geteuid()) && uint64(st.Dev) == s.device && st.Mode&07777 == 0600
	if dir.path == "" && (name == "staging" || name == "quarantine" || name == "blobs") && st.Mode&unix.S_IFMT == unix.S_IFDIR {
		return entry, false, nil
	}
	if dir.path == "" && name == ".owner.lock" && regular {
		return entry, false, nil
	}
	parts := strings.Split(dir.path, "/")
	if parts[0] == "blobs" && st.Mode&unix.S_IFMT == unix.S_IFDIR && (len(parts) == 1 && name == "sha256" || len(parts) >= 2 && len(parts) <= 3 && lowerHex(name, 2)) {
		return entry, false, s.pushInventory(int(dir.file.Fd()), name, dir.path+"/"+name)
	}
	if regular {
		switch dir.path {
		case "staging":
			if generatedName(name, "s-") {
				entry.Kind = StagingEntry
				s.scan.stageEntries++
				s.scan.stageBytes = cappedInventoryBytes(s.scan.stageBytes, entry.Bytes, maxStageBytes)
			}
		case "quarantine":
			if generatedName(name, "q-") {
				entry.Kind = QuarantineEntry
				s.scan.quarantineEntries++
				s.scan.quarantineBytes = cappedInventoryBytes(s.scan.quarantineBytes, entry.Bytes, maxQuarantineBytes)
			}
		default:
			if len(parts) == 4 && parts[0] == "blobs" && parts[1] == "sha256" && len(name) == 68 && strings.HasSuffix(name, ".taf") && lowerHex(name[:64], 64) && name[:2] == parts[2] && name[2:4] == parts[3] {
				var digest [32]byte
				decoded, _ := hex.DecodeString(name[:64])
				copy(digest[:], decoded)
				entry.BlobID = content.NewBlobID(digest)
				referenced, err := references(ctx, entry.BlobID)
				if ctx.Err() != nil {
					return entry, false, ErrCanceled
				}
				if err != nil {
					return entry, false, ErrUnavailable
				}
				entry.Kind = UnreferencedCanonical
				if referenced {
					entry.Kind = ReferencedCanonical
				}
			}
		}
	}
	if entry.Kind == UnexpectedEntry {
		s.scan.diagnostics = true
	}
	return entry, true, nil
}

func lowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func generatedName(name, prefix string) bool {
	return len(name) == len(prefix)+32+4 && strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".taf") && lowerHex(name[len(prefix):len(prefix)+32], 32)
}

// Saturate one above the hard bound; arbitrarily large sparse retained files
// must not overflow counters back into admissible capacity.
func cappedInventoryBytes(total, bytes, cap uint64) uint64 {
	if total > cap || bytes > cap-total {
		return cap + 1
	}
	return total + bytes
}

// InvalidateInventory is the adapter hook for reference mutation/restore under
// the common lifecycle gate. Tokens never survive a change of selected DB.
func (s *Store) InvalidateInventory() error {
	if s == nil {
		return ErrInvalid
	}
	if !s.mu.TryLock() {
		return ErrBusy
	}
	defer s.mu.Unlock()
	if s.closed || !s.initialized || s.poisoned {
		return ErrUnavailable
	}
	s.invalidateInventory()
	if s.poisoned {
		return ErrUnavailable
	}
	return nil
}

func (s *platformStore) invalidateInventory() {
	if s.scan != nil {
		if s.scan.idle != nil {
			s.scan.idle.Stop()
		}
		for _, dir := range s.scan.stack {
			if dir.file.Close() != nil {
				s.poisoned = true
			}
		}
		s.scan = nil
	}
}
