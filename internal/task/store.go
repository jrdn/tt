package task

import (
	"context"

	"github.com/jmoiron/sqlx"
)

// Store is the storage backend used by the CLI, TUI and web UI. SQLStore
// serves both local SQLite and Postgres; phase 4 adds an HTTP implementation.
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

func (s *SQLStore) Close() error {
	return s.db.Close()
}
