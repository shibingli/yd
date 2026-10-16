// This file implements the strictly offline verifier: it only ever reads local files and never builds a
// request, which is what makes it safe to run in CI and inside a build sandbox. | 本文件实现严格离线的
// 校验器：它只读取本地文件，绝不构造请求，因此可以安全地在 CI 与构建沙箱中运行。

package assets

import (
	"errors"

	"github.com/spf13/afero"
	"github.com/shibingli/yd/afs"
	"github.com/shibingli/yd/errx"
)

// VerifyReport groups the assets by verification outcome. | VerifyReport 按校验结果对资产分组。
//
// All three lists hold asset names in manifest order. | 三个列表都按清单顺序保存资产名。
type VerifyReport struct {
	// OK lists assets whose local file exists and matches the lock. | OK 列出本地文件存在且与锁定一致的资产。
	OK []string
	// Missing lists assets whose local file does not exist. | Missing 列出本地文件不存在的资产。
	Missing []string
	// Mismatch lists assets whose content differs from the lock, including assets that have no lock
	// yet: an unlocked asset cannot be verified, which is a problem worth reporting. | Mismatch 列出内容
	// 与锁定不符的资产，含尚未锁定的资产：未锁定的资产无法校验，本身就值得报告。
	Mismatch []string
}

// Verify checks every locked asset against the file below root. | Verify 依据锁定值校验 root 下的每个资产文件。
//
// It performs no network access whatsoever: a file is only opened, hashed and compared. The returned
// error satisfies errors.Is(err, ErrVerifyFailed) when at least one asset is missing or mismatching,
// while the report still describes every asset, so the caller can print a complete picture. | 它完全不
// 访问网络：只打开文件、计算哈希并比较。只要有资产缺失或不符，返回的错误就满足
// errors.Is(err, ErrVerifyFailed)，同时报告仍描述全部资产，便于调用方打印完整结果。
func Verify(fsys afero.Fs, m *Manifest, root string) (*VerifyReport, error) {
	if m == nil {
		return nil, ErrNilManifest
	}
	if fsys == nil {
		fsys = afero.NewOsFs()
	}

	rep := &VerifyReport{}
	errs := make([]error, 0, len(m.Assets))
	for i := range m.Assets {
		a := m.Assets[i]
		local := a.LocalPath(root)

		if !afs.Exists(fsys, local) {
			rep.Missing = append(rep.Missing, a.Name)
			errs = append(errs, errx.Detail(ErrVerifyFailed, "%s: %s 缺失", a.Name, local))
			continue
		}
		if a.SHA256 == "" {
			rep.Mismatch = append(rep.Mismatch, a.Name)
			errs = append(errs, errx.Detail(ErrVerifyFailed, "%s: 未锁定校验和", a.Name))
			continue
		}

		sum, err := afs.Sha256File(fsys, local)
		if err != nil {
			rep.Mismatch = append(rep.Mismatch, a.Name)
			errs = append(errs, errx.WrapDetail(err, ErrVerifyFailed, "%s", a.Name))
			continue
		}
		if sum != a.SHA256 {
			rep.Mismatch = append(rep.Mismatch, a.Name)
			errs = append(errs, errx.Detail(ErrVerifyFailed, "%s: 锁定 %s，实际 %s", a.Name, a.SHA256, sum))
			continue
		}
		rep.OK = append(rep.OK, a.Name)
	}

	if len(errs) > 0 {
		return rep, errors.Join(errs...)
	}
	return rep, nil
}
