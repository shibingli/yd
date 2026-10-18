module github.com/shibingli/yd/benchmarks/crypto

go 1.27.2

require (
	github.com/emmansun/gmsm v0.45.0
	github.com/shibingli/yd v0.0.0
	github.com/tjfoc/gmsm v1.4.1
	golang.org/x/crypto v0.58.0
)

require (
	github.com/cristalhq/base64 v0.1.2 // indirect
	golang.org/x/sys v0.49.0 // indirect
)

replace github.com/shibingli/yd => ../..
