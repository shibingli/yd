module github.com/shibingli/yd/benchmarks/logx

go 1.27.2

require (
	github.com/rs/zerolog v1.34.0
	github.com/shibingli/yd v0.0.0
	github.com/sirupsen/logrus v1.9.3
	go.uber.org/zap v1.27.0
)

require (
	github.com/cristalhq/base64 v0.1.2 // indirect
	github.com/goccy/go-json v0.11.2 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/stretchr/testify v1.10.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
)

replace github.com/shibingli/yd => ../..
