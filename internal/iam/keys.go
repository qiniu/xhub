package iam

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sunqirui1987/xhub/internal/util"
	"xorm.io/builder"
	"xorm.io/xorm"
)

// Key 是用户凭证；团队仅为读取时解析的用户当前归属，不独立绑定或分配团队。
type Key struct {
	ID        string   `xorm:"pk 'id'" json:"id"`
	TokenHash string   `xorm:"'token_hash'" json:"-"`
	KeyPrefix string   `xorm:"'key_prefix'" json:"key_prefix"`
	OwnerType string   `xorm:"'owner_type'" json:"owner_type"`
	UserID    *string  `xorm:"'user_id'" json:"user_id"`
	TeamID    string   `xorm:"-" json:"team_id"`
	ProjectID *string  `xorm:"'project_id'" json:"project_id"`
	CreatedBy *string  `xorm:"'created_by'" json:"created_by"`
	Name      string   `xorm:"'name'" json:"name"`
	Models    []string `xorm:"json 'models'" json:"models"`
	MaxBudget *float64 `xorm:"'max_budget'" json:"max_budget"`
	Spend     float64  `xorm:"'spend'" json:"spend"`
	TPMLimit  *int     `xorm:"'tpm_limit'" json:"tpm_limit"`
	RPMLimit  *int     `xorm:"'rpm_limit'" json:"rpm_limit"`
	Status    string   `xorm:"'status'" json:"status"`
	// RouteTemplateID is the named router settings this key selects. Empty means
	// it selects nothing and inherits from its team.
	RouteTemplateID *string    `xorm:"'route_template_id'" json:"route_template_id,omitempty"`
	ExpiresAt       *time.Time `xorm:"'expires_at'" json:"expires_at"`
	LastUsedAt      *time.Time `xorm:"'last_used_at'" json:"last_used_at"`
	CreatedAt       time.Time  `xorm:"created 'created_at'" json:"created_at"`
	UpdatedAt       time.Time  `xorm:"updated 'updated_at'" json:"-"`
}

// 告诉 xorm 这个结构体对应数据库表 api_keys。
// 参数：无。
// 返回 string（string）：xorm 使用的表名 api_keys。这个结构体的行都进这张表。
// 调用：xorm 在映射这张表时。
// 测试：无直接单测
func (APIKey) TableName() string { return "api_keys" }

// APIKey is aliased so the short name stays available in this package.
type APIKey = Key

// ActiveKey reports a key that may be used: not blocked, not revoked, not expired.
// 参数：无。
// 返回 bool（bool）：记录存在且状态为启用时为真。接收者为 nil 时为假。
// 调用：authz/decide.go
// 测试：无直接单测
func (k *Key) ActiveKey() bool {
	if k == nil || k.Status != StatusActive {
		return false
	}
	return k.ExpiresAt == nil || time.Now().Before(*k.ExpiresAt)
}

// OwnedBy reports whether the key belongs to this person.
// 参数 userID（string）：用户 id。空串表示没有指定用户。
// 返回 bool（bool）：这把密钥属于这个人时返回真。
// 调用：仅在 keys.go 内使用
// 测试：无直接单测
func (k *Key) OwnedBy(userID string) bool {
	return k != nil && k.OwnerType == OwnerPersonal && k.UserID != nil && *k.UserID == userID
}

// HashKey hashes a plaintext key for storage and lookup.
// 参数 plain（string）：明文口令或明文密钥。只在创建和轮换时出现，不会写入日志。
// 返回 string（string）：明文密钥的哈希。入库、查找和日志都用它，不明文落库。
// 调用：auth/auth.go、gateway/keys/admin.go
// 测试：无直接单测
func HashKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// NewPlainKey returns a new sk-... credential.
// 参数：无。
// 返回 string（string）：新的 sk- 明文密钥，只在创建或轮换时交给调用方。
// 调用：仅在 keys.go 内使用
// 测试：无直接单测
func NewPlainKey() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "sk-" + hex.EncodeToString(b[:])
}

