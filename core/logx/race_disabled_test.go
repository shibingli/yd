//go:build !race

package logx

// raceEnabled reports whether the test binary was built with the race detector. | raceEnabled
// 报告测试二进制是否以竞态检测器构建。
const raceEnabled = false
