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
	// 环检查：方向性关系（改编/续作/衍生/重制/扩充）合起来必须是 DAG。
	// adapter §3 一直写着"不允许环"，但代码从没查过——结果是 A→B 与 B→A 可以
	// 同时存在，读取侧看到两句互相矛盾的话（"A 是 B 的续作" / "B 是 A 的续作"）。
	// `same_series` / `references` 天然互相，不参与（见 domain.DirectionalRelationTypes）。
	if domain.DirectionalRelationTypes[body.Type] {
		reaches, err := s.relationReaches(body.ToID, body.FromID)
		if err != nil {
			return nil, err
		}
		if reaches {
			return nil, ErrValidation{Message: "这条边会形成环：目标节点已经（沿方向性关系）指回起点，两边都留着的话读取侧会看到互相矛盾的两个方向。反过来的那条边若存在，删掉它再建这条。"}
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
		l       domain.LibraryLink
		url, th sql.NullString
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

// ListLibraryLinksBySource lists all links from one source (e.g. gameatlas)。
// 建档建议用它排除"已经挂过链"的条目。
func (s *Store) ListLibraryLinksBySource(source domain.LibrarySource) ([]domain.LibraryLink, error) {
	rows, err := s.DB.Query(`SELECT id, work_id, source, external_id, url, title_hint, created_at
		FROM library_links WHERE source = ? ORDER BY created_at`, string(source))
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

// DeleteLibraryLink removes one link by id.
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

// relationReaches 判断有向图上 from 能否走到 target——**只走方向性关系**。
// 建边前用它查环：新边 from→to 若让 to 能绕回 from，就成环。
func (s *Store) relationReaches(from, target string) (bool, error) {
	seen := map[string]bool{}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == target {
			return true, nil
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		rows, err := s.DB.Query(`SELECT to_id, type FROM relations WHERE from_id = ?`, cur)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var next, typ string
			if err := rows.Scan(&next, &typ); err != nil {
				rows.Close()
				return false, err
			}
			// 对称关系不算"走得到"：same_series/references 各自成对出现是正常的
			if domain.DirectionalRelationTypes[domain.RelationType(typ)] && !seen[next] {
				queue = append(queue, next)
			}
		}
		rows.Close()
	}
	return false, nil
}