// KeyInput 创建用户凭证；UserID 必填，TeamID 仅校验调用方提供的上下文，不持久化绑定。
type KeyInput struct {
	OwnerType string
	UserID    string
	TeamID    string
	ProjectID string
	Name      string
	Models    []string
	MaxBudget *float64
	TPMLimit  *int
	RPMLimit  *int
	ExpiresAt *time.Time
	// RouteTemplateID is a two-level optional: nil leaves the current selection
	// alone, and a non-nil pointer to nil clears it, which puts the key back on
	// inheriting from its team. The double pointer is what keeps "not sent" apart
	// from "sent as null".
	RouteTemplateID **string
}

// CreateKey 持久化用户密钥并返回一次性明文；从用户解析唯一团队，禁止无主或服务密钥。
// 参数 ctx（context.Context）：上下文，取消时停止；by（Actor）：执行这次修改的操作者。审计日志记这个人的 id 和角色；in（KeyInput）：创建或修改密钥的输入，含团队、名称、允许的模型和限额。
// 返回 *Key（*Key）：写入后的密钥行，含哈希、限额和归属。失败时为 nil；string（string）：只展示这一次的明文密钥。失败时为空串，库里只存哈希；error（error）：失败原因。nil 表示创建成功。
// 调用：gateway/keys/admin.go、gateway/keys/generate.go
// 测试：activity_http_test.go、authz_test.go、log_completeness_test.go
func (db *DB) CreateKey(ctx context.Context, by Actor, in KeyInput) (*Key, string, error) {
	plain := NewPlainKey()
	var out *Key
	err := db.tx(ctx, func(s *xorm.Session) error {
		if in.OwnerType != OwnerPersonal || in.UserID == "" {
			return ErrInvalid
		}
		owner, err := getUser(ctx, s, in.UserID)
		if err != nil {
			return err
		}
		if !owner.Active() {
			return ErrInactive
		}
		resolved, err := userTeam(s, in.UserID)
		if err != nil {
			return err
		}
		if in.TeamID != "" && in.TeamID != resolved {
			return ErrInvalid
		}
		in.TeamID = resolved
		if in.TeamID == "" {
			if in.OwnerType != OwnerPersonal || in.ProjectID != "" {
				return ErrInvalid
			}
		} else {
			if err := lockTeam(ctx, s, in.TeamID); err != nil {
				return err
			}
			team, err := getTeam(ctx, s, in.TeamID)
			if err != nil {
				return err
			}
			if team.Status != StatusActive {
				return ErrInactive
			}
		}
		if err := checkWithinTeam(s, in.TeamID, in.ProjectID, in.Models, in.MaxBudget); err != nil {
			return err
		}
		k := Key{ID: newID(), TokenHash: HashKey(plain), KeyPrefix: plain[:11], OwnerType: in.OwnerType,
			TeamID: in.TeamID, Name: in.Name, Models: nonNil(in.Models), MaxBudget: in.MaxBudget,
			TPMLimit: in.TPMLimit, RPMLimit: in.RPMLimit, Status: StatusActive, ExpiresAt: in.ExpiresAt}
		if in.UserID != "" {
			k.UserID = &in.UserID
		}
		if by.ID != "" {
			k.CreatedBy = &by.ID
		}
		if in.ProjectID != "" {
			k.ProjectID = &in.ProjectID
		}
		// A selection sent with the create is stored with it. Update honoured
		// this and create did not, so a key minted with a template silently got
		// none and routed by its team's settings instead.
		if in.RouteTemplateID != nil {
			k.RouteTemplateID = *in.RouteTemplateID
		}
		// TeamID 是派生字段；数据库只保存真正的用户归属，避免转移后两条身份链不一致。
		if _, err := s.Insert(&k); err != nil {
			return err
		}
		if err := validateQuota(s, "key", k.ID); err != nil {
			return err
		}
		out = &k
		return writeAudit(s, by, Audit{Action: "key.create", ObjectType: "key", ObjectID: k.ID, TeamID: in.TeamID,
			Detail: map[string]any{"owner_type": k.OwnerType}})
	})
	if err != nil {
		return nil, "", err
	}
	return out, plain, nil
}

