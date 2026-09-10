package store

import (
	"testing"

	"wikiatlas/backend/internal/domain"
)

func TestAdminPasswordRoundTrip(t *testing.T) {
	s, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if !s.AdminPasswordConfigured() || !s.UsingDefaultPassword() {
		t.Fatal("新库应带出厂密码 1234")
	}
	if !s.CheckAdminPassword("admin", DefaultAdminPassword) {
		t.Fatal("出厂密码应可登录")
	}
	if err := s.SetAdminPassword("s3cret-pw"); err != nil {
		t.Fatal(err)
	}
	if !s.AdminPasswordConfigured() {
		t.Fatal("设置后应显示已配置")
	}
	if !s.CheckAdminPassword("admin", "s3cret-pw") {
		t.Fatal("正确密码应通过")
	}
	if s.CheckAdminPassword("admin", "wrong") {
		t.Fatal("错误密码不应通过")
	}
	if s.CheckAdminPassword("someone", "s3cret-pw") {
		t.Fatal("错误用户名不应通过")
	}
	if secret := s.SessionSecret(); secret == "" {
		t.Fatal("会话密钥应可生成")
	}
	if a, b := s.SessionSecret(), s.SessionSecret(); a != b {
		t.Fatal("会话密钥应稳定")
	}
}

func TestVisibilityInheritance(t *testing.T) {
	s, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	universe, _ := s.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindUniverse, Title: "公开宇宙"})
	pub, _ := s.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindSeries, Title: "公开系列", ParentID: &universe.ID})
	child, _ := s.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindWork, Title: "公开单作", ParentID: &pub.ID})

	vis := domain.VisibilityPublic
	if _, err := s.PatchWork(universe.ID, domain.PatchWorkBody{Visibility: &vis}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchWork(pub.ID, domain.PatchWorkBody{Visibility: &vis}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchWork(child.ID, domain.PatchWorkBody{Visibility: &vis}); err != nil {
		t.Fatal(err)
	}
	if !s.IsPublicWork(child.ID) {
		t.Fatal("祖先全公开时子节点应可见")
	}
	priv := domain.VisibilityPrivate
	if _, err := s.PatchWork(pub.ID, domain.PatchWorkBody{Visibility: &priv}); err != nil {
		t.Fatal(err)
	}
	if s.IsPublicWork(child.ID) {
		t.Fatal("父节点转私有后，子节点应对访客不可见")
	}
	ids, err := s.PublicWorkIDs()
	if err != nil {
		t.Fatal(err)
	}
	if ids[child.ID] || ids[pub.ID] {
		t.Fatal("私有子树不应出现在公开集合里")
	}
}
