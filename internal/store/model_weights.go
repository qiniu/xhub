package store

import (
	"encoding/json"
	"time"

	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/router"
)

// SaveModelDirectory 原子保存部署变更、衔接改名授权并清理模型默认及客户模板里的失效分配。
// 参数 entry 非空表示创建/更新，deletedID 非空表示删除，directory 是变更后的完整目录（含配置文件部署）。
// 由当前部署管理接口调用；失败整笔回滚，成功后清缓存，不修改内存目录，也不在启动时执行数据修复。
func (s *Store) SaveModelDirectory(entry *ProxyModel, deletedID string, directory []config.ModelEntry) error {
	if s == nil {
		return nil
	}
	tx := s.Engine.NewSession()
	defer tx.Close()
	if err := tx.Begin(); err != nil {
		return err
	}
	defer tx.Rollback()
	if entry != nil {
		// 在修改部署前取得旧公开名，并在同一事务内迁移显式授权；失败不能只保存改名。
		var old proxyModelRow
		found, err := tx.NoCache().ID(entry.ID).Get(&old)
		if err != nil {
			return err
		}
		if found && old.ModelName != entry.ModelName {
			keepOld := false
			for _, dep := range directory {
				if dep.ModelName == old.ModelName {
					keepOld = true
				}
			}
			if err := s.renameModelGrants(tx, old.ModelName, entry.ModelName, keepOld); err != nil {
				return err
			}
		}
		params, err := json.Marshal(entry.Params)
		if err != nil {
			return err
		}
		info, err := json.Marshal(entry.Info)
		if err != nil {
			return err
		}
		row := proxyModelRow{ID: entry.ID, ModelName: entry.ModelName, Params: string(params), Info: string(info), UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
		n, err := tx.ID(row.ID).AllCols().Update(&row)
		if err != nil {
			return err
		}
		if n == 0 {
			if _, err = tx.Insert(&row); err != nil {
				return err
			}
		}
	}
	if deletedID != "" {
		if _, err := tx.ID(deletedID).Delete(&proxyModelRow{}); err != nil {
			return err
		}
	}
	var defaults []configRow
	if err := tx.Where("namespace = ?", "model_defaults").Find(&defaults); err != nil {
		return err
	}
	for _, row := range defaults {
		// 变更后仅剩一个部署无需权重分配，直接移除配置；避免无效权重影响后续路由。
		count := 0
		for _, dep := range directory {
			if dep.ModelName == row.Key {
				count++
			}
		}
		var allocations []router.Allocation
		if count > 1 {
			var weights router.DefaultWeights
			if err := json.Unmarshal([]byte(row.ValueJSON), &weights); err != nil {
				return err
			}
			allocations = router.CleanAllocations(weights.Allocations, []string{row.Key}, directory)
		}
		if len(allocations) == 0 {
			if _, err := tx.ID(coreIDs(row.Namespace, row.Key)).Delete(&configRow{}); err != nil {
				return err
			}
		} else {
			raw, err := json.Marshal(router.DefaultWeights{Allocations: allocations})
			if err != nil {
				return err
			}
			if string(raw) != row.ValueJSON {
				row.ValueJSON = string(raw)
				if _, err = tx.ID(coreIDs(row.Namespace, row.Key)).Cols("value_json").Update(&row); err != nil {
					return err
				}
			}
		}
	}
	// 框架存储也可独立使用；身份表存在时才同步清理客户模板，仍使用同一数据库事务。
	hasTemplates, err := s.Engine.IsTableExist(new(iam.RouteTemplate))
	if err != nil {
		return err
	}
	if hasTemplates {
		var templates []iam.RouteTemplate
		// xorm 的 ForUpdate 仅支持 MySQL；项目使用 PostgreSQL，显式 SQL 在同一事务内锁行。
		if err = tx.SQL("SELECT * FROM route_templates FOR UPDATE").Find(&templates); err != nil {
			return err
		}
		for _, row := range templates {
			var doc map[string]any
			if err = json.Unmarshal([]byte(row.Body), &doc); err != nil {
				return err
			}
			clean, err := router.CleanTemplateWeights(doc, directory)
			if err != nil {
				return err
			}
			raw, err := json.Marshal(clean)
			if err != nil {
				return err
			}
			if string(raw) != row.Body {
				row.Body = string(raw)
				if _, err = tx.ID(row.ID).Cols("body").Update(&row); err != nil {
					return err
				}
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.bust(new(proxyModelRow), new(configRow))
	return nil
}
