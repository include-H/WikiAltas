package store

import (
	"database/sql"

	"wikiatlas/backend/internal/domain"
)

// CreateRelation inserts a typed edge between works.
func (s *Store) CreateRelation(body domain.CreateRelationBody) (*domain.Relation, error) {
	if body.FromID == "" || body.ToID == "" {
		return nil, ErrValidation{Message: "fromId and toId are required"}
	}
	if body.FromID == body.ToID {
		return nil, ErrValidation{Message: "cannot relate a work to itself"}
	}
	if !domain.ValidRelationTypes[body.Type] {
		return nil, ErrValidation{Message: "invalid relation type; see frozen dictionary"}
	}
	// both ends must exist
	for _, id := range []string{body.FromID, body.ToID} {
		var n int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM works WHERE id = ?`, id).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, ErrValidation{Message: "work not found: " + id}
		}
	}
	id := NewID()
	now := Now()
	_, err := s.DB.Exec(`INSERT INTO relations (id, from_id, to_id, type, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, body.FromID, body.ToID, string(body.Type), now)
	if err != nil {
		// unique violation
		return nil, ErrConflict{Message: "relation already exists"}
	}
	return s.GetRelation(id)
}

// GetRelation returns one relation.
func (s *Store) GetRelation(id string) (*domain.Relation, error) {
	var r domain.Relation
	err := s.DB.QueryRow(`SELECT id, from_id, to_id, type, created_at FROM relations WHERE id = ?`, id).
		Scan(&r.ID, &r.FromID, &r.ToID, &r.Type, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound{What: "relation"}
	}
	return &r, err
}

// ListRelationsByWork returns edges touching a work (either direction).
func (s *Store) ListRelationsByWork(workID string) ([]domain.Relation, error) {
	rows, err := s.DB.Query(`SELECT id, from_id, to_id, type, created_at FROM relations
		WHERE from_id = ? OR to_id = ? ORDER BY created_at`, workID, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Relation, 0)
	for rows.Next() {
		var r domain.Relation
		if err := rows.Scan(&r.ID, &r.FromID, &r.ToID, &r.Type, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRelation removes a relation edge.
func (s *Store) DeleteRelation(id string) error {
	res, err := s.DB.Exec(`DELETE FROM relations WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound{What: "relation"}
	}
	return nil
}

// CreateLibraryLink attaches an external library reference.
func (s *Store) CreateLibraryLink(workID string, source domain.LibrarySource, externalID string, url, titleHint *string) (*domain.LibraryLink, error) {
	switch source {
	case domain.LibraryEmby, domain.LibraryKomga, domain.LibraryGameAtlas:
	default:
		return nil, ErrValidation{Message: "source must be emby|komga|gameatlas"}
	}
	if externalID == "" {
		return nil, ErrValidation{Message: "externalId is required"}
	}
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM works WHERE id = ?`, workID).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrNotFound{What: "work"}
	}
	id := NewID()
	now := Now()
	_, err := s.DB.Exec(`INSERT INTO library_links (id, work_id, source, external_id, url, title_hint, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, workID, string(source), externalID, nullStr(url), nullStr(titleHint), now)
	if err != nil {
		return nil, ErrConflict{Message: "library link already exists for this source+externalId"}
	}
	return s.GetLibraryLink(id)
}

// GetLibraryLink returns one link.
func (s *Store) GetLibraryLink(id string) (*domain.LibraryLink, error) {
	var (
		l        domain.LibraryLink
		url, th  sql.NullString
	)
	err := s.DB.QueryRow(`SELECT id, work_id, source, external_id, url, title_hint, created_at FROM library_links WHERE id = ?`, id).
		Scan(&l.ID, &l.WorkID, &l.Source, &l.ExternalID, &url, &th, &l.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound{What: "library_link"}
	}
	if err != nil {
		return nil, err
	}
	if url.Valid {
		l.URL = &url.String
	}
	if th.Valid {
		l.TitleHint = &th.String
	}
	return &l, nil
}

// ListLibraryLinksByWork lists links for a work.
func (s *Store) ListLibraryLinksByWork(workID string) ([]domain.LibraryLink, error) {
	rows, err := s.DB.Query(`SELECT id, work_id, source, external_id, url, title_hint, created_at
		FROM library_links WHERE work_id = ? ORDER BY source, created_at`, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.LibraryLink, 0)
	for rows.Next() {
		var (
			l       domain.LibraryLink
			url, th sql.NullString
		)
		if err := rows.Scan(&l.ID, &l.WorkID, &l.Source, &l.ExternalID, &url, &th, &l.CreatedAt); err != nil {
			return nil, err
		}
		if url.Valid {
			l.URL = &url.String
		}
		if th.Valid {
			l.TitleHint = &th.String
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// DeleteLibraryLink removes a link.
func (s *Store) DeleteLibraryLink(id string) error {
	res, err := s.DB.Exec(`DELETE FROM library_links WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound{What: "library_link"}
	}
	return nil
}
