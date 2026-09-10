package store

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"wikiatlas/backend/internal/domain"
)

// 简易用户系统：单管理员账号。
// 密码用 PBKDF2-HMAC-SHA256（标准库实现，不引第三方依赖），会话密钥随机生成后存 settings。

const (
	secretAdminPassword = "admin_password_hash"
	secretSessionKey    = "session_secret"
	pbkdf2Iterations    = 120_000
	pbkdf2KeyLen        = 32
)

// AdminUsername 返回管理员用户名（默认 admin）。
func (s *Store) AdminUsername() string {
	if st, err := s.GetSettings(); err == nil && strings.TrimSpace(st.Admin.Username) != "" {
		return strings.TrimSpace(st.Admin.Username)
	}
	return "admin"
}

// AdminPasswordConfigured 是否已设置密码（未设置时所有人都是访客）。
func (s *Store) AdminPasswordConfigured() bool {
	return s.GetSecret(secretAdminPassword) != ""
}

// SetAdminPassword 设置/修改管理员密码（空字符串=清除）。
func (s *Store) SetAdminPassword(password string) error {
	if password == "" {
		return s.SetSecret(secretAdminPassword, "")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	sum := pbkdf2SHA256([]byte(password), salt, pbkdf2Iterations, pbkdf2KeyLen)
	encoded := fmt.Sprintf("pbkdf2$%d$%s$%s", pbkdf2Iterations, hex.EncodeToString(salt), hex.EncodeToString(sum))
	return s.SetSecret(secretAdminPassword, encoded)
}

// CheckAdminPassword 校验用户名+密码（恒定时间比较）。
func (s *Store) CheckAdminPassword(username, password string) bool {
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(username)), []byte(s.AdminUsername())) != 1 {
		return false
	}
	encoded := s.GetSecret(secretAdminPassword)
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err1 := hex.DecodeString(parts[2])
	want, err2 := hex.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// SessionSecret 返回（必要时生成）会话签名密钥。
func (s *Store) SessionSecret() string {
	if v := s.GetSecret(secretSessionKey); v != "" {
		return v
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	secret := hex.EncodeToString(buf)
	_ = s.SetSecret(secretSessionKey, secret)
	return secret
}

// SessionFingerprint 跟访问密码绑定：改密码后所有旧会话自动失效。
func (s *Store) SessionFingerprint() string {
	sum := sha256.Sum256([]byte(s.GetSecret(secretAdminPassword)))
	return hex.EncodeToString(sum[:8])
}

// IsPublicWork 便捷方法：节点对访客是否可见。
func (s *Store) IsPublicWork(id string) bool {
	ok, err := s.VisibleToGuests(id)
	return err == nil && ok
}

// NewNodeVisibility 读取设置页里的"新节点默认可见性"。
func (s *Store) NewNodeVisibility() domain.Visibility {
	if st, err := s.GetSettings(); err == nil && st.Admin.NewNodeVisibility != "" {
		return st.Admin.NewNodeVisibility
	}
	return domain.VisibilityPrivate
}

// pbkdf2SHA256 是 RFC 2898 的 PBKDF2，用 HMAC-SHA256 实现（标准库拼装）。
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	hashLen := sha256.Size
	blocks := (keyLen + hashLen - 1) / hashLen
	out := make([]byte, 0, blocks*hashLen)
	for block := 1; block <= blocks; block++ {
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			mac.Reset()
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}
