//go:build amd64 && goexperiment.simd && !b64x_std && !b64x_force_avx2 && !b64x_force_avx512 && !b64x_force_vbmi

package b64x

// forceLevel keeps the runtime feature dispatch in production. | forceLevel 让生产构建保持运行时特性分发。
const forceLevel = 0
