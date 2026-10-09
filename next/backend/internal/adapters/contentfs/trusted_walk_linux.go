//go:build linux

package contentfs

import "golang.org/x/sys/unix"

// trustedDirectory retains the checked chain until the operation finishes.
// The base is borrowed from the Store; only descendants belong to this walk.
type trustedDirectory struct {
	base  int
	names []string
	fds   []int
}

// walkTrustedDirectory never creates or synchronizes directories. Every open
// uses the same openat2 confinement as blob I/O, followed by descriptor checks.
func (s *platformStore) walkTrustedDirectory(base int, names ...string) (*trustedDirectory, error) {
	walk := &trustedDirectory{base: base, names: names}
	if s.checkDir(s.root) != nil || s.checkDir(base) != nil {
		return nil, ErrUnavailable
	}
	parent := base
	for _, name := range names {
		fd, err := s.ops.open(parent, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if err != nil {
			if walk.close(s) != nil {
				return nil, ErrUnavailable
			}
			return nil, err
		}
		walk.fds = append(walk.fds, fd)
		if s.checkDir(fd) != nil || !sameEntry(parent, name, fd) {
			_ = walk.close(s)
			return nil, ErrUnavailable
		}
		parent = fd
	}
	return walk, nil
}

func (w *trustedDirectory) leaf() int {
	if len(w.fds) == 0 {
		return w.base
	}
	return w.fds[len(w.fds)-1]
}

// Recheck trust and directory-entry identity before output/mutation and after
// delivery. This detects observed path swaps without reopening the blob by path.
// Hostile arbitrary same-owner mutation remains outside the trust contract.
func (w *trustedDirectory) valid(s *platformStore) bool {
	if s.checkDir(s.root) != nil || s.checkDir(w.base) != nil {
		return false
	}
	parent := w.base
	for i, fd := range w.fds {
		if s.checkDir(fd) != nil || !sameEntry(parent, w.names[i], fd) {
			return false
		}
		parent = fd
	}
	return true
}

func (w *trustedDirectory) close(s *platformStore) error {
	failed := false
	for i := len(w.fds) - 1; i >= 0; i-- {
		if unix.Close(w.fds[i]) != nil {
			failed = true
		}
	}
	w.fds = nil
	if failed {
		s.poisoned = true
		return ErrUnavailable
	}
	return nil
}
