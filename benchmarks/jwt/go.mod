module github.com/shibingli/yd/benchmarks/jwt

go 1.27.2

replace github.com/shibingli/yd => ../..

require (
	github.com/cristalhq/jwt/v5 v5.4.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/kataras/jwt v0.3.0
	github.com/shibingli/yd v0.0.0-00010101000000-000000000000
)

require (
	github.com/cristalhq/base64 v0.1.2 // indirect
	github.com/goccy/go-json v0.11.2 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)