// RotateKey 在同一事务中更新凭据、有效期与额度；可选 inputs 只能包含一份由网关合并已有字段的配置，失败时旧密钥保持有效。
// 参数 ctx（context.Context）：上下文，取消时停止；by（Actor）：执行这次修改的操作者。审计日志记这个人的 id 和角色；id（string）：密钥 id。空串表示没有指定密钥。
// 返回 *Key（*Key）：轮换后的密钥行。失败时为 nil；string（string）：新的明文密钥，只展示这一次；error（error）：失败原因。nil 表示轮换成功。
// 调用：gateway/keys/admin.go
// 测试：keys_rotation_test.go 与 cmd/regression/key_lifecycle_test.go
func (db *DB) RotateKey(ctx context.Context, by Actor, id string, inputs ...KeyInput) (*Key, string, error) {
	plain := NewPlainKey()
	var out *Key
	err := db.tx(ctx, func(s *xorm.Session) error {
		cur, err := getKey(ctx, s, id)
		if err != nil {
			return err
		}
		if len(inputs) > 1 {
			return ErrInvalid
		}
		patch := Key{TokenHash: HashKey(plain), KeyPrefix: plain[:11], Status: StatusActive}
		cols := []string{"token_hash", "key_prefix", "status"}
		if len(inputs) == 1 {
			in := inputs[0]
			projectID := ""
			if cur.ProjectID != nil {
				projectID = *cur.ProjectID
			}
			if err := checkWithinTeam(s, cur.TeamID, projectID, in.Models, in.MaxBudget); err != nil {
				return err
			}
			patch.Name, patch.Models, patch.MaxBudget = in.Name, nonNil(in.Models), in.MaxBudget
			patch.TPMLimit, patch.RPMLimit = in.TPMLimit, in.RPMLimit
			cols = append(cols, "name", "models", "max_budget", "tpm_limit", "rpm_limit")
			// 留空期限保留既有到期时间，显式正数期限才能恢复已过期凭据。
			if in.ExpiresAt != nil {
				patch.ExpiresAt = in.ExpiresAt
				cols = append(cols, "expires_at")
			}
		}
		if _, err := s.ID(id).Cols(cols...).Update(&patch); err != nil {
			return err
		}
		// 分配校验必须和更换哈希在同一事务内，避免失败更新先撤销旧密钥。
		if err := validateQuota(s, "key", id); err != nil {
			return err
		}
		if out, err = getKey(ctx, s, id); err != nil {
			return err
		}
		return writeAudit(s, by, Audit{Action: "key.rotate", ObjectType: "key", ObjectID: id, TeamID: cur.TeamID})
	})
	if err != nil {
		return nil, "", err
	}
	return out, plain, nil
}

// DeleteKey removes a key.
// 参数 ctx（context.Context）：上下文，取消时停止；by（Actor）：执行这次修改的操作者。审计日志记这个人的 id 和角色；id（string）：密钥 id。空串表示没有指定密钥。
// 返回 error（error）：失败原因，nil 表示这一步成功。
// 调用：gateway/keys/generate.go
// 测试：无直接单测
func (db *DB) DeleteKey(ctx context.Context, by Actor, id string) error {
	return db.tx(ctx, func(s *xorm.Session) error {
		cur, err := getKey(ctx, s, id)
		if err != nil {
			return err
		}
		if err := affected(s.ID(id).Delete(&Key{})); err != nil {
			return err
		}
		return writeAudit(s, by, Audit{Action: "key.delete", ObjectType: "key", ObjectID: id, TeamID: cur.TeamID})
	})
}

// SetKeyStatus blocks or unblocks a key.
// 参数 ctx（context.Context）：上下文，取消时停止；by（Actor）：执行这次修改的操作者。审计日志记这个人的 id 和角色；id（string）：密钥 id。空串表示没有指定密钥；status（string）：HTTP 状态码。
// 返回 *Key（*Key）：交给调用方的密钥行；error（error）：失败原因。nil 表示这一步成功。
// 调用：gateway/keys/generate.go
// 测试：authz_test.go
func (db *DB) SetKeyStatus(ctx context.Context, by Actor, id, status string) (*Key, error) {
	var out *Key
	err := db.tx(ctx, func(s *xorm.Session) error {
		cur, err := getKey(ctx, s, id)
		if err != nil {
			return err
		}
		if _, err := s.ID(id).Cols("status").Update(&Key{Status: status}); err != nil {
			return err
		}
		if out, err = getKey(ctx, s, id); err != nil {
			return err
		}
		return writeAudit(s, by, Audit{Action: "key.status", ObjectType: "key", ObjectID: id, TeamID: cur.TeamID,
			Detail: map[string]any{"status": status}})
	})
	return out, err
}

