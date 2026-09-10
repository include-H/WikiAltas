package store

import (
	"net/url"
	"strings"
)

// SetSecret 写一个密钥类配置（llm_api_key / exa_api_key）。空值表示删除。
func (s *Store) SetSecret(key, value string) error {
	if strings.TrimSpace(value) == "" {
		_, err := s.DB.Exec(`DELETE FROM settings WHERE key = ?`, key)
		return err
	}
	_, err := s.DB.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// GetSecret 读密钥类配置（空表示未配置）。
func (s *Store) GetSecret(key string) string {
	var raw string
	if err := s.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&raw); err != nil {
		return ""
	}
	return raw
}

// ExaAPIKey 返回 Exa key（只来自设置页，存 settings 表）。
func (s *Store) ExaAPIKey() string {
	return s.GetSecret("exa_api_key")
}

// MaskURL 只保留 host，用于日志/展示（避免把密钥拼进日志）。
func MaskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}
