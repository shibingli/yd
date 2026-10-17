module github.com/shibingli/yd/benchmarks/compress

go 1.27.2

require (
	github.com/andybalholm/brotli v1.2.6
	github.com/golang/snappy v1.0.0
	github.com/klauspost/compress v1.20.3
	github.com/pierrec/lz4/v4 v4.1.33
	github.com/shibingli/yd v0.0.0
)

require (
	github.com/bytedance/gopkg v0.1.4 // indirect
	github.com/bytedance/sonic v1.15.4 // indirect
	github.com/bytedance/sonic/loader v0.5.2 // indirect
	github.com/cloudwego/base64x v0.1.7 // indirect
	github.com/goccy/go-json v0.11.2 // indirect
	github.com/klauspost/cpuid/v2 v2.4.1 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	golang.org/x/arch v0.32.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
)

replace github.com/shibingli/yd => ../..
