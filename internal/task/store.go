package task

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// Store is the storage backend used by the CLI, TUI and web UI. SQLStore
// serves both local SQLite and Postgres; client.Store talks to a tt server.
type Store interface {
	Create(ctx context.Context, title string, opts CreateOpts) (*Task, error)
	Get(ctx context.Context, prefix string) (*Task, error)
	List(ctx context.Context, opts ListOpts) ([]Task, error)
	Recent(ctx context.Context, limit int) ([]Task, error)
	Update(ctx context.Context, prefix string, opts UpdateOpts) (*Task, error)
	Save(ctx context.Context, t *Task) error
	Search(ctx context.Context, query string) ([]Task, error)
	Next(ctx context.Context, handle string) (*Task, error)

	AddComment(ctx context.Context, taskPrefix, body string, author *string) (*Comment, error)
	GetComments(ctx context.Context, taskID string) ([]Comment, error)

	AddRelation(ctx context.Context, fromPrefix, relType, toPrefix string) error
	RemoveRelation(ctx context.Context, fromPrefix, relType, toPrefix string) error
	GetRelations(ctx context.Context, taskID string) ([]Relation, error)

	UpsertExternalRef(ctx context.Context, taskID, source, externalID string, url *string) error
	FindByExternalRef(ctx context.Context, source, externalID string) (*Task, error)
	GetExternalRefs(ctx context.Context, taskID string) ([]ExternalRef, error)

	Close() error
}

// SQLStore implements Store over a SQLite or Postgres database. It delegates
// to the package-level functions, whose SQL is portable across both; ctx is
// accepted for interface parity but not yet passed to queries.
type SQLStore struct {
	db *sqlx.DB
}

var _ Store = (*SQLStore)(nil)

func NewSQLStore(db *sqlx.DB) *SQLStore {
	return &SQLStore{db: db}
}

func (s *SQLStore) Create(ctx context.Context, title string, opts CreateOpts) (*Task, error) {
	return Create(s.db, title, opts)
}

func (s *SQLStore) Get(ctx context.Context, prefix string) (*Task, error) {
	return Get(s.db, prefix)
}

func (s *SQLStore) List(ctx context.Context, opts ListOpts) ([]Task, error) {
	return List(s.db, opts)
}

func (s *SQLStore) Recent(ctx context.Context, limit int) ([]Task, error) {
	return Recent(s.db, limit)
}

func (s *SQLStore) Update(ctx context.Context, prefix string, opts UpdateOpts) (*Task, error) {
	return Update(s.db, prefix, opts)
}

func (s *SQLStore) Save(ctx context.Context, t *Task) error {
	return Save(s.db, t)
}

func (s *SQLStore) Search(ctx context.Context, query string) ([]Task, error) {
	return Search(s.db, query)
}

func (s *SQLStore) Next(ctx context.Context, handle string) (*Task, error) {
	return Next(s.db, handle)
}

func (s *SQLStore) AddComment(ctx context.Context, taskPrefix, body string, author *string) (*Comment, error) {
	return AddComment(s.db, taskPrefix, body, author)
}

func (s *SQLStore) GetComments(ctx context.Context, taskID string) ([]Comment, error) {
	return GetComments(s.db, taskID)
}

func (s *SQLStore) AddRelation(ctx context.Context, fromPrefix, relType, toPrefix string) error {
	return AddRelation(s.db, fromPrefix, relType, toPrefix)
}

func (s *SQLStore) RemoveRelation(ctx context.Context, fromPrefix, relType, toPrefix string) error {
	return RemoveRelation(s.db, fromPrefix, relType, toPrefix)
}

func (s *SQLStore) GetRelations(ctx context.Context, taskID string) ([]Relation, error) {
	return GetRelations(s.db, taskID)
}

func (s *SQLStore) UpsertExternalRef(ctx context.Context, taskID, source, externalID string, url *string) error {
	return UpsertExternalRef(s.db, taskID, source, externalID, url)
}

func (s *SQLStore) FindByExternalRef(ctx context.Context, source, externalID string) (*Task, error) {
	return FindByExternalRef(s.db, source, externalID)
}

func (s *SQLStore) GetExternalRefs(ctx context.Context, taskID string) ([]ExternalRef, error) {
	return GetExternalRefs(s.db, taskID)
}

func (s *SQLStore) Close() error {
	return s.db.Close()
}

// Event reports a change to a project's tasks. TaskID and Action are empty
// when the source only knows that something changed (local SQLite).
type Event struct {
	TaskID string `json:"task_id,omitempty"`
	Action string `json:"action,omitempty"`
}

// Subscriber is implemented by stores that can report changes as they
// happen. The channel closes when ctx ends or the subscription can't
// continue.
type Subscriber interface {
	Subscribe(ctx context.Context) (<-chan Event, error)
}

// Subscribe polls a local SQLite database's mutation counter, which
// triggers bump on every write from any process (e.g. agents).
func (s *SQLStore) Subscribe(ctx context.Context) (<-chan Event, error) {
	if s.db.DriverName() != "sqlite" {
		return nil, fmt.Errorf("live updates need a tt server or a local database")
	}
	count := func() string {
		var v string
		s.db.GetContext(ctx, &v, `SELECT value FROM meta WHERE key = 'mutation_count'`)
		return v
	}
	last := count()
	ch := make(chan Event, 1)
	go func() {
		defer close(ch)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if c := count(); c != last {
					last = c
					select {
					case ch <- Event{}:
					default: // a refresh is already pending
					}
				}
			}
		}
	}()
	return ch, nil
}