// UpdateKey changes the narrowing and limits of an existing key. Narrowing may never exceed what the team (and project) grants.
// 参数 ctx（context.Context）：上下文，取消时停止；by（Actor）：执行这次修改的操作者。审计日志记这个人的 id 和角色；id（string）：密钥 id。空串表示没有指定密钥；in（KeyInput）：创建或修改密钥的输入，含团队、名称、允许的模型和限额。
// 返回 *Key（*Key）：交给调用方的密钥行；error（error）：失败原因。nil 表示这一步成功。
// 调用：gateway/keys/admin.go、gateway/keys/generate.go
// 测试：无直接单测
func (db *DB) UpdateKey(ctx context.Context, by Actor, id string, in KeyInput) (*Key, error) {
	var out *Key
	err := db.tx(ctx, func(s *xorm.Session) error {
		cur, err := getKey(ctx, s, id)
		if err != nil {
			return err
		}
		projectID := ""
		if cur.ProjectID != nil {
			projectID = *cur.ProjectID
		}
		if err := checkWithinTeam(s, cur.TeamID, projectID, in.Models, in.MaxBudget); err != nil {
			return err
		}
		patch := Key{Name: in.Name, Models: nonNil(in.Models), MaxBudget: in.MaxBudget,
			TPMLimit: in.TPMLimit, RPMLimit: in.RPMLimit}
		cols := []string{"name", "models", "max_budget", "tpm_limit", "rpm_limit"}
		if in.ExpiresAt != nil {
			patch.ExpiresAt, cols = in.ExpiresAt, append(cols, "expires_at")
		}
		if in.RouteTemplateID != nil {
			patch.RouteTemplateID, cols = *in.RouteTemplateID, append(cols, "route_template_id")
		}
		if _, err := s.ID(id).Cols(cols...).Update(&patch); err != nil {
			return err
		}
		if out, err = getKey(ctx, s, id); err != nil {
			return err
		}
		if err := validateQuota(s, "key", id); err != nil {
			return err
		}
		return writeAudit(s, by, Audit{Action: "key.update", ObjectType: "key", ObjectID: id, TeamID: cur.TeamID})
	})
	return out, err
}

// GetKey loads one key with its narrowing.
// 参数 ctx（context.Context）：上下文，取消或超时时停止后续工作；id（string）：密钥 id。空串表示没有指定密钥。
// 返回 *Key（*Key）：交给调用方的密钥行；error（error）：失败原因。nil 表示这一步成功。
// 调用：authz/decide.go、gateway/keys/generate.go、gateway/limits.go
// 测试：无直接单测
func (db *DB) GetKey(ctx context.Context, id string) (*Key, error) {
	s := db.session(ctx)
	defer s.Close()
	return getKey(ctx, s, id)
}

// KeyByHash loads a key by credential hash for authentication.
// 参数 ctx（context.Context）：上下文，取消或超时时停止后续工作；hash（string）：密钥哈希，用来对齐热花费和日志，不是明文。
// 返回 *Key（*Key）：按哈希查出的密钥行。没有匹配时为 nil；error（error）：失败原因。nil 表示这一步成功。
// 调用：auth/auth.go、gateway/keys/generate.go
// 测试：无直接单测
func (db *DB) KeyByHash(ctx context.Context, hash string) (*Key, error) {
	s := db.session(ctx)
	defer s.Close()
	var k Key
	ok, err := s.Where("token_hash = ?", hash).Get(&k)
	if err != nil {
		return nil, mapErr(err)
	}
	if !ok {
		return nil, ErrNotFound
	}
	if err := resolveKeyTeam(s, &k); err != nil {
		return nil, err
	}
	return &k, nil
}

// KeyFilter 供管理接口向 ListKeys 传递授权范围与资源筛选；各字段取交集，无写入副作用。
// 字符串为空表示不附加该条件；TeamIDs/KeyIDs 为 nil 表示不限制，非 nil 空切片表示无权限匹配任何记录。
// 两种空值不能混淆，否则没有授权团队的成员可能看到整个表。
type KeyFilter struct {
	OrganizationID string // 密钥所属用户当前团队的组织；空串不附加组织条件，不替代权限范围。
	TeamID         string
	ProjectID      string
	UserID         string
	OwnerType      string
	TeamIDs        []string
	KeyIDs         []string
}

