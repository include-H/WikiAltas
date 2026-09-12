package store

import (
	"database/sql"
	"encoding/json"
	"strings"

	"wikiatlas/backend/internal/domain"
)

// GetSettings loads settings from the settings key-value table, merging defaults.
func (s *Store) GetSettings() (*domain.Settings, error) {
	cfg := domain.DefaultSettings()
	var raw sql.NullString
	err := s.DB.QueryRow(`SELECT value FROM settings WHERE key = 'app'`).Scan(&raw)
	if err == sql.ErrNoRows {
		return &cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if raw.Valid && raw.String != "" {
		// 存下来的 JSON 覆盖到默认值上（递归合并）
		if err := mergeSettings(&cfg, raw.String); err != nil {
			return nil, err
		}
	}
	// API key presence from env is not reflected here; handler may override.
	return &cfg, nil
}

// SaveSettings persists the full settings object.
func (s *Store) SaveSettings(st *domain.Settings) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`INSERT INTO settings (key, value) VALUES ('app', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(b))
	return err
}

// mergeSettings 把存下来的 JSON 覆盖到默认值上（递归按 key 合并）。
//
// 通用实现，不是逐字段手写——手写的那个版本已经漏过三个字段：
// ContextWindow（设置页填了窗口，刷新又变回默认）和整个 Admin 段
// （管理员标识改了名、刷新又变回 admin；新节点可见性同理）。
// 漏字段这种 bug 不该靠人记得，所以这里改成结构无关的合并，
// 再加一个"每个字段都存得回来"的round-trip 测试兜底。
//
// 语义：**key 缺席 = 用默认值；key 存在（哪怕是零值）= 用户的值**。
// 带 omitempty 的零值字段本来就不会落盘，所以不会出现"零值盖掉默认"的意外。
func mergeSettings(dst *domain.Settings, storedJSON string) error {
	if strings.TrimSpace(storedJSON) == "" {
		return nil
	}
	base, err := json.Marshal(dst)
	if err != nil {
		return err
	}
	var baseMap, storedMap map[string]any
	if err := json.Unmarshal(base, &baseMap); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(storedJSON), &storedMap); err != nil {
		return err
	}
	mergeMap(baseMap, storedMap)
	merged, err := json.Marshal(baseMap)
	if err != nil {
		return err
	}
	return json.Unmarshal(merged, dst)
}

// mergeMap 把 src 的键值递归并进 dst；src 里显式为 null 的键不动 dst。
func mergeMap(dst, src map[string]any) {
	for k, v := range src {
		if v == nil {
			continue
		}
		if sv, ok := v.(map[string]any); ok {
			if dv, ok := dst[k].(map[string]any); ok {
				mergeMap(dv, sv)
				continue
			}
		}
		dst[k] = v
	}
}

// HasAPIKey reports whether an API key is stored (never returns the key).
func (s *Store) HasAPIKey() bool {
	var raw sql.NullString
	err := s.DB.QueryRow(`SELECT value FROM settings WHERE key = 'llm_api_key'`).Scan(&raw)
	if err != nil || !raw.Valid {
		return false
	}
	return raw.String != ""
}

// SetAPIKey stores the LLM API key (dev convenience; production should use a secret store).
func (s *Store) SetAPIKey(key string) error {
	if key == "" {
		_, err := s.DB.Exec(`DELETE FROM settings WHERE key = 'llm_api_key'`)
		return err
	}
	_, err := s.DB.Exec(`INSERT INTO settings (key, value) VALUES ('llm_api_key', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key)
	return err
}

// GetAPIKey returns the stored key or empty.
func (s *Store) GetAPIKey() string {
	var raw sql.NullString
	_ = s.DB.QueryRow(`SELECT value FROM settings WHERE key = 'llm_api_key'`).Scan(&raw)
	if raw.Valid {
		return raw.String
	}
	return ""
}
