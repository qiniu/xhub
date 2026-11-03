package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/util"
)

const scimUserSchema = "urn:ietf:params:scim:schemas:core:2.0:User"

// scimUser 是第一阶段公开目录用户资源；本地权限字段不接受外部目录覆盖。
type scimUser struct {
	Schemas     []string `json:"schemas"`
	ID          string   `json:"id,omitempty"`
	ExternalID  string   `json:"externalId"`
	UserName    string   `json:"userName"`
	DisplayName string   `json:"displayName"`
	Active      *bool    `json:"active"`
}

// scimWrite 写标准 SCIM JSON；参数为响应、状态及内容，无返回，供全部目录端点使用。
func scimWrite(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(code)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

// scimError 将服务错误转换为标准格式；不暴露存储错误或身份秘密，无返回值。
// 参数 typ/detail 是固定协议错误文本，供目录认证、过滤与资源错误使用。
func scimError(w http.ResponseWriter, code int, typ, detail string) {
	value := map[string]any{"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:Error"}, "status": strconv.Itoa(code), "detail": detail}
	if typ != "" {
		value["scimType"] = typ
	}
	scimWrite(w, code, value)
}

// directoryModule 注册连接管理和用户同步端点；参数为网关，返回模块，由启动过程挂载。
// 提供 User/Group 资源和原子 PATCH；不支持的协议能力在发现接口显式声明。
func directoryModule(s *Server) util.Module {
	return util.Bind("directory", func(reg util.Registrar) {
		reg.Handle("GET /admin/archive/kodo", s.archiveAdmin)
		reg.Handle("PUT /admin/archive/kodo", s.archiveAdmin)
		reg.Handle("POST /admin/archive/kodo/test", s.archiveAdmin)
		reg.Handle("POST /admin/archive/kodo/retry", s.archiveAdmin)
		reg.Handle("GET /admin/enterprise/sso", s.enterpriseOIDCAdmin)
		reg.Handle("GET /admin/enterprise/sso/{connection_id}/refresh", s.enterpriseSAMLRefreshAdmin)
		reg.Handle("PUT /admin/enterprise/sso/{connection_id}/refresh", s.enterpriseSAMLRefreshAdmin)
		reg.Handle("POST /admin/enterprise/sso/{connection_id}/refresh", s.enterpriseSAMLRefreshAdmin)
		reg.Handle("POST /admin/enterprise/sso", s.enterpriseOIDCAdmin)
		reg.Handle("GET /admin/enterprise/sso/{connection_id}", s.enterpriseOIDCAdmin)
		reg.Handle("PUT /admin/enterprise/sso/{connection_id}", s.enterpriseOIDCAdmin)
		reg.Handle("DELETE /admin/enterprise/sso/{connection_id}", s.enterpriseOIDCAdmin)
		reg.Handle("GET /auth/enterprise/options", s.enterpriseOIDCOptions)
		reg.Handle("GET /auth/enterprise/{connection_id}/start", s.enterpriseOIDCStart)
		reg.Handle("GET /auth/enterprise/callback", s.enterpriseOIDCCallback)
		reg.Handle("GET /admin/enterprise/sso/{connection_id}/metadata", s.enterpriseSAMLMetadata)
		reg.Handle("GET /auth/enterprise/{connection_id}/saml/metadata", s.enterpriseSAMLMetadata)
		reg.Handle("POST /auth/enterprise/saml/acs", s.enterpriseSAMLACS)
		reg.Handle("GET /auth/enterprise/saml/finish", s.enterpriseSAMLFinish)
		reg.Handle("GET /admin/directory/connections", s.directoryAdmin)
		reg.Handle("POST /admin/directory/connections", s.directoryAdmin)
		reg.Handle("PUT /admin/directory/connections/{connection_id}", s.directoryAdmin)
		reg.Handle("DELETE /admin/directory/connections/{connection_id}", s.directoryAdmin)
		reg.Handle("GET /admin/directory/connections/{connection_id}", s.directoryDetails)
		reg.Handle("PUT /admin/directory/mappings", s.directoryMapping)
		reg.Handle("PUT /admin/directory/users/{user_id}/assignment", s.directoryAssignment)
		base := "/scim/v2/connections/{connection_id}"
		reg.Handle("GET "+base+"/ServiceProviderConfig", s.scimUsers)
		reg.Handle("GET "+base+"/Schemas", s.scimDiscovery)
		reg.Handle("GET "+base+"/Schemas/{id}", s.scimDiscovery)
		reg.Handle("GET "+base+"/ResourceTypes", s.scimDiscovery)
		reg.Handle("GET "+base+"/ResourceTypes/{id}", s.scimDiscovery)
		reg.Handle("PATCH "+base+"/Users/{id}", s.scimPatch)
		reg.Handle("PATCH "+base+"/Groups/{id}", s.scimPatch)
		reg.Handle("GET "+base+"/Users", s.scimUsers)
		reg.Handle("POST "+base+"/Users", s.scimUsers)
		reg.Handle("GET "+base+"/Users/{id}", s.scimUsers)
		reg.Handle("PUT "+base+"/Users/{id}", s.scimUsers)
		reg.Handle("DELETE "+base+"/Users/{id}", s.scimUsers)
		reg.Handle("GET "+base+"/Groups", s.scimGroups)
		reg.Handle("POST "+base+"/Groups", s.scimGroups)
		reg.Handle("GET "+base+"/Groups/{id}", s.scimGroups)
		reg.Handle("PUT "+base+"/Groups/{id}", s.scimGroups)
		reg.Handle("DELETE "+base+"/Groups/{id}", s.scimGroups)
	})
}

