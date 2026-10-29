// This file implements backup, rollback and file application for `yd sync`.
//
// 本文件实现 `yd sync` 的备份、回滚与文件应用。

package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/shibingli/yd/tooling/assets"
	"github.com/shibingli/ydx/errs"
	"github.com/spf13/afero"
)

// syncBackupPath returns a unique backup directory below .yd/backup. | syncBackupPath 返回 .yd/backup
// 下唯一的备份目录。
func syncBackupPath(fs afero.Fs, dir string) string {
	stamp := time.Now().UTC().Format("20060102-150405")
	base := filepath.Join(dir, ".yd", "backup", "sync-"+stamp)
	path := base
	for i := 1; ; i++ {
		if _, err := fs.Stat(path); err != nil {
			return path
		}
		path = fmt.Sprintf("%s-%d", base, i)
	}
}

// backupSyncFiles copies every file that will be overwritten, plus go.mod/go.sum and the asset lock
// when their upgrade steps are enabled. | backupSyncFiles 复制每个将被覆盖的文件；启用相应升级步骤时
// 还会备份 go.mod/go.sum 与资产锁。
func backupSyncFiles(fs afero.Fs, dir string, plan *syncPlan, upgradeGo, upgradeAssets bool, backupPath string) (string, error) {
	rels := make([]string, 0, len(plan.Files)+3)
	seen := make(map[string]bool)
	add := func(rel string) {
		if rel == "" || seen[rel] {
			return
		}
		seen[rel] = true
		rels = append(rels, rel)
	}
	for _, file := range plan.Files {
		if file.Kind == syncOverwrite {
			add(file.Rel)
		}
	}
	if upgradeGo {
		add("go.mod")
		add("go.sum")
	}
	if upgradeAssets {
		add(assets.DefaultManifestPath)
	}
	backedUp := 0
	for _, rel := range rels {
		data, exists, err := readProjectFile(fs, dir, rel)
		if err != nil {
			return "", err
		}
		if !exists {
			continue
		}
		dst := filepath.Join(backupPath, filepath.FromSlash(rel))
		if err := fs.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return "", errs.WrapDetail(err, ErrSyncApply, "create backup directory for %[1]s | 为 %[1]s 创建备份目录", rel)
		}
		if err := afero.WriteFile(fs, dst, data, 0o600); err != nil {
			return "", errs.WrapDetail(err, ErrSyncApply, "back up %[1]s | 备份 %[1]s", rel)
		}
		backedUp++
	}
	if backedUp == 0 {
		return "", nil
	}
	return backupPath, nil
}

// applySyncPlan writes the planned adds and overwrites. | applySyncPlan 写入计划中的新增与覆盖。
func applySyncPlan(env *Env, dir string, plan *syncPlan) error {
	if err := applySyncFiles(env, dir, plan); err != nil {
		return err
	}
	_, overwrites := plan.counts()
	fmt.Fprintf(env.Out, "scaffold synced | 脚手架已同步: %d added | 新增, %d overwritten | 覆盖\n", len(plan.Files)-overwrites, overwrites)
	return nil
}

// applySyncFiles performs the writes and returns a rollback error on failure. | applySyncFiles 执行
// 写入，并在失败时返回包含回滚结果的错误。
func applySyncFiles(env *Env, dir string, plan *syncPlan) error {
	applied := make([]syncFile, 0, len(plan.Files))
	for _, file := range plan.Files {
		path := filepath.Join(dir, filepath.FromSlash(file.Rel))
		if err := env.FS.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return rollbackSyncFiles(env, dir, applied,
				errs.WrapDetail(err, ErrSyncApply, "create directory for %[1]s | 为 %[1]s 创建目录", file.Rel))
		}
		mode := file.Mode
		if mode == 0 {
			mode = 0o644
		}
		// Record the file before writing: a partial write may already have truncated it, and the
		// rollback path must restore it as well. | 在写入前登记文件：部分写入可能已经截断目标，回滚
		// 路径同样必须恢复它。
		applied = append(applied, file)
		if err := afero.WriteFile(env.FS, path, file.Content, mode); err != nil {
			return rollbackSyncFiles(env, dir, applied,
				errs.WrapDetail(err, ErrSyncApply, "write %[1]s | 写入 %[1]s", file.Rel))
		}
		if file.Rel == syncConfigLocalRel {
			// The local override may contain secrets; keep it private even when it already existed
			// with a broader mode. | 本地覆盖可能含密钥；即使文件原本权限更宽，也把它收紧为私有。
			_ = env.FS.Chmod(path, 0o600)
		}
	}
	return nil
}

// rollbackSyncFiles restores every file already written by a failed apply, removes files that did
// not exist before, and joins rollback failures with the original cause. | rollbackSyncFiles 恢复
// 应用失败前已写入的每个文件、删除此前不存在的文件，并把回滚失败与原始原因聚合返回。
func rollbackSyncFiles(env *Env, dir string, applied []syncFile, cause error) error {
	causes := []error{cause}
	for i := len(applied) - 1; i >= 0; i-- {
		file := applied[i]
		path := filepath.Join(dir, filepath.FromSlash(file.Rel))
		if file.Kind == syncAdd {
			if err := env.FS.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				causes = append(causes, errs.WrapDetail(err, ErrSyncApply, "remove %[1]s after rollback | 回滚时删除 %[1]s", file.Rel))
			}
			continue
		}
		mode := file.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := afero.WriteFile(env.FS, path, file.Existing, mode); err != nil {
			causes = append(causes, errs.WrapDetail(err, ErrSyncApply, "restore %[1]s after rollback | 回滚时恢复 %[1]s", file.Rel))
		}
	}
	return errors.Join(causes...)
}
