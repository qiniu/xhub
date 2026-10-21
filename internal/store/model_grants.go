package store

import (
	"encoding/json"
	"fmt"

	"xorm.io/xorm"
)

// renameModelGrants 在部署保存事务中衔接组织、团队、项目和 Key 的显式授权。
// old/new 为公开名，keepOld 表示旧名仍有部署；空授权继承语义不变，不改金额和历史记录。
// 调用者负责提交和回滚；读取并锁定行，损坏 JSON 或数据库错误使整笔改名回滚。
func (s *Store) renameModelGrants(tx *xorm.Session, old, name string, keepOld bool) error {
	for _, table := range []string{"organizations", "teams", "projects", "api_keys"} {
		exists, err := s.Engine.IsTableExist(table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		var rows []struct {
			ID     string `xorm:"id"`
			Models string `xorm:"models"`
		}
		// 表名来自固定白名单；按主键排序取得行锁，避免与其他改名的锁顺序相反。
		if err := tx.SQL("SELECT id, COALESCE(models, 'null') AS models FROM " + table + " ORDER BY id FOR UPDATE").Find(&rows); err != nil {
			return err
		}
		for _, row := range rows {
			var models []string
			if err := json.Unmarshal([]byte(row.Models), &models); err != nil {
				return fmt.Errorf("invalid model grants in %s: %w", table, err)
			}
			updated, changed := renamedModelGrants(models, old, name, keepOld)
			if !changed {
				continue
			}
			raw, err := json.Marshal(updated)
			if err != nil {
				return err
			}
			if _, err := tx.Exec("UPDATE "+table+" SET models = ? WHERE id = ?", string(raw), row.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// renamedModelGrants 返回改名后的授权副本及是否变化；只有包含旧名的显式授权才衔接。
// keepOld 用于共享公开名拆出部署；结果去重，不修改输入，空列表及不相关授权原样返回。
func renamedModelGrants(models []string, old, name string, keepOld bool) ([]string, bool) {
	found := false
	for _, model := range models {
		if model == old {
			found = true
		}
	}
	if !found || old == name {
		return models, false
	}
	out := make([]string, 0, len(models)+1)
	seen := map[string]bool{}
	for _, model := range models {
		if model == old && !keepOld {
			model = name
		}
		if !seen[model] {
			out = append(out, model)
			seen[model] = true
		}
	}
	if !seen[name] {
		out = append(out, name)
	}
	return out, true
}
