package store

import (
	"database/sql"

	"wikiatlas/backend/internal/domain"
)

// migrateVisibility 给 works 加 visibility 列（默认 private：发布是显式动作）。
func (s *Store) migrateVisibility() error {
	rows, err := s.DB.Query(`PRAGMA table_info(works)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	has := false
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == "visibility" {
			has = true
		}
	}
	if has {
		return nil
	}
	_, err = s.DB.Exec(`ALTER TABLE works ADD COLUMN visibility TEXT NOT NULL DEFAULT 'private'`)
	return err
}

// VisibleToGuests 判断节点对访客是否可见：自身与所有祖先都是 public。
// （库里读的是 works 表，因此资料夹也天然继承所属节点的可见性。）
func (s *Store) VisibleToGuests(id string) (bool, error) {
	cur := id
	for i := 0; i < 64 && cur != ""; i++ {
		var vis string
		var parent sql.NullString
		err := s.DB.QueryRow(`SELECT visibility, parent_id FROM works WHERE id = ?`, cur).Scan(&vis, &parent)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if vis != string(domain.VisibilityPublic) {
			return false, nil
		}
		cur = ""
		if parent.Valid {
			cur = parent.String
		}
	}
	return true, nil
}

// PublicWorkIDs 返回对访客可见的节点集合（用于树与检索过滤）。
func (s *Store) PublicWorkIDs() (map[string]bool, error) {
	rows, err := s.DB.Query(`SELECT id, parent_id, visibility FROM works`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parent := map[string]sql.NullString{}
	vis := map[string]string{}
	for rows.Next() {
		var id, v string
		var p sql.NullString
		if err := rows.Scan(&id, &p, &v); err != nil {
			return nil, err
		}
		parent[id] = p
		vis[id] = v
	}
	out := map[string]bool{}
	for id := range vis {
		cur, ok := id, true
		for i := 0; i < 64 && cur != ""; i++ {
			if vis[cur] != string(domain.VisibilityPublic) {
				ok = false
				break
			}
			p := parent[cur]
			cur = ""
			if p.Valid {
				cur = p.String
			}
		}
		if ok {
			out[id] = true
		}
	}
	return out, nil
}
