//go:build windows

package fssecure

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"time"

	"github.com/shibingli/ydx/errs"
)

// Windows well-known SIDs: LocalSystem and the built-in Administrators group. Granting them keeps
// the service manager and an administrator able to operate on the file without restoring access
// for ordinary local users. | Windows 已知 SID：LocalSystem 与内置 Administrators 组。保留它们使服务
// 管理器与管理员仍可操作文件，同时不恢复普通本地用户的访问。
const (
	sidLocalSystem     = "*S-1-5-18"
	sidAdministrators  = "*S-1-5-32-544"
	restrictTimeout    = 10 * time.Second
	directoryACLFlags  = "(OI)(CI)F"
	fileACLFlags       = "F"
	inheritanceRemove  = "/inheritance:r"
	grantReplacePrefix = "/grant:r"
)

// restrict replaces the inherited Windows ACL with an explicit one for the current user,
// LocalSystem and Administrators. icacls is the only supported mechanism: chmod does not touch
// Windows ACLs, so a 0600 file would still be readable by other local users. |
// restrict 用「当前用户、LocalSystem 与 Administrators」的显式 ACL 替换继承的 Windows ACL。
// 唯一支持的机制是 icacls：chmod 不会改动 Windows ACL，0600 文件仍可能被同机其他用户读取。
func restrict(path string, _ fs.FileMode) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	flags := fileACLFlags
	args := []string{path, inheritanceRemove, grantReplacePrefix}
	if info.IsDir() {
		flags = directoryACLFlags
	}
	args = append(args,
		sidLocalSystem+":"+flags,
		sidAdministrators+":"+flags,
	)
	if current, userErr := user.Current(); userErr == nil {
		if name := strings.TrimSpace(current.Username); name != "" {
			args = append(args, name+":"+flags)
		}
	}
	if info.IsDir() {
		// /T must follow the grant list; placing it before the SIDs makes icacls reject the
		// command (exit 87). | /T 必须位于授权列表之后；放在 SID 之前会让 icacls 拒绝命令（退出码 87）。
		args = append(args, "/T")
	}
	// icacls occasionally returns a transient failure while another process is rewriting the same
	// volume's ACLs or an antivirus scanner holds the file. Retry a few times with a short backoff;
	// the caller still receives the last error if every attempt fails, so a genuinely unsupported
	// filesystem stays fail-closed. | 另一个进程重写同一卷的 ACL 或杀毒软件持有文件时，icacls
	// 偶尔会返回瞬时失败。短暂退避后重试几次；全部失败仍把最后一个错误交给调用方，因此真正不支持
	// ACL 的文件系统仍然失败关闭。
	const attempts = 3
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), restrictTimeout)
		cmd := exec.CommandContext(ctx, "icacls", args...)
		output, runErr := cmd.CombinedOutput()
		cancel()
		if runErr == nil {
			return nil
		}
		lastErr = errs.WrapDetail(runErr, ErrRestrict, "icacls failed: %[1]s | icacls 失败：%[1]s",
			strings.TrimSpace(string(output)))
		if attempt < attempts {
			time.Sleep(time.Duration(attempt) * 50 * time.Millisecond)
		}
	}
	return lastErr
}
