package store

import (
	"strings"

	"wikiatlas/backend/internal/domain"
)

// Search runs FTS5 (trigram) over works and docs, with LIKE fallback for short/odd queries.
// kind filters: "", "work", "doc".
func (s *Store) Search(q, kind string, limit int) ([]domain.SearchHit, error) {
	if limit <= 0 {
		limit = 20
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return []domain.SearchHit{}, nil
	}

	// strip FTS special chars from user input
	safe := strings.Map(func(r rune) rune {
		switch r {
		case '"', '\'', '*', '(', ')', '-', ':', '^', '{', '}', '[', ']', '~':
			return ' '
		default:
			return r
		}
	}, q)
	safe = strings.Join(strings.Fields(safe), " ")
	if safe == "" {
		return []domain.SearchHit{}, nil
	}

	// trigram requires >= 3 runes; shorter → LIKE
	runes := []rune(safe)
	if len(runes) >= 3 {
		if hits, err := s.searchFTS(safe, kind, limit); err == nil && len(hits) > 0 {
			return hits, nil
		}
	}
	return s.searchLIKE(safe, kind, limit)
}

func (s *Store) searchFTS(match, kind string, limit int) ([]domain.SearchHit, error) {
	out := make([]domain.SearchHit, 0)
	if kind == "" || kind == "work" {
		rows, err := s.DB.Query(`
			SELECT w.id, w.title, w.slug,
			       COALESCE(snippet(works_fts, -1, '«', '»', '…', 12), '')
			FROM works_fts
			JOIN works w ON w.rowid = works_fts.rowid
			WHERE works_fts MATCH ?
			ORDER BY rank
			LIMIT ?`, match, limit)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var h domain.SearchHit
			if err := rows.Scan(&h.ID, &h.Title, &h.Slug, &h.Snippet); err != nil {
				rows.Close()
				return nil, err
			}
			h.Kind = "work"
			out = append(out, h)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	if kind == "" || kind == "doc" {
		rows, err := s.DB.Query(`
			SELECT d.id, d.title, d.slug,
			       COALESCE(snippet(docs_fts, -1, '«', '»', '…', 12), '')
			FROM docs_fts
			JOIN docs d ON d.rowid = docs_fts.rowid
			WHERE docs_fts MATCH ?
			ORDER BY rank
			LIMIT ?`, match, limit)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var h domain.SearchHit
			if err := rows.Scan(&h.ID, &h.Title, &h.Slug, &h.Snippet); err != nil {
				rows.Close()
				return nil, err
			}
			h.Kind = "doc"
			out = append(out, h)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) searchLIKE(q, kind string, limit int) ([]domain.SearchHit, error) {
	like := "%" + q + "%"
	out := make([]domain.SearchHit, 0)
	if kind == "" || kind == "work" {
		rows, err := s.DB.Query(`
			SELECT id, title, slug,
			       CASE WHEN content_md IS NULL THEN '' ELSE substr(content_md, 1, 80) END
			FROM works
			WHERE title LIKE ? OR COALESCE(content_md,'') LIKE ?
			LIMIT ?`, like, like, limit)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var h domain.SearchHit
			if err := rows.Scan(&h.ID, &h.Title, &h.Slug, &h.Snippet); err != nil {
				rows.Close()
				return nil, err
			}
			h.Kind = "work"
			out = append(out, h)
		}
		rows.Close()
	}
	if kind == "" || kind == "doc" {
		rows, err := s.DB.Query(`
			SELECT id, title, slug, substr(content_md, 1, 80)
			FROM docs
			WHERE title LIKE ? OR content_md LIKE ?
			LIMIT ?`, like, like, limit)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var h domain.SearchHit
			if err := rows.Scan(&h.ID, &h.Title, &h.Slug, &h.Snippet); err != nil {
				rows.Close()
				return nil, err
			}
			h.Kind = "doc"
			out = append(out, h)
		}
		rows.Close()
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
