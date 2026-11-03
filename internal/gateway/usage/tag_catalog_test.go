package usage

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/store"
)

// tagHost 只替代鉴权和存储取得动作；不使用其他宿主方法，验证拒绝发生在访问配置库之前。
type tagHost struct {
	Host
	denied bool
	reads  int
}

// RequireManage 按测试前置决定403或管理员；不改变生产权限判断，无外部依赖。
func (h *tagHost) RequireManage(w http.ResponseWriter, r *http.Request) *auth.Principal {
	if h.denied {
		w.WriteHeader(403)
		return nil
	}
	return &auth.Principal{UserID: "admin"}
}

// RecordStore 计数后返回缺库；验证TagList缺失依赖不能伪装空列表，不连接数据库。
func (h *tagHost) RecordStore() *store.Store { h.reads++; return nil }

// TestTagListGuardAndUnavailable 检查拒绝先于读取及缺库503；请求与响应全部内存创建，无需清理。
func TestTagListGuardAndUnavailable(t *testing.T) {
	for _, denied := range []bool{true, false} {
		h := &tagHost{denied: denied}
		w := httptest.NewRecorder()
		TagList(h, w, httptest.NewRequest("GET", "/tag/list", nil))
		want, reads := 503, 1
		if denied {
			want, reads = 403, 0
		}
		if w.Code != want || h.reads != reads {
			t.Fatalf("标签鉴权/缺库结果错误: %d reads=%d", w.Code, h.reads)
		}
	}
}
