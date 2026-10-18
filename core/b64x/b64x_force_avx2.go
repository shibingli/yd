//go:build b64x_native && goexperiment.simd && amd64 && b64x_force_avx2

package b64x

// forceLevel selects the AVX2 kernels for cross-environment testing.
// | forceLevel 为跨环境测试选择 AVX2 内核。
const forceLevel = 1