// ListKeys 查询权限范围与资源条件的交集，按创建时间倒序返回；组织取所属用户当前团队，组织管理员身份不代表 Key 归属。
// 参数 ctx（context.Context）：上下文，取消时停止；f（KeyFilter）：列出密钥使用的KeyFilter。
// 返回 []Key（[]Key）：过滤条件内的密钥，最新的在前。条件对不上任何行时为空切片；error（error）：查询失败。nil 表示成功。
// 调用：gateway/keys/admin.go、gateway/keys/generate.go
// 测试：key_organization_filter_test.go；查询失败返回错误，不能伪装成空列表。
func (db *DB) ListKeys(ctx context.Context, f KeyFilter) ([]Key, error) {
	s := db.session(ctx)
	defer s.Close()
	switch {
	case f.TeamIDs != nil:
		if len(f.TeamIDs) == 0 {
			return nil, nil
		}
		s = s.Where(builder.In("user_id", builder.Select("user_id").From("team_members").Where(builder.In("team_id", f.TeamIDs))))
	}
	if f.KeyIDs != nil {
		if len(f.KeyIDs) == 0 {
			return nil, nil
		}
		s = s.In("id", f.KeyIDs)
	}
	if f.TeamID != "" {
		s = s.Where("user_id IN (SELECT user_id FROM team_members WHERE team_id = ?)", f.TeamID)
	}
	if f.OrganizationID != "" {
		// 附加 AND 条件，保留原有授权范围；使用当前团队，避免转团队后仍匹配旧组织。
		s = s.Where("user_id IN (SELECT m.user_id FROM team_members m JOIN teams t ON t.id = m.team_id WHERE t.organization_id = ?)", f.OrganizationID)
	}
	if f.ProjectID != "" {
		s = s.Where("project_id = ?", f.ProjectID)
	}
	if f.UserID != "" {
		s = s.Where("user_id = ?", f.UserID)
	}
	if f.OwnerType != "" {
		s = s.Where("owner_type = ?", f.OwnerType)
	}
	var rows []Key
	if err := s.Desc("created_at").Find(&rows); err != nil {
		return nil, mapErr(err)
	}
	if len(rows) == 0 {
		return rows, nil
	}
	for i := range rows {
		if err := resolveKeyTeam(s, &rows[i]); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

// TouchKey records the last use of a key.
// 参数 ctx（context.Context）：上下文，取消或超时时停止后续工作；id（string）：密钥 id。空串表示没有指定密钥。
// 返回 error（error）：失败原因，nil 表示这一步成功。
// 调用：仅在 keys.go 内使用
// 测试：无直接单测
func (db *DB) TouchKey(ctx context.Context, id string) error {
	_, err := db.Engine.Context(ctx).ID(id).Cols("last_used_at").Update(&Key{LastUsedAt: timePtr(time.Now())})
	return err
}

// 得到指向这个时间的指针，方便写进可选的时间列。
// 参数 t（time.Time）：时间点。零值表示调用方没有提供时间。
// 返回 *time.Time（*time.Time）：解析出的时间。
// 调用：仅在 keys.go 内使用
// 测试：无直接单测
func timePtr(t time.Time) *time.Time { return &t }

// RevokeTeamKeys ends every key of a team; used when a team is blocked.
// 参数 ctx（context.Context）：上下文，取消时停止；by（Actor）：执行这次修改的操作者。审计日志记这个人的 id 和角色；teamID（string）：团队 id。空串表示没有指定团队。
// 返回 error（error）：失败原因，nil 表示这一步成功。
// 调用：仅在 keys.go 内使用
// 测试：无直接单测
func (db *DB) RevokeTeamKeys(ctx context.Context, by Actor, teamID string) error {
	return db.tx(ctx, func(s *xorm.Session) error {
		if err := lockTeam(ctx, s, teamID); err != nil {
			return err
		}
		n, err := s.Where("user_id IN (SELECT user_id FROM team_members WHERE team_id = ?) AND status <> ?", teamID, StatusRevoked).
			Cols("status").Update(&Key{Status: StatusRevoked})
		if err != nil {
			return err
		}
		util.Info("iam revoked %d key(s) of team %s", n, teamID)
		return writeAudit(s, by, Audit{Action: "key.revoke_team", ObjectType: "team", ObjectID: teamID, TeamID: teamID})
	})
}

// 按 id 从当前会话读取密钥行。
// 参数 ctx（context.Context）：上下文，取消或超时时停止后续工作；s（*xorm.Session）：当前事务里的数据库会话。调用方负责提交，这里不关闭它；id（string）：密钥 id。空串表示没有指定密钥。
// 返回 *Key（*Key）：按 id 查出的密钥行。没有这条记录时为 nil；error（error）：失败原因。nil 表示这一步成功。
// 调用：仅在 keys.go 内使用
// 测试：无直接单测
func getKey(ctx context.Context, s *xorm.Session, id string) (*Key, error) {
	var k Key
	if err := get(s.Where("id = ?", id), &k); err != nil {
		return nil, err
	}
	if err := resolveKeyTeam(s, &k); err != nil {
		return nil, err
	}
	return &k, nil
}

// userTeam 查询用户唯一团队；s 为当前会话、userID 为属主，返回团队或归属错误。
// 密钥读取/创建调用；无团队返回空，异常多团队必须拒绝，不猜测计费来源，无写入副作用。
func userTeam(s *xorm.Session, userID string) (string, error) {
	var members []TeamMembership
	if err := s.Where("user_id = ?", userID).Find(&members); err != nil {
		return "", err
	}
	if len(members) > 1 {
		return "", ErrMultipleTeams
	}
	if len(members) == 0 {
		return "", nil
	}
	return members[0].TeamID, nil
}

// resolveKeyTeam 补齐密钥的派生团队；s 为会话、k 为密钥，返回归属错误并更新内存字段。
// 查询和模型授权调用；无属主必须失败，永远不采用创建者作为计费对象，不写数据库。
func resolveKeyTeam(s *xorm.Session, k *Key) error {
	if k.UserID == nil || *k.UserID == "" || k.OwnerType != OwnerPersonal {
		return ErrInvalid
	}
	team, err := userTeam(s, *k.UserID)
	k.TeamID = team
	return err
}

// ---------- model resolution ----------

// allowedModels 按组织、团队、项目、密钥逐层取模型交集，供模型发现与推理共用，组织收窄后已有凭证立即受限。
// 参数 s 为当前会话，teamID/projectID/keyID 为单一归属链；返回授权集合或查询错误，无写入副作用。
// nil 表示全链未限制，非 nil 空集合表示交集无可用模型；不得将两者混同，否则会把拒绝错误转换为放行。
func allowedModels(s *xorm.Session, teamID, projectID, keyID string) (map[string]struct{}, error) {
	orgID := ""
	if teamID != "" {
		var team Team
		if err := get(s.Where("id = ?", teamID), &team); err != nil {
			return nil, err
		}
		orgID = team.OrganizationID
	}
	set, err := scopeModels(s, "organizations", orgID)
	if err != nil {
		return nil, err
	}
	for _, scope := range []struct{ table, id string }{
		{"teams", teamID},
		{"projects", projectID},
		{"api_keys", keyID},
	} {
		if scope.id == "" {
			continue
		}
		narrow, err := scopeModels(s, scope.table, scope.id)
		if err != nil {
			return nil, err
		}
		// A level with no list of its own inherits the ceiling above it.
		if narrow == nil {
			continue
		}
		// The first level that does restrict becomes the ceiling; anything
		// above it was unrestricted, so there is nothing to intersect with.
		if set == nil {
			set = narrow
			continue
		}
		set = intersectModels(set, narrow)
	}
	// nil means the whole chain placed no restriction, which is a real answer
	// and not the same as "may reach nothing". Callers rely on the difference,
	// so it is passed through rather than flattened into an empty set.
	return set, nil
}

// scopeModels reads one row's model list. A nil result means the row does not restrict: either it does not exist or its list is empty.
// 参数 s（*xorm.Session）：当前事务里的数据库会话。调用方负责提交，这里不关闭它；table（string）：范围模型使用的表。空串表示调用方没有提供这项；id（string）：部署、模型或凭据 id。空串表示没有选定。
// 返回 map[string]值（map[string]值）：范围模型的字段表。缺键表示上游或库里没有这个字段；error（error）：失败原因。nil 表示这一步成功。
// 调用：仅在 keys.go 内使用
// 测试：无直接单测
func scopeModels(s *xorm.Session, table, id string) (map[string]struct{}, error) {
	if id == "" {
		return nil, nil
	}
	var row modelsRow
	ok, err := s.Table(table).Cols("models").Where("id = ?", id).Get(&row)
	if err != nil {
		return nil, mapErr(err)
	}
	if !ok || len(row.Models) == 0 {
		return nil, nil
	}
	out := make(map[string]struct{}, len(row.Models))
	for _, m := range row.Models {
		if m != "" {
			out[m] = struct{}{}
		}
	}
	return out, nil
}

// intersectModels returns the models present in both sets.
// 参数 a（map[string]值）：intersect模型使用的map[string]值；b（map[string]值）：intersect模型使用的map[string]值。
// 返回 map[string]值（map[string]值）：intersect模型的字段表。缺键表示上游或库里没有这个字段。
// 调用：仅在 keys.go 内使用
// 测试：无直接单测
func intersectModels(a, b map[string]struct{}) map[string]struct{} {
	out := map[string]struct{}{}
	for m := range a {
		if _, ok := b[m]; ok {
			out[m] = struct{}{}
		}
	}
	return out
}

// modelsRow decodes a TEXT column holding a JSON array. Projecting the column
// straight into []string yields the raw document as a single element.
type modelsRow struct {
	Models []string `xorm:"json 'models'"`
}

// AllowedModelsForTeam 为密钥创建前的团队目录返回组织与团队模型交集；只读，nil 表示整条归属链未限制。
// 参数 ctx（context.Context）：上下文，取消或超时时停止后续工作；teamID（string）：团队 id。空串表示没有指定团队。
// 返回 []string（[]string）：nil 表示不限制，空非 nil 表示没有可用模型；error（error）：失败原因。nil 表示这一步成功。
// 调用：gateway/identity/handlers.go、gateway/models/access.go
// 测试：无直接单测
func (db *DB) AllowedModelsForTeam(ctx context.Context, teamID string) ([]string, error) {
	s := db.session(ctx)
	defer s.Close()
	set, err := allowedModels(s, teamID, "", "")
	if err != nil {
		return nil, err
	}
	return sortedKeys(set), nil
}

// AllowedModelsForKey is the key's effective model set; the catalog and the inference path both call it so a listed model is always a usable model.
// 参数 ctx（context.Context）：上下文，取消时停止；k（*Key）：Allowed模型为密钥使用的密钥行。
// 返回 []string（[]string）：Allowed模型为密钥。没有匹配时为 nil 或空切片，调用方按长度判断；error（error）：失败原因。nil 表示这一步成功。
// 调用：gateway/models/access.go
// 测试：无直接单测
func (db *DB) AllowedModelsForKey(ctx context.Context, k *Key) ([]string, error) {
	s := db.session(ctx)
	defer s.Close()
	// 缓存中的团队提示不能决定模型权限，实时读取用户唯一团队。
	if k == nil || k.UserID == nil {
		return nil, ErrInvalid
	}
	teamID, err := userTeam(s, *k.UserID)
	if err != nil {
		return nil, err
	}
	projectID := ""
	if k.ProjectID != nil {
		projectID = *k.ProjectID
	}
	set, err := allowedModels(s, teamID, projectID, k.ID)
	if err != nil {
		return nil, err
	}
	return sortedKeys(set), nil
}

// budgetCeiling 计算团队、项目和密钥中最紧的额度；个人密钥无团队时跳过团队层，仍检查明确指定的父级。
// 参数 s（*xorm.Session）：当前事务里的数据库会话。调用方负责提交，这里不关闭它；teamID（string）：团队 id。空串表示没有指定团队；projectID（string）：项目 id。空串表示不按项目过滤；keyID（string）：密钥 id。空串表示没有指定密钥。
// 返回 *float64（*float64）：团队、项目和密钥里最紧的预算上限。某一层没有上限时不参与比较。三层都没有时为 nil；error（error）：团队、项目或密钥查不到。nil 表示比较完成。
// 调用：checkWithinTeam、iam/teams.go；只读查询，不修改额度。
// 测试：keys_personal_test.go、gateway/limits_personal_test.go、regression/teamless_keys_test.go。
func budgetCeiling(s *xorm.Session, teamID, projectID, keyID string) (*float64, error) {
	var ceiling *float64
	if teamID != "" {
		var team Team
		if err := get(s.Where("id = ?", teamID), &team); err != nil {
			return nil, err
		}
		ceiling = team.MaxBudget
	}
	if projectID != "" {
		var p Project
		if err := get(s.Where("id = ?", projectID), &p); err != nil {
			return nil, err
		}
		if p.MaxBudget != nil && (ceiling == nil || *p.MaxBudget < *ceiling) {
			ceiling = p.MaxBudget
		}
	}
	if keyID != "" {
		var k Key
		if err := get(s.Where("id = ?", keyID), &k); err != nil {
			return nil, err
		}
		if k.MaxBudget != nil && (ceiling == nil || *k.MaxBudget < *ceiling) {
			ceiling = k.MaxBudget
		}
	}
	return ceiling, nil
}

// sortedKeys renders a model set as a sorted list. A nil set stays nil, because nil means "unrestricted" and an empty slice means "may reach nothing"; those are different answers and the callers branchon them.
// 参数 set（map[string]值）：sorted密钥使用的map[string]值。
// 返回 []string（[]string）：sorted密钥。没有匹配时为 nil 或空切片，调用方按长度判断。
// 调用：仅在 keys.go 内使用
// 测试：无直接单测
func sortedKeys(set map[string]struct{}) []string {
	if set == nil {
		return nil
	}
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sortStrings(out)
	return out
}

// 把字符串切片按字典序原地排序。
// 参数 s（[]string）：文本列表。空切片表示没有可处理的项。
// 返回：无。切片已按字典序原地排好。
// 调用：仅在 keys.go 内使用
// 测试：无直接单测
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ParseExpiry 将正整数秒、分、小时或天转换为从当前时刻起算的绝对过期时间。
// 参数 d 为期限字符串；空串表示永不过期。返回 UTC 时间或输入错误；非正数及会使 time.Duration 溢出的值会被拒绝。
// 创建与更新密钥接口调用本函数；函数只读取当前时间，不修改持久化数据。
func ParseExpiry(d string) (*time.Time, error) {
	d = strings.TrimSpace(d)
	if d == "" {
		return nil, nil
	}
	if len(d) < 2 {
		return nil, fmt.Errorf("invalid duration %q", d)
	}
	n, err := strconv.ParseInt(d[:len(d)-1], 10, 64)
	if err != nil || n <= 0 {
		return nil, fmt.Errorf("invalid duration %q", d)
	}
	var unit time.Duration
	switch d[len(d)-1] {
	case 's':
		unit = time.Second
	case 'm':
		unit = time.Minute
	case 'h':
		unit = time.Hour
	case 'd':
		unit = 24 * time.Hour
	default:
		return nil, fmt.Errorf("invalid duration %q", d)
	}
	// 先除后乘，避免整数乘法回绕后把很长的未来期限变成已过期时间。
	if n > int64((time.Duration(1<<63-1))/unit) {
		return nil, fmt.Errorf("duration %q is too large", d)
	}
	at := time.Now().Add(time.Duration(n) * unit)
	at = at.UTC()
	return &at, nil
}

// ResetKeySpend sets a key's spend back to zero, or to the given value. The usage events behind the old figure stay, so history is never rewritten.
// 参数 ctx（context.Context）：上下文，取消时停止；by（Actor）：执行这次修改的操作者。审计日志记这个人的 id 和角色；id（string）：密钥 id。空串表示没有指定密钥；to（float64）：Reset密钥花费使用的小数。0 表示没有费用或尚未计价。
// 返回 *Key（*Key）：交给调用方的密钥行；error（error）：失败原因。nil 表示这一步成功。
// 调用：gateway/keys/admin.go
// 测试：无直接单测
func (db *DB) ResetKeySpend(ctx context.Context, by Actor, id string, to float64) (*Key, error) {
	var out *Key
	err := db.tx(ctx, func(s *xorm.Session) error {
		cur, err := getKey(ctx, s, id)
		if err != nil {
			return err
		}
		if _, err := s.ID(id).Cols("spend").Update(&Key{Spend: to}); err != nil {
			return err
		}
		if out, err = getKey(ctx, s, id); err != nil {
			return err
		}
		if err := validateQuota(s, "key", id); err != nil {
			return err
		}
		return writeAudit(s, by, Audit{Action: "key.reset_spend", ObjectType: "key", ObjectID: id, TeamID: cur.TeamID,
			Detail: map[string]any{"to": to}})
	})
	return out, err
}
