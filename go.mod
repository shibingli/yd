module github.com/shibingli/yd

go 1.27.3

require (
	github.com/alicebob/miniredis/v2 v2.40.0
	github.com/emmansun/gmsm v0.45.2
	github.com/fsnotify/fsnotify v1.10.1
	github.com/glebarez/sqlite v1.11.0
	github.com/kardianos/service v1.3.0
	github.com/quic-go/quic-go v0.64.1
	github.com/redis/go-redis/v9 v9.23.0
	github.com/shibingli/ydx/afs v0.1.6
	github.com/shibingli/ydx/asynq v0.1.10
	github.com/shibingli/ydx/base64 v0.1.12
	github.com/shibingli/ydx/buffpool v0.1.4
	github.com/shibingli/ydx/cache v0.1.17
	github.com/shibingli/ydx/captcha v0.1.1
	github.com/shibingli/ydx/config v0.1.8
	github.com/shibingli/ydx/cron v0.1.2
	github.com/shibingli/ydx/errs v0.1.4
	github.com/shibingli/ydx/gc v0.1.1
	github.com/shibingli/ydx/gorm v0.1.13
	github.com/shibingli/ydx/guomi v0.1.2
	github.com/shibingli/ydx/i18n v0.1.10
	github.com/shibingli/ydx/json v0.1.14
	github.com/shibingli/ydx/jwt v0.1.11
	github.com/shibingli/ydx/log v0.1.11
	github.com/shibingli/ydx/middleware v0.1.13
	github.com/shibingli/ydx/middleware/ratelimit v0.1.12
	github.com/shibingli/ydx/nodeid v0.1.4
	github.com/shibingli/ydx/openapi v0.1.10
	github.com/shibingli/ydx/openapi/ydrouter v0.1.10
	github.com/shibingli/ydx/protocol v0.1.5
	github.com/shibingli/ydx/realtime v0.1.7
	github.com/shibingli/ydx/redis v0.1.10
	github.com/shibingli/ydx/regexpx v0.1.7
	github.com/shibingli/ydx/render v0.1.10
	github.com/shibingli/ydx/router v0.1.12
	github.com/shibingli/ydx/sse v0.1.10
	github.com/shibingli/ydx/websocket v0.1.13
	github.com/shibingli/ydx/webtransport v0.1.0
	github.com/shibingli/ydx/workflow v0.1.3
	github.com/spf13/afero v1.15.0
	github.com/spf13/pflag v1.0.10
	github.com/yuin/goldmark v1.8.7
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/crypto v0.58.0
	google.golang.org/grpc v1.85.0
	gorm.io/gorm v1.31.2
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/andybalholm/brotli v1.2.7 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dunglas/httpsfv v1.1.2 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/glebarez/go-sqlite v1.23.0 // indirect
	github.com/go-sql-driver/mysql v1.10.1 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/godoes/gorm-dameng v0.7.2 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hibiken/asynq v0.26.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.11.0 // indirect
	github.com/jackc/puddle/v2 v2.2.3 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/klauspost/compress v1.20.3 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/pelletier/go-toml/v2 v2.4.3 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/webtransport-go v0.13.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/sagikazarmark/locafero v0.12.0 // indirect
	github.com/shibingli/ydx/middleware/accesslog v0.1.12 // indirect
	github.com/shibingli/ydx/middleware/asyncafter v0.1.12 // indirect
	github.com/shibingli/ydx/middleware/compress v0.1.12 // indirect
	github.com/shibingli/ydx/middleware/cors v0.1.12 // indirect
	github.com/shibingli/ydx/middleware/recovery v0.1.12 // indirect
	github.com/shibingli/ydx/middleware/reqlog v0.1.12 // indirect
	github.com/shibingli/ydx/middleware/requestid v0.1.12 // indirect
	github.com/shibingli/ydx/sysinfo v0.1.4 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/viper v1.21.0 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/yuin/gopher-lua v1.1.2 // indirect
	go.uber.org/atomic v1.12.0 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/time v0.16.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261026165753-2ffab32bce7e // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gorm.io/driver/mysql v1.6.0 // indirect
	gorm.io/driver/postgres v1.6.3 // indirect
	modernc.org/libc v1.79.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
	modernc.org/sqlite v1.61.0 // indirect
)
