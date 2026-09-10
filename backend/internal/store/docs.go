package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"wikiatlas/backend/internal/domain"
)

func scanDoc(row interface{ Scan(...any) error }) (*domain.Doc, error) {
	var (
		d     domain.Doc
		links string
	)
	err := row.Scan(&d.ID, &d.FolderOf, &d.Title, &d.Slug, &d.ContentMd, &d.ContentVer, &links, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(links), &d.Links)
	if d.Links == nil {
		d.Links = []string{}
	}
	return &d, nil
}

const docCols = `id, folder_of, title, slug, content_md, content_ver, links_json, created_at, updated_at`

// CreateDoc inserts a materials document under a work.
func (s *Store) CreateDoc(workID string, body domain.CreateDocBody) (*domain.Doc, error) {
	if body.Title == "" {
		return nil, ErrValidation{Message: "title is required"}
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
	slug := slugify(body.Title)
	base := slug
	for i := 2; ; i++ {
		var c int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM docs WHERE slug = ?`, slug).Scan(&c); err != nil {
			return nil, err
		}
		if c == 0 {
			break
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
	content := ""
	if body.ContentMd != nil {
		content = *body.ContentMd
	}
	links := body.Links
	if links == nil {
		links = []string{}
	}
	if len(links) > 0 {
		if err := s.validateLinksAgainstFolder(workID, links); err != nil {
			return nil, err
		}
	}
	linksJSON, _ := json.Marshal(links)
	_, err := s.DB.Exec(`INSERT INTO docs (id, folder_of, title, slug, content_md, content_ver, links_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		id, workID, body.Title, slug, content, string(linksJSON), now, now)
	if err != nil {
		return nil, err
	}
	// if initial content, also write revision 1
	if content != "" {
		_, err = s.DB.Exec(`UPDATE docs SET content_ver = 1 WHERE id = ?`, id)
		if err != nil {
			return nil, err
		}
		revID := NewID()
		_, err = s.DB.Exec(`INSERT INTO revisions (id, target_type, target_id, version, content_md, author, run_id, summary, created_at)
			VALUES (?, 'doc', ?, 1, ?, 'human', NULL, 'initial', ?)`,
			revID, id, content, now)
		if err != nil {
			return nil, err
		}
	}
	return s.GetDoc(id)
}

// GetDoc returns one doc by id.
func (s *Store) GetDoc(id string) (*domain.Doc, error) {
	d, err := scanDoc(s.DB.QueryRow(`SELECT `+docCols+` FROM docs WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound{What: "doc"}
	}
	return d, err
}

// ListDocsByWork lists docs in a work folder.
func (s *Store) ListDocsByWork(workID string) ([]domain.Doc, error) {
	rows, err := s.DB.Query(`SELECT `+docCols+` FROM docs WHERE folder_of = ? ORDER BY title`, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Doc, 0)
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// PatchDoc updates title and/or links.
func (s *Store) PatchDoc(id string, body domain.PatchDocBody) (*domain.Doc, error) {
	if _, err := s.GetDoc(id); err != nil {
		return nil, err
	}
	now := Now()
	if body.Title != nil {
		if *body.Title == "" {
			return nil, ErrValidation{Message: "title cannot be empty"}
		}
		if _, err := s.DB.Exec(`UPDATE docs SET title = ?, updated_at = ? WHERE id = ?`, *body.Title, now, id); err != nil {
			return nil, err
		}
	}
	if body.Links != nil {
		if err := s.validateDocLinks(id, *body.Links); err != nil {
			return nil, err
		}
		b, _ := json.Marshal(*body.Links)
		if _, err := s.DB.Exec(`UPDATE docs SET links_json = ?, updated_at = ? WHERE id = ?`, string(b), now, id); err != nil {
			return nil, err
		}
	}
	return s.GetDoc(id)
}

// validateDocLinks 落实"资料夹本质上就是一个系列"这条规则：
//  1. 资料必须挂在**系列**节点下（资料夹 = 系列）
//  2. 关联目标必须是**该系列下的单作**（含嵌套系列的子孙），不能跳到别的系列去
func (s *Store) validateDocLinks(docID string, links []string) error {
	var folderID string
	err := s.DB.QueryRow(`SELECT folder_of FROM docs WHERE id = ?`, docID).Scan(&folderID)
	if err != nil {
		return err
	}
	return s.validateLinksAgainstFolder(folderID, links)
}

// validateLinksAgainstFolder：资料夹必须是系列，关联目标必须在该系列子树内。
func (s *Store) validateLinksAgainstFolder(folderID string, links []string) error {
	var kind string
	if err := s.DB.QueryRow(`SELECT kind FROM works WHERE id = ?`, folderID).Scan(&kind); err != nil {
		return err
	}
	if kind != string(domain.WorkKindSeries) {
		return ErrValidation{Message: "只有系列资料夹里的资料可以关联条目（资料夹 = 系列）"}
	}
	for _, target := range links {
		ok, err := s.isDescendantOf(target, folderID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrValidation{Message: "只能关联本系列下的单作，不能跨系列关联"}
		}
	}
	return nil
}

// isDescendantOf 判断 workID 是否在 ancestorID 的子树里（含直接子节点）。
func (s *Store) isDescendantOf(workID, ancestorID string) (bool, error) {
	cur := workID
	for i := 0; i < 64 && cur != ""; i++ {
		if cur == ancestorID {
			return true, nil
		}
		var parent sql.NullString
		err := s.DB.QueryRow(`SELECT parent_id FROM works WHERE id = ?`, cur).Scan(&parent)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		cur = ""
		if parent.Valid {
			cur = parent.String
		}
	}
	return false, nil
}

// DeleteDoc removes a doc.
func (s *Store) DeleteDoc(id string) error {
	if _, err := s.GetDoc(id); err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM revisions WHERE target_type = 'doc' AND target_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM docs WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// PutDocContent transactionally commits new doc content with optional version check.
func (s *Store) PutDocContent(id string, body domain.PutContentBody) (*domain.ContentCommitResult, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var contentVer int64
	err = tx.QueryRow(`SELECT content_ver FROM docs WHERE id = ?`, id).Scan(&contentVer)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound{What: "doc"}
	}
	if err != nil {
		return nil, err
	}
	if body.ExpectedVersion != nil && *body.ExpectedVersion != contentVer {
		return nil, ErrConflict{Message: fmt.Sprintf("version conflict: expected %d, current %d", *body.ExpectedVersion, contentVer)}
	}
	switch body.Author {
	case domain.AuthorHuman, domain.AuthorLLM, domain.AuthorImport:
	default:
		return nil, ErrValidation{Message: "author must be human|llm|import"}
	}
	now := Now()
	newVer := contentVer + 1
	summary := ""
	if body.Summary != nil {
		summary = *body.Summary
	}
	if _, err := tx.Exec(`UPDATE docs SET content_md = ?, content_ver = ?, updated_at = ? WHERE id = ?`,
		body.ContentMd, newVer, now, id); err != nil {
		return nil, err
	}
	revID := NewID()
	var runID any
	if body.RunID != nil && *body.RunID != "" {
		runID = *body.RunID
	}
	if _, err := tx.Exec(`INSERT INTO revisions (id, target_type, target_id, version, content_md, author, run_id, summary, created_at)
		VALUES (?, 'doc', ?, ?, ?, ?, ?, ?, ?)`,
		revID, id, newVer, body.ContentMd, string(body.Author), runID, summary, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &domain.ContentCommitResult{ID: id, ContentVer: newVer, RevisionID: revID}, nil
}
