package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
)

// failOnWriteFS fails opening one path for writing while delegating everything else. |
// failOnWriteFS 对指定路径的写入打开操作返回失败，其它操作全部委托给底层文件系统。
type failOnWriteFS struct {
	afero.Fs
	failPath string
}

// OpenFile implements afero.Fs. | OpenFile 实现 afero.Fs。
func (f *failOnWriteFS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if filepath.ToSlash(name) == filepath.ToSlash(f.failPath) && flag&os.O_WRONLY != 0 {
		return nil, errors.New("injected write failure")
	}
	return f.Fs.OpenFile(name, flag, perm)
}

// TestApplySyncPlanRollsBack proves a mid-apply failure restores overwritten files and removes
// files that did not exist before, so the configuration baseline cannot advance on a partial
// write. | TestApplySyncPlanRollsBack 验证应用中途失败会恢复已覆盖文件并删除此前不存在的文件，
// 从而不会在部分写入后推进配置基线。
func TestApplySyncPlanRollsBack(t *testing.T) {
	mem := afero.NewMemMapFs()
	dir := "app"
	syncWrite(t, mem, dir+"/a.txt", "old-a")
	syncWrite(t, mem, dir+"/"+syncConfigBaselineRel, "old-baseline")
	baselinePath := dir + "/" + syncConfigBaselineRel
	failing := &failOnWriteFS{Fs: mem, failPath: baselinePath}
	te := newTestEnv(t, failing, "", false)
	plan := &syncPlan{Files: []syncFile{
		{Rel: "a.txt", Content: []byte("new-a"), Existing: []byte("old-a"), Kind: syncOverwrite},
		{Rel: "b.txt", Content: []byte("new-b"), Kind: syncAdd},
		{Rel: syncConfigBaselineRel, Content: []byte("new-baseline"), Existing: []byte("old-baseline"), Kind: syncOverwrite},
	}}
	err := applySyncPlan(te.env, dir, plan)
	if err == nil {
		t.Fatalf("apply should fail | 应用本应失败")
	}
	if got := syncRead(t, mem, dir+"/a.txt"); got != "old-a" {
		t.Fatalf("a.txt = %q, want old-a | 回滚未恢复 a.txt", got)
	}
	if _, err := afero.ReadFile(mem, dir+"/b.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("b.txt should not exist after rollback: %v | 回滚后 b.txt 不应存在", err)
	}
	if got := syncRead(t, mem, baselinePath); got != "old-baseline" {
		t.Fatalf("baseline = %q, want old-baseline | 回滚后基线不应推进", got)
	}
}

// TestSortSyncPlanFilesKeepsBaselineLast proves the derived snapshot is always applied after the
// baseline and local files it describes. | TestSortSyncPlanFilesKeepsBaselineLast 验证派生快照始终
// 在它描述的基线与本地文件之后应用。
func TestSortSyncPlanFilesKeepsBaselineLast(t *testing.T) {
	files := []syncFile{
		{Rel: syncConfigBaselineRel},
		{Rel: "z.txt"},
		{Rel: syncConfigLocalRel},
	}
	sortSyncPlanFiles(files)
	if files[len(files)-1].Rel != syncConfigBaselineRel {
		t.Fatalf("baseline is not last: %+v | 基线不在最后", files)
	}
}
