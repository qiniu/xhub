package iam

import (
	"context"
	"encoding/json"
	"strings"

	"xorm.io/xorm"
)

// OrgConfig 表示组织创建时的固定预算周期、模型上限及 JSON 元数据；由后台校验并在同一事务中保存。
type OrgConfig struct {
	BudgetDuration string
	Models         []string
	Metadata       map[string]any
}

// NormalizeOrganizationModels 校验组织模型范围并返回去重副本，供创建、更新和 HTTP 输入校验调用。
// 参数 models 为公开模型名；返回非 nil 列表或 ErrInvalid；空列表及单独 all-proxy-models 表示不限制。
// 特殊选项不可与具体模型混用，空白名称拒绝；不修改输入，不查询部署目录，允许先授权后添加模型。
func NormalizeOrganizationModels(models []string) ([]string, error) {
	for _, model := range models {
		if model == "" || strings.TrimSpace(model) != model {
			return nil, ErrInvalid
		}
		if model == "all-proxy-models" || model == "no-default-models" {
			if len(models) != 1 {
				return nil, ErrInvalid
			}
			if model == "all-proxy-models" {
				return []string{}, nil
			}
		}
	}
	return nonNil(models), nil
}

// normalizeOrganizationMetadata 校验并深拷贝组织 JSON 对象，供数据库创建及更新调用。
// 参数 metadata 为调用方对象，nil 表示清空；返回独立非 nil 对象或 ErrInvalid，不支持非 JSON 值及非有限数。
// JSON 往返避免结果或调用方继续修改嵌套映射影响彼此；不写入数据，不改变用户输入的语言。
func normalizeOrganizationMetadata(metadata map[string]any) (map[string]any, error) {
	if metadata == nil {
		return map[string]any{}, nil
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return nil, ErrInvalid
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil, ErrInvalid
	}
	return out, nil
}

// checkOrganizationModels 验证团队显式模型列表未超出组织范围，供团队创建、模型修改和转移事务调用。
// 参数 s 为调用方事务、orgID 为目标组织、models 为团队模型；返回查询错误或 ErrInvalid，不写数据。
// 空团队列表继承上级，既有团队在组织收窄后由运行期交集约束；无组织范围时保持原有可用模型。
func checkOrganizationModels(s *xorm.Session, orgID string, models []string) error {
	allowed, err := scopeModels(s, "organizations", orgID)
	if err != nil {
		return err
	}
	if allowed != nil {
		for _, model := range models {
			if _, ok := allowed[model]; !ok {
				return ErrInvalid
			}
		}
	}
	return nil
}

// AllowedModelsForUser 返回账号当前唯一团队及组织的模型交集，供控制台模型发现调用。
// 参数 ctx/userID 为查询上下文及账号 ID；返回 nil 表示未限制，空非 nil 表示拒绝全部，错误必须拒绝。
// 每次查询成员关系，避免会话缓存或传入其他团队绕过组织上限；只读，无持久化副作用。
func (db *DB) AllowedModelsForUser(ctx context.Context, userID string) ([]string, error) {
	s := db.session(ctx)
	defer s.Close()
	teamID, err := userTeam(s, userID)
	if err != nil {
		return nil, err
	}
	models, err := allowedModels(s, teamID, "", "")
	if err != nil {
		return nil, err
	}
	return sortedKeys(models), nil
}
