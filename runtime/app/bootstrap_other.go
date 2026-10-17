//go:build !unix && !windows

package app

import "github.com/shibingli/yd/runtime/server"

// platformReexec reports that this platform cannot replace its process image;
// the caller fails closed with the startup sentinel. | platformReexec 表示该平台无法
// 替换进程镜像；调用方以启动哨兵失败关闭。
func platformReexec(env []string) error {
	return server.ErrHTTP2ExtendedConnectRequired
}
