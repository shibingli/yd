package asynqx

import (
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/hibiken/asynq"

	"github.com/shibingli/yd/core/errx"
)

// Declared sentinel errors of the queue grammar, testable with errors.Is. | 队列语法的已声明哨兵错误，可用 errors.Is 判定。
var (
	// ErrInvalidQueueSpec indicates a segment is not "name:weight" - no colon, an empty name, | ErrInvalidQueueSpec 表示某个片段不是 "名称:权重"：
	// an empty segment between commas, or a repeated name. | 缺冒号、名称为空、逗号间出现空片段，或名称重复。
	ErrInvalidQueueSpec = errx.New("asynqx: invalid queue spec | asynqx：队列配置格式非法")
	// ErrInvalidQueueWeight indicates the weight is not a positive integer. | ErrInvalidQueueWeight 表示权重不是正整数。
	ErrInvalidQueueWeight = errx.New("asynqx: invalid queue weight | asynqx：队列权重非法")
)

// defaultQueue is the queue used when no weight is configured, matching asynq's own default. | defaultQueue 是未配置任何权重时使用的队列，与 asynq 自身默认一致。
const defaultQueue = "default"

// ParseQueues parses the "name:weight" comma-separated queue spec, for example | ParseQueues 解析 "名称:权重" 形式的逗号分隔队列配置，例如
// "critical:3, default:2, low:1". | "critical:3, default:2, low:1"。
//
// Whitespace around names, weights and segments is ignored, so a config-file value may be | 名称、权重与片段两侧的空白都会被忽略，因此配置文件中可以为了可读性加空格。
// spaced for readability. An empty (or all-whitespace) spec returns an empty table, which | 空串（或全空白）返回空表，
// callers treat the same way as Options.Queues being left empty. | 调用方对它的处理与 Options.Queues 留空一致。
//
// Malformed input is reported through declared sentinels and yields a nil table: | 非法输入通过已声明的哨兵错误上报并返回 nil 表：
// ErrInvalidQueueSpec for a broken segment or a repeated name, ErrInvalidQueueWeight when | 片段损坏或名称重复返回 ErrInvalidQueueSpec，权重不是正整数返回 ErrInvalidQueueWeight。
// the weight is not a positive integer.
func ParseQueues(s string) (map[string]int, error) {
	spec := strings.TrimSpace(s)
	out := make(map[string]int)
	if spec == "" {
		return out, nil
	}
	for _, seg := range strings.Split(spec, ",") {
		name, weightText, ok := strings.Cut(seg, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, errx.Detail(ErrInvalidQueueSpec, "%q", strings.TrimSpace(seg))
		}
		if _, dup := out[name]; dup {
			return nil, errx.Detail(ErrInvalidQueueSpec, "repeated queue %[1]q | 重复的队列 %[1]q", name)
		}
		weight, err := strconv.Atoi(strings.TrimSpace(weightText))
		if err != nil || weight <= 0 {
			return nil, errx.Detail(ErrInvalidQueueWeight, "%q", strings.TrimSpace(weightText))
		}
		out[name] = weight
	}
	return out, nil
}

// ExponentialBackoff returns an asynq retry-delay policy that doubles the delay per retry. | ExponentialBackoff 返回按重试次数逐次翻倍的 asynq 重试延迟策略。
//
// asynq passes the zero-based retried count, so the first retry (n=0) waits base and the second
// (n=1) waits 2*base. The delay is base * 2^n, plus a jitter of up to 10% so a batch of tasks that
// failed together does not wake up together; a negative count is treated as the first retry.
// | asynq 传入的是从 0 开始的重试计数，因此第一次重试（n=0）等待 base，第二次（n=1）等待 2*base。
// 延迟为 base * 2^n，再叠加不超过其 10% 的抖动，避免同时失败的一批任务在同一瞬间齐刷刷重试；
// 负数按首次重试处理。
//
// The jitter never pushes a delay past max and every capped step is exactly max, so the delay | 抖动不会把延迟推过 max，且封顶档恒为 max 本身，因此延迟随重试次数单调不减，
// grows monotonically with the retry count and an operator can predict the retry schedule. | 运维可以据此推断重试节奏。
//
// base <= 0 disables the delay (always zero). max <= 0 means "no cap": the doubling stops at | base <= 0 表示不延迟（恒为 0）。max <= 0 表示不设上限：
// the Duration saturation point instead of overflowing. | 倍增在 Duration 饱和处停下，不会溢出。
func ExponentialBackoff(base, max time.Duration) func(int, error, *asynq.Task) time.Duration {
	return func(n int, _ error, _ *asynq.Task) time.Duration {
		if base <= 0 {
			return 0
		}
		ceil := max
		if ceil <= 0 {
			ceil = time.Duration(math.MaxInt64)
		}
		delay := min(base, ceil)
		if n < 0 {
			n = 0
		}
		for i := 0; i < n && delay < ceil; i++ {
			if delay > ceil-delay {
				// Doubling again would pass the cap: clamp instead of overflowing. | 再翻一倍就会越过上限：直接封顶，避免乘法溢出。
				delay = ceil
				break
			}
			delay *= 2
		}
		if j := delay / 10; j > 0 {
			delay += rand.N(j)
		}
		if delay > ceil {
			delay = ceil
		}
		return delay
	}
}

// queueWeights returns the effective queue weights for the server. | queueWeights 返回服务端生效的队列权重。
//
// Segments that asynq would ignore are dropped, and an empty result falls back to the single | asynq 会忽略的条目在此被剔除；结果为空时回落到单条 default 队列。
// default queue. The returned map is always non-nil and freshly allocated, so mutating | 返回值永不为 nil 且为新分配，因此启动后修改 Options.Queues
// Options.Queues afterwards cannot affect a running server. | 不会影响已在运行的服务端。
func queueWeights(configured map[string]int) map[string]int {
	out := make(map[string]int, len(configured))
	for name, weight := range configured {
		name = strings.TrimSpace(name)
		if name != "" && weight > 0 {
			out[name] = weight
		}
	}
	if len(out) == 0 {
		return map[string]int{defaultQueue: 1}
	}
	return out
}
