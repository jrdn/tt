package task

import (
	"context"

	"github.com/jmoiron/sqlx"
)

// Store is the storage backend used by the CLI, TUI and web UI. Local mode
// uses SQLiteStore; later phases add Postgres and HTTP implementations.
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

// SQLiteStore implements Store over a local SQLite database. It delegates to
// the package-level functions; ctx is accepted for interface parity but not
// yet used by the SQLite queries.
type SQLiteStore struct {
	db *sqlx.DB
}

var _ Store = (*SQLiteStore)(nil)

func NewSQLiteStore(db *sqlx.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func (s *SQLiteStore) Create(ctx context.Context, title string, opts CreateOpts) (*Task, error) {
	return Create(s.db, title, opts)
}

func (s *SQLiteStore) Get(ctx context.Context, prefix string) (*Task, error) {
	return Get(s.db, prefix)
}

func (s *SQLiteStore) List(ctx context.Context, opts ListOpts) ([]Task, error) {
	return List(s.db, opts)
}

func (s *SQLiteStore) Recent(ctx context.Context, limit int) ([]Task, error) {
	return Recent(s.db, limit)
}

func (s *SQLiteStore) Update(ctx context.Context, prefix string, opts UpdateOpts) (*Task, error) {
	return Update(s.db, prefix, opts)
}

func (s *SQLiteStore) Save(ctx context.Context, t *Task) error {
	return Save(s.db, t)
}

func (s *SQLiteStore) Search(ctx context.Context, query string) ([]Task, error) {
	return Search(s.db, query)
}

func (s *SQLiteStore) Next(ctx context.Context, handle string) (*Task, error) {
	return Next(s.db, handle)
}

func (s *SQLiteStore) AddComment(ctx context.Context, taskPrefix, body string, author *string) (*Comment, error) {
	return AddComment(s.db, taskPrefix, body, author)
}

func (s *SQLiteStore) GetComments(ctx context.Context, taskID string) ([]Comment, error) {
	return GetComments(s.db, taskID)
}

func (s *SQLiteStore) AddRelation(ctx context.Context, fromPrefix, relType, toPrefix string) error {
	return AddRelation(s.db, fromPrefix, relType, toPrefix)
}

func (s *SQLiteStore) RemoveRelation(ctx context.Context, fromPrefix, relType, toPrefix string) error {
	return RemoveRelation(s.db, fromPrefix, relType, toPrefix)
}

func (s *SQLiteStore) GetRelations(ctx context.Context, taskID string) ([]Relation, error) {
	return GetRelations(s.db, taskID)
}

func (s *SQLiteStore) UpsertExternalRef(ctx context.Context, taskID, source, externalID string, url *string) error {
	return UpsertExternalRef(s.db, taskID, source, externalID, url)
}

func (s *SQLiteStore) FindByExternalRef(ctx context.Context, source, externalID string) (*Task, error) {
	return FindByExternalRef(s.db, source, externalID)
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
