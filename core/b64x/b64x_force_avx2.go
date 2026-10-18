//go:build amd64 && goexperiment.simd && !b64x_std && b64x_force_avx2

package b64x

// forceLevel selects the AVX2 kernels for cross-environment testing.
// | forceLevel 为跨环境测试选择 AVX2 内核。
const forceLevel = 1
