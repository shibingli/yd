// Package cachebench compares the yd local cache with mainstream Go cache libraries.
// | Package cachebench 把 yd 本地缓存与主流 Go 缓存库放在同一口径下对比。
//
// The module is deliberately independent: Ristretto, FreeCache, Theine, BigCache, go-cache,
// ttlcache and golang-lru stay out of the framework's go.mod. | 该模块刻意独立：Ristretto、
// FreeCache、Theine、BigCache、go-cache、ttlcache 与 golang-lru 都不会进入框架的 go.mod。
package cachebench
