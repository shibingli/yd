package metricsx

import "time"

// latencyBounds are the inclusive upper bounds of the latency histogram. | latencyBounds 是延迟直方图的
// 闭区间上界。
//
// The boundaries cover the range of ordinary HTTP middleware: a few hundred microseconds for a header
// pass-through up to ten seconds for a slow upstream. The histogram is bounded so a request path never
// allocates or grows memory. | 边界覆盖普通 HTTP 中间件的范围：从头部直通的几百微秒到慢上游的十秒。
// 直方图容量固定，因此请求路径既不分配也不增长内存。
var latencyBounds = [...]time.Duration{
	250 * time.Microsecond,
	500 * time.Microsecond,
	time.Millisecond,
	2 * time.Millisecond,
	5 * time.Millisecond,
	10 * time.Millisecond,
	20 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	200 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
}

// bucketIndex returns the first bucket whose upper bound covers d. | bucketIndex 返回上界覆盖 d 的第一个桶。
func bucketIndex(d time.Duration) int {
	for i, bound := range latencyBounds {
		if d <= bound {
			return i
		}
	}
	return len(latencyBounds)
}

// latency computes the mean, the 95th percentile and the maximum. | latency 计算平均值、95 分位与最大值。
//
// The percentile is the upper bound of the bucket holding the 95th observation, clamped to the exact
// maximum so a tiny sample never reports a percentile above its longest request. | 分位取第 95 个观测所在
// 桶的上界，并夹到精确最大值，因此极小样本也不会报出高于最长请求的分位。
func (e *Entry) latency(requests uint64) (average, p95, maximum time.Duration) {
	maximum = time.Duration(e.maxNano.Load())
	if requests == 0 {
		return 0, 0, maximum
	}
	average = time.Duration(e.totalNano.Load() / requests)

	target := (requests*95 + 99) / 100
	var seen uint64
	for i := range e.buckets {
		seen += e.buckets[i].Load()
		if seen < target {
			continue
		}
		if i < len(latencyBounds) {
			p95 = latencyBounds[i]
		} else {
			p95 = maximum
		}
		break
	}
	if p95 > maximum {
		p95 = maximum
	}
	// A concurrent snapshot can observe Requests just after it was incremented and just before the
	// histogram bucket was; falling back to the exact maximum keeps the reading truthful. | 并发快照可能
	// 刚好看到 Requests 已递增、直方图桶尚未递增；回退到精确最大值可以保持读数真实。
	if p95 == 0 && maximum > 0 {
		p95 = maximum
	}
	return average, p95, maximum
}
