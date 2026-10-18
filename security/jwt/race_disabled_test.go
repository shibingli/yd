//go:build !race

package jwt

// raceEnabled reports that the race detector is inactive. | raceEnabled 表示竞态检测器未启用。
const raceEnabled = false
