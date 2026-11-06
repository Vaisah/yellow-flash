package sqlite

import (
	"context"
	"database/sql"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/infrastructure/persistence/sqlite/repositories"
)

// Store composes the SQLite repositories. It satisfies domain.Store, so it
// can be handed to the engine directly.
type Store struct {
	db *sql.DB
	*repositories.Downloads
	*repositories.Segments
	settings *repositories.Settings
	queues   *repositories.Queues
}

var _ domain.Store = (*Store)(nil)

// Open opens (creating if needed) and migrates the database at path.
func Open(path string) (*Store, error) { return OpenContext(context.Background(), path) }

// OpenContext is Open with a context for the initial connection and migration.
func OpenContext(ctx context.Context, path string) (*Store, error) {
	db, err := openDB(ctx, path)
	if err != nil {
		return nil, err
	}
	return &Store{
		db:        db,
		Downloads: repositories.NewDownloads(db),
		Segments:  repositories.NewSegments(db),
		settings:  repositories.NewSettings(db),
		queues:    repositories.NewQueues(db),
	}, nil
}

// Close closes the database. Shut the engine down first.
func (s *Store) Close() error { return s.db.Close() }

// Settings returns the settings repository.
func (s *Store) Settings() *repositories.Settings { return s.settings }

// Queues returns the queue repository.
func (s *Store) Queues() *repositories.Queues { return s.queues }
