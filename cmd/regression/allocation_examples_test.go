package regression

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestQuotaThousandAllocationHTTP 验证三个维度的 1000=300+300+400 真实管理契约及数据面。
// 参数 t 为测试上下文；前置独立四层账户和本地供应商，验证超配回滚、共享/零、调大、删除释放和 Key 子分配；harness 清理 schema。
func TestQuotaThousandAllocationHTTP(t *testing.T) {
	for _, dimension := range []string{"max_budget", "rpm_limit", "tpm_limit"} {
		t.Run(dimension, func(t *testing.T) {
			h := newHarness(t, chatDeployment("example-model"))
			admin := h.adminSession()
			tn := h.provision(t, admin, "thousand")
			h.ok(http.MethodPatch, "/organization/update", admin, map[string]any{"organization_id": tn.orgID, dimension: 1000})
			h.ok(http.MethodPost, "/team/update", admin, map[string]any{"team_id": tn.teamID, dimension: 1000})
			h.ok(http.MethodPost, "/user/update", admin, map[string]any{"user_id": tn.userID, dimension: 300})
			// 每个名字都代表相同的普通用户；nil 是共享容量而不是另一种账号。
			create := func(name string, limit any) reply {
				return h.do(http.MethodPost, "/user/new", admin, map[string]any{"user_email": name + "@local.invalid", "password": "password123", "team_id": tn.teamID, dimension: limit})
			}
			bob := create("bob", 300)
			if bob.status != 200 {
				t.Fatal(bob.describe())
			}
			shared := create("shared", nil)
			if shared.status != 200 {
				t.Fatal(shared.describe())
			}
			zero := create("zero", 0)
			if zero.status != 200 {
				t.Fatal(zero.describe())
			}
			tooMuch := create("last", 401)
			if tooMuch.status != 400 || !strings.Contains(tooMuch.describe(), "allocation_exceeded") {
				t.Fatalf("401超配应返回400: %s", tooMuch.describe())
			}
			// 使用同一邮箱重试成功同时证明失败没有留下半个用户。
			last := create("last", 400)
			if last.status != 200 {
				t.Fatalf("400恰好分满且前次写入应回滚: %s", last.describe())
			}
			sharedKey := h.ok(http.MethodPost, "/key/generate", admin, map[string]any{"user_id": firstString(shared.json(), "user_id")}).json()
			zeroKey := h.ok(http.MethodPost, "/key/generate", admin, map[string]any{"user_id": firstString(zero.json(), "user_id")}).json()
			body := chatRequest("example-model", "ok")
			for _, key := range []map[string]any{sharedKey, zeroKey} {
				if key == nil {
					continue
				}
				if dimension != "max_budget" && firstString(key, "key") == firstString(sharedKey, "key") {
					h.ok(http.MethodPost, "/v1/chat/completions", firstString(key, "key"), body)
					continue
				}
				mark := len(h.upstreamCalls())
				blocked := h.do(http.MethodPost, "/v1/chat/completions", firstString(key, "key"), body)
				wantStatus := 429
				if dimension == "max_budget" {
					wantStatus = 403
				}
				if blocked.status != wantStatus || len(h.upstreamSince(mark)) != 0 {
					t.Fatalf("分满后的共享用户及零用户必须拦截: %s", blocked.describe())
				}
			}
			h.ok(http.MethodPost, "/v1/chat/completions", tn.key, body)
			// 给有用量的 Alice 调大1仍超配，读回必须保持300；删除400成员后可调大到700。
			bad := h.do(http.MethodPost, "/user/update", admin, map[string]any{"user_id": tn.userID, dimension: 301})
			if bad.status != 400 {
				t.Fatalf("调大1超配应失败: %s", bad.describe())
			}
			info := h.ok(http.MethodGet, "/user/info?user_id="+tn.userID, admin, nil).json()["user_info"].(map[string]any)
			if fmt.Sprint(info[dimension]) != "300" {
				t.Fatalf("失败更新必须保留300: %v", info)
			}
			h.ok(http.MethodPost, "/user/delete", admin, map[string]any{"user_id": firstString(last.json(), "user_id")})
			h.ok(http.MethodPost, "/v1/chat/completions", firstString(sharedKey, "key"), body)
			// 金额共享用户已花费少量金额，因此先释放 Bob 的固定300，避免把已消费当作可退还分配。
			h.ok(http.MethodPost, "/user/update", admin, map[string]any{"user_id": firstString(bob.json(), "user_id"), dimension: nil})
			h.ok(http.MethodPost, "/user/update", admin, map[string]any{"user_id": tn.userID, dimension: 700})
			// Key 金额是独立上限，可以重叠；速率仍使用699+2验证超配。
			for i, amount := range []int{100, 599, 2} {
				key := h.do(http.MethodPost, "/key/generate", tn.session, map[string]any{"key_alias": fmt.Sprint("part-", i), dimension: amount})
				if (i < 2 || dimension == "max_budget") && key.status != 200 {
					t.Fatalf("合法Key分配: %s", key.describe())
				}
				if i == 2 && dimension != "max_budget" && key.status != 400 {
					t.Fatalf("Key分配合计超过个人700: %s", key.describe())
				}
			}
		})
	}
}

// TestPersonalKeyDerivedProjectHTTP 验证无团队参数的项目 Key、越界失败及已删除业务 Key 路由。
// 参数 t 为测试上下文；前置两个独立组织/团队，覆盖本人和管理员，正确 Key 真实推理；harness 清理所有测试数据。
func TestPersonalKeyDerivedProjectHTTP(t *testing.T) {
	h := newHarness(t, chatDeployment("project-example"))
	admin := h.adminSession()
	a := h.openScope(t, admin, "project-a")
	b := h.openScope(t, admin, "project-b")
	for _, token := range []string{admin, a.session} {
		key := h.ok(http.MethodPost, "/key/generate", token, map[string]any{"user_id": a.userID, "project_id": a.projectID}).json()
		if firstString(key, "team_id") != a.teamID {
			t.Fatalf("团队应从用户派生: %v", key)
		}
		h.ok(http.MethodPost, "/v1/chat/completions", firstString(key, "key"), chatRequest("project-example", "ok"))
		bad := h.do(http.MethodPost, "/key/generate", token, map[string]any{"user_id": a.userID, "project_id": b.projectID})
		if bad.status < 400 {
			t.Fatalf("跨团队项目不应创建: %s", bad.describe())
		}
	}
	if old := h.do(http.MethodPost, "/key/service-account/generate", admin, map[string]any{}); old.status != 404 {
		t.Fatalf("业务Key路由必须不存在: %s", old.describe())
	}
}
