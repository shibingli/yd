//go:build amd64 && goexperiment.simd && !b64x_std && b64x_force_avx512

package b64x

// forceLevel selects the baseline AVX-512 kernels for cross-environment testing.
// | forceLevel 为跨环境测试选择基础 AVX-512 内核。
const forceLevel = 2
