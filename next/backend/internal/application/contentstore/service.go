package contentstore

import (
	"context"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
)

// Service couples media operations to the selected database owner. Its store
// is attached once, so restore/reference mutations invalidate scans even when
// they originate outside this service.
type Service struct {
	owner OperationOwner
	media MediaStore
}

func NewService(ctx context.Context, owner MediaOwner, media MediaStore) (*Service, error) {
	if ctx == nil || owner == nil || media == nil {
		return nil, ErrInvalidInput
	}
	if err := owner.AttachContentInventory(ctx, media); err != nil {
		return nil, err
	}
	return &Service{owner: owner, media: media}, nil
}

// Import transfers source ownership only when media publication is admitted.
// Rejected gate admission leaves it with the caller. Publication failure keeps
// retained bytes; no result is advertised until the DB phase completes.
func (s *Service) Import(ctx context.Context, key content.ImportKey, command content.ImportCommand, source content.TAFSource, mode content.TAFSourceMode) (result ImportResult, err error) {
	if s == nil || ctx == nil || key.IsZero() || command.IsZero() || source == nil {
		return result, ErrInvalidInput
	}
	duration := s.media.ImportDuration()
	if duration <= 0 || duration > content.HardImportDuration {
		return result, ErrInvalidInput
	}
	// Start before owner admission; every nested phase inherits this deadline.
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	err = s.owner.WithinContentOperation(ctx, func(ctx context.Context, session Session) error {
		envelope, err := s.media.Publish(ctx, command.BlobID(), command.CompleteBytes(), source, mode)
		if err != nil {
			return err
		}
		if !command.MatchesEnvelope(envelope) {
			return ErrMismatch
		}
		return session.WithinTransaction(ctx, func(repository BlobRepository) error {
			var err error
			result, err = repository.RecordImport(ctx, key, command)
			return err
		})
	})
	if err != nil {
		return ImportResult{}, err
	}
	return result, nil
}

func (s *Service) LookupImport(ctx context.Context, key content.ImportKey, command content.ImportCommand) (result ImportResult, found bool, err error) {
	if s == nil || ctx == nil || key.IsZero() || command.IsZero() {
		return result, false, ErrInvalidInput
	}
	err = s.owner.WithinContentOperation(ctx, func(ctx context.Context, session Session) error {
		return session.WithinTransaction(ctx, func(repository BlobRepository) error {
			var err error
			result, found, err = repository.LookupImport(ctx, key, command)
			return err
		})
	})
	if err != nil {
		return ImportResult{}, false, err
	}
	return result, found, nil
}

func (s *Service) Inventory(ctx context.Context, cursor string, size int) (page InventoryPage, err error) {
	if s == nil || ctx == nil {
		return page, ErrInvalidInput
	}
	err = s.owner.WithinContentOperation(ctx, func(ctx context.Context, session Session) error {
		var err error
		page, err = s.media.Inventory(ctx, cursor, size, session.References)
		return err
	})
	if err != nil {
		return InventoryPage{}, err
	}
	return page, nil
}

// Availability performs fresh, full descriptor verification and makes no
// metadata update. Quarantine subsequently reports typed missing here.
func (s *Service) Availability(ctx context.Context, id content.BlobID, size uint64) error {
	if s == nil || ctx == nil {
		return ErrInvalidInput
	}
	return s.owner.WithinContentOperation(ctx, func(ctx context.Context, _ Session) error {
		return s.media.Verify(ctx, id, size)
	})
}

func (s *Service) Quarantine(ctx context.Context, id content.BlobID, size uint64) error {
	if s == nil || ctx == nil {
		return ErrInvalidInput
	}
	return s.owner.WithinContentOperation(ctx, func(ctx context.Context, _ Session) error {
		return s.media.Quarantine(ctx, id, size)
	})
}