// directoryDetails 返回连接管理快照；参数为 HTTP 请求响应，仅已授权平台管理员可访问。
// 返回组、映射及用户分配来源，不返回凭据，无存储副作用；失败沿用 IAM 错误契约。
func (s *Server) directoryDetails(w http.ResponseWriter, r *http.Request) {
	if s.RequireManage(w, r) == nil {
		return
	}
	out, err := s.Identity().DirectoryManagementSnapshot(r.Context(), r.PathValue("connection_id"))
	if err != nil {
		s.WriteIAMError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	util.WriteJSON(w, 200, out)
}

// directoryDecode 读取单个有大小上限的 JSON；参数为请求和接收结构，返回是否合法。
// 用于管理和 SCIM 写操作；拒绝尾随 JSON，不写数据库。
func directoryDecode(r *http.Request, target any) bool {
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if d.Decode(target) != nil {
		return false
	}
	return d.Decode(new(any)) == io.EOF
}

// directoryAdmin 管理连接；平台管理员会话验证后才读取或写入，返回 HTTP JSON。
// 参数为响应和请求；创建仅展示一次 token，更新不得改变绑定组织；删除撤销连接但保留本地业务数据，成功返回204。
func (s *Server) directoryAdmin(w http.ResponseWriter, r *http.Request) {
	p := s.RequireManage(w, r)
	if p == nil {
		return
	}
	db := s.Identity()
	by := iam.Actor{ID: p.UserID, Kind: "session"}
	if r.Method == http.MethodDelete {
		if err := db.DeleteDirectoryConnection(r.Context(), by, r.PathValue("connection_id")); err != nil {
			s.WriteIAMError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodGet {
		rows, err := db.DirectoryConnections(r.Context())
		if err != nil {
			s.WriteIAMError(w, r, err)
			return
		}
		util.WriteJSON(w, 200, rows)
		return
	}
	var in struct {
		OrganizationID string `json:"organization_id"`
		Name           string `json:"name"`
		Enabled        bool   `json:"enabled"`
		DefaultTeamID  string `json:"default_team_id"`
	}
	if !directoryDecode(r, &in) {
		util.WriteError(w, 400, "invalid_request", "Invalid JSON")
		return
	}
	if r.Method == http.MethodPost {
		c, token, err := db.CreateDirectoryConnection(r.Context(), by, in.OrganizationID, in.Name)
		if err != nil {
			s.WriteIAMError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		util.WriteJSON(w, 201, map[string]any{"connection": c, "token": token})
		return
	}
	if err := db.ConfigureDirectory(r.Context(), by, r.PathValue("connection_id"), in.Enabled, in.DefaultTeamID); err != nil {
		s.WriteIAMError(w, r, err)
		return
	}
	util.WriteJSON(w, 200, map[string]any{"ok": true})
}

// directoryMapping 保存目录组映射；仅平台管理员会话可操作，同组织校验由事务层完成。
// 参数是 HTTP 请求与响应；返回标准管理结果，失败不改变映射或成员归属。
func (s *Server) directoryMapping(w http.ResponseWriter, r *http.Request) {
	p := s.RequireManage(w, r)
	if p == nil {
		return
	}
	var in iam.DirectoryMapping
	if !directoryDecode(r, &in) {
		util.WriteError(w, 400, "invalid_request", "Invalid JSON")
		return
	}
	if err := s.Identity().SetDirectoryMapping(r.Context(), iam.Actor{ID: p.UserID, Kind: "session"}, in); err != nil {
		s.WriteIAMError(w, r, err)
		return
	}
	util.WriteJSON(w, 200, map[string]any{"ok": true})
}

// directoryAssignment 设置人工团队或恢复自动；参数为请求与响应，无函数返回。
// 仅平台管理员会话可调用；指定用户范围重算，数据库失败时所有副作用回滚。
func (s *Server) directoryAssignment(w http.ResponseWriter, r *http.Request) {
	p := s.RequireManage(w, r)
	if p == nil {
		return
	}
	var in struct {
		Mode   string `json:"mode"`
		TeamID string `json:"team_id"`
	}
	if !directoryDecode(r, &in) {
		util.WriteError(w, 400, "invalid_request", "Invalid JSON")
		return
	}
	if err := s.Identity().OverrideDirectoryTeam(r.Context(), iam.Actor{ID: p.UserID, Kind: "session"}, r.PathValue("user_id"), in.Mode, in.TeamID); err != nil {
		s.WriteIAMError(w, r, err)
		return
	}
	util.WriteJSON(w, 200, map[string]any{"ok": true})
}

// scimPaging 解析受限分页与精确过滤；参数为请求，返回起点、数量、字段、值和错误。
// 供列表使用，不支持的表达式显式拒绝，不将过滤拼入 SQL。
func scimPaging(r *http.Request) (int, int, string, string, error) {
	start, count := 1, 100
	var err error
	q := r.URL.Query()
	if q.Has("startIndex") {
		start, err = strconv.Atoi(q.Get("startIndex"))
		if err != nil || start < 1 {
			return 0, 0, "", "", iam.ErrInvalid
		}
	}
	if q.Has("count") {
		count, err = strconv.Atoi(q.Get("count"))
		if err != nil || count < 0 || count > 200 {
			return 0, 0, "", "", iam.ErrInvalid
		}
	}
	field, value := "", ""
	if filter := q.Get("filter"); filter != "" {
		parts := strings.SplitN(strings.TrimSpace(filter), " eq ", 2)
		if len(parts) != 2 || (parts[0] != "userName" && parts[0] != "externalId") || json.Unmarshal([]byte(parts[1]), &value) != nil {
			return 0, 0, "", "", iam.ErrInvalid
		}
		field = parts[0]
	}
	if q.Has("sortBy") || q.Has("sortOrder") {
		return 0, 0, "", "", iam.ErrInvalid
	}
	return start, count, field, value, nil
}

// scimPublic 转换无秘密的用户快照；参数为数据库资源，返回标准 SCIM User，无副作用。
func scimPublic(in iam.DirectoryUserResource) scimUser {
	active := in.Active
	return scimUser{Schemas: []string{scimUserSchema}, ID: in.ID, ExternalID: in.ExternalID, UserName: in.Email, DisplayName: in.Name, Active: &active}
}

// scimStoreError 返回固定存储错误映射；参数为响应和错误，无返回，不泄漏 SQL。
// 供资源读写使用，区分冲突、不存在、关系非法和服务器故障。
func scimStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, iam.ErrConflict):
		scimError(w, 409, "uniqueness", "Resource conflicts with an existing identity")
	case errors.Is(err, iam.ErrNotFound):
		scimError(w, 404, "", "Resource not found")
	case errors.Is(err, iam.ErrInvalid), errors.Is(err, iam.ErrInactive):
		scimError(w, 400, "invalidValue", "Invalid resource")
	default:
		scimError(w, 500, "", "Directory operation failed")
	}
}

// scimUsers 处理连接认证后的用户列表、创建、读取、替换和逻辑停用。
// 参数为 HTTP 请求/响应，无返回；外部字段不授予角色，跨连接读取等价于不存在。
func (s *Server) scimUsers(w http.ResponseWriter, r *http.Request) {
	db := s.Identity()
	connectionID := r.PathValue("connection_id")
	auth := strings.Fields(r.Header.Get("Authorization"))
	if len(auth) != 2 || !strings.EqualFold(auth[0], "Bearer") {
		scimError(w, 401, "", "Invalid directory credential")
		return
	}
	if _, err := db.AuthenticateDirectory(r.Context(), connectionID, auth[1]); err != nil {
		scimError(w, 401, "", "Invalid directory credential")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/ServiceProviderConfig") {
		scimWrite(w, 200, map[string]any{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"}, "patch": map[string]any{"supported": true}, "bulk": map[string]any{"supported": false, "maxOperations": 0, "maxPayloadSize": 0}, "filter": map[string]any{"supported": true, "maxResults": 200}, "changePassword": map[string]any{"supported": false}, "sort": map[string]any{"supported": false}, "etag": map[string]any{"supported": false}, "authenticationSchemes": []map[string]any{{"type": "oauthbearertoken", "name": "Bearer token", "description": "Connection-scoped bearer token", "primary": true}}})
		return
	}
	id := r.PathValue("id")
	if r.Method == http.MethodGet && id == "" {
		start, count, field, value, err := scimPaging(r)
		if err != nil {
			scimError(w, 400, "invalidFilter", "Unsupported filter or invalid pagination")
			return
		}
		rows, total, err := db.DirectoryUsersPage(r.Context(), connectionID, field, value, start-1, count)
		if err != nil {
			scimStoreError(w, err)
			return
		}
		out := make([]scimUser, 0, len(rows))
		for _, row := range rows {
			out = append(out, scimPublic(row))
		}
		scimWrite(w, 200, map[string]any{"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:ListResponse"}, "totalResults": total, "startIndex": start, "itemsPerPage": len(out), "Resources": out})
		return
	}
	var existing *iam.DirectoryUserResource
	if id != "" {
		var err error
		existing, err = db.DirectoryUserByID(r.Context(), connectionID, id)
		if err != nil {
			scimStoreError(w, err)
			return
		}
		if r.Method == http.MethodGet {
			scimWrite(w, 200, scimPublic(*existing))
			return
		}
	}
	var in scimUser
	if r.Method == http.MethodDelete {
		in = scimPublic(*existing)
		active := false
		in.Active = &active
	} else {
		if !directoryDecode(r, &in) {
			scimError(w, 400, "invalidSyntax", "Invalid JSON")
			return
		}
		validSchema := false
		for _, schema := range in.Schemas {
			if schema == scimUserSchema {
				validSchema = true
			}
		}
		if !validSchema || (in.ID != "" && in.ID != id) || (existing != nil && in.ExternalID != existing.ExternalID) {
			scimError(w, 400, "invalidValue", "Invalid schema or immutable identity")
			return
		}
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	d, _, err := db.ProvisionDirectoryUser(r.Context(), connectionID, in.ExternalID, in.UserName, in.DisplayName, active)
	if err != nil {
		scimStoreError(w, err)
		return
	}
	if r.Method == http.MethodDelete {
		scimWrite(w, 204, nil)
		return
	}
	row, err := db.DirectoryUserByID(r.Context(), connectionID, d.ID)
	if err != nil {
		scimStoreError(w, err)
		return
	}
	code := 200
	if r.Method == http.MethodPost {
		code = 201
	}
	w.Header().Set("Location", "/scim/v2/connections/"+connectionID+"/Users/"+d.ID)
	scimWrite(w, code, scimPublic(*row))
}
