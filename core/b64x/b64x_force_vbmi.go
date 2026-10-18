//go:build b64x_native && goexperiment.simd && amd64 && b64x_force_vbmi

package b64x

// forceLevel selects the AVX-512 VBMI/VBMI2 kernels for cross-environment testing.
// | forceLevel 为跨环境测试选择 AVX-512 VBMI/VBMI2 内核。
const forceLevel = 3
