package httpapi

import "testing"

// 访问密码是"软门槛"：连续失败 5 次锁 5 分钟（对齐 GameManager 的策略）。
func TestLoginLockoutPolicy(t *testing.T) {
	key := "test-lockout-key"
	clearLoginFails(key)

	allowed, remaining, retry := checkLoginAllowed(key)
	if !allowed || remaining != maxLoginFails || retry != 0 {
		t.Fatalf("初始状态 = (%v,%d,%d)", allowed, remaining, retry)
	}
	for i := 1; i < maxLoginFails; i++ {
		remaining, lockedFor := noteLoginFail(key)
		if lockedFor != 0 {
			t.Fatalf("第 %d 次失败不应锁定", i)
		}
		if remaining != maxLoginFails-i {
			t.Fatalf("第 %d 次失败剩余次数 = %d", i, remaining)
		}
	}
	remaining, lockedFor := noteLoginFail(key)
	if lockedFor <= 0 || remaining != 0 {
		t.Fatalf("第 %d 次失败应锁定: remaining=%d lockedFor=%d", maxLoginFails, remaining, lockedFor)
	}
	allowed, _, retry = checkLoginAllowed(key)
	if allowed || retry <= 0 {
		t.Fatalf("锁定期内应拒绝: allowed=%v retry=%d", allowed, retry)
	}
	clearLoginFails(key)
	if allowed, _, _ = checkLoginAllowed(key); !allowed {
		t.Fatal("清除后应恢复可用")
	}
}
