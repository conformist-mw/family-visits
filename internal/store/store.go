package store

import (
	"database/sql"

	"visits/internal/model"
)

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store { return &Store{db: db} }

// Create inserts one appointment and returns it with its assigned id.
func (s *Store) Create(a model.Appointment) (model.Appointment, error) {
	res, err := s.db.Exec(`
		INSERT INTO appointments (title, person, location, starts_at, ends_at, status, note, raw)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Title, a.Person, a.Location, a.StartsAt, nullIfEmpty(a.EndsAt),
		orDefault(a.Status, model.StatusPlanned), a.Note, a.Raw)
	if err != nil {
		return model.Appointment{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return model.Appointment{}, err
	}
	return s.Get(id)
}

// CreateMany inserts a batch in one transaction (a parsed message may hold
// several visits) and returns the stored rows in input order.
func (s *Store) CreateMany(items []model.Appointment) ([]model.Appointment, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO appointments (title, person, location, starts_at, ends_at, status, note, raw)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	ids := make([]int64, 0, len(items))
	for _, a := range items {
		res, err := stmt.Exec(a.Title, a.Person, a.Location, a.StartsAt, nullIfEmpty(a.EndsAt),
			orDefault(a.Status, model.StatusPlanned), a.Note, a.Raw)
		if err != nil {
			return nil, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	out := make([]model.Appointment, 0, len(ids))
	for _, id := range ids {
		a, err := s.Get(id)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (s *Store) Get(id int64) (model.Appointment, error) {
	row := s.db.QueryRow(selectCols+` WHERE id = ?`, id)
	return scanOne(row)
}

// Upcoming returns non-cancelled, non-deleted appointments starting at or
// after `from` (LocalDatetime), soonest first.
func (s *Store) Upcoming(from string, limit int) ([]model.Appointment, error) {
	return s.query(selectCols+`
		WHERE deleted_at IS NULL AND status != ? AND starts_at >= ?
		ORDER BY starts_at ASC
		LIMIT ?`, model.StatusCancelled, from, limit)
}

// Between returns non-cancelled, non-deleted appointments in [from, to)
// (LocalDatetime), soonest first — used for the today/week digests.
func (s *Store) Between(from, to string) ([]model.Appointment, error) {
	return s.query(selectCols+`
		WHERE deleted_at IS NULL AND status != ? AND starts_at >= ? AND starts_at < ?
		ORDER BY starts_at ASC`, model.StatusCancelled, from, to)
}

// SetStatus updates status and bumps updated_at.
func (s *Store) SetStatus(id int64, status string) error {
	_, err := s.db.Exec(`
		UPDATE appointments SET status = ?, updated_at = `+nowExpr+`
		WHERE id = ?`, status, id)
	return err
}

// Reschedule moves an appointment to a new start (LocalDatetime) and bumps
// updated_at; the ICS feed reflects it on HA's next poll.
func (s *Store) Reschedule(id int64, newStart string) error {
	_, err := s.db.Exec(`
		UPDATE appointments SET starts_at = ?, updated_at = `+nowExpr+`
		WHERE id = ?`, newStart, id)
	return err
}

// ActiveFrom returns non-cancelled, non-deleted appointments starting at or
// after `from` (LocalDatetime), soonest first — the source for the ICS feed.
func (s *Store) ActiveFrom(from string) ([]model.Appointment, error) {
	return s.query(selectCols+`
		WHERE deleted_at IS NULL AND status != ? AND starts_at >= ?
		ORDER BY starts_at ASC`, model.StatusCancelled, from)
}

const nowExpr = `strftime('%Y-%m-%dT%H:%M:%S','now','localtime')`

const selectCols = `
	SELECT id, title, person, location, starts_at,
	       COALESCE(ends_at,''), status, note, raw,
	       COALESCE(ha_uid,''), COALESCE(ha_synced_at,''),
	       created_at, updated_at, COALESCE(deleted_at,'')
	FROM appointments`

func (s *Store) query(q string, args ...any) ([]model.Appointment, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Appointment
	for rows.Next() {
		a, err := scanOne(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanOne(sc scanner) (model.Appointment, error) {
	var a model.Appointment
	err := sc.Scan(&a.ID, &a.Title, &a.Person, &a.Location, &a.StartsAt,
		&a.EndsAt, &a.Status, &a.Note, &a.Raw,
		&a.HaUID, &a.HaSyncedAt, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt)
	return a, err
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
