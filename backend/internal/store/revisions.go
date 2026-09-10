package store

import (
	"database/sql"

	"wikiatlas/backend/internal/domain"
)

// ListRevisions returns revisions for a target, newest first. includeContent controls contentMd.
func (s *Store) ListRevisions(targetType, targetID string, limit int, includeContent bool) ([]domain.Revision, error) {
	if limit <= 0 {
		limit = 20
	}
	cols := `id, target_type, target_id, version, author, run_id, summary, created_at`
	if includeContent {
		cols += `, content_md`
	}
	rows, err := s.DB.Query(`SELECT `+cols+` FROM revisions WHERE target_type = ? AND target_id = ? ORDER BY version DESC LIMIT ?`,
		targetType, targetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Revision, 0)
	for rows.Next() {
		var (
			r      domain.Revision
			runID  sql.NullString
		)
		if includeContent {
			if err := rows.Scan(&r.ID, &r.TargetType, &r.TargetID, &r.Version, &r.Author, &runID, &r.Summary, &r.CreatedAt, &r.ContentMd); err != nil {
				return nil, err
			}
		} else {
			if err := rows.Scan(&r.ID, &r.TargetType, &r.TargetID, &r.Version, &r.Author, &runID, &r.Summary, &r.CreatedAt); err != nil {
				return nil, err
			}
		}
		if runID.Valid {
			r.RunID = &runID.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRevision returns one revision.
func (s *Store) GetRevision(id string) (*domain.Revision, error) {
	var (
		r     domain.Revision
		runID sql.NullString
	)
	err := s.DB.QueryRow(`SELECT id, target_type, target_id, version, author, run_id, summary, created_at, content_md
		FROM revisions WHERE id = ?`, id).Scan(&r.ID, &r.TargetType, &r.TargetID, &r.Version, &r.Author, &runID, &r.Summary, &r.CreatedAt, &r.ContentMd)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound{What: "revision"}
	}
	if err != nil {
		return nil, err
	}
	if runID.Valid {
		r.RunID = &runID.String
	}
	return &r, nil
}

// RestoreRevision creates a new revision whose content equals the old one (rollback is a new version).
func (s *Store) RestoreRevision(targetType, targetID, revID string, author domain.Author) (*domain.ContentCommitResult, error) {
	rev, err := s.GetRevision(revID)
	if err != nil {
		return nil, err
	}
	if rev.TargetType != targetType || rev.TargetID != targetID {
		return nil, ErrValidation{Message: "revision does not belong to this target"}
	}
	summary := "restore from v" + itoa(rev.Version)
	body := domain.PutContentBody{
		ContentMd: rev.ContentMd,
		Author:    author,
		Summary:   &summary,
	}
	if targetType == "work" {
		return s.PutWorkContent(targetID, body)
	}
	return s.PutDocContent(targetID, body)
}

func itoa(n int64) string {
	// small helper to avoid strconv import noise in this file
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
