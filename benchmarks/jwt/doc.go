// Package jwtbench measures the JWT signing and verification path of
// security/jwt against github.com/golang-jwt/jwt/v5, github.com/cristalhq/jwt/v5
// and github.com/kataras/jwt. It lives in its own module so the comparison
// dependencies never enter the framework's go.mod. | Package jwtbench
// 测量 security/jwt 相对 github.com/golang-jwt/jwt/v5、github.com/cristalhq/jwt/v5
// 与 github.com/kataras/jwt 的签发与验签路径；独立模块使对比依赖不会进入框架的
// go.mod。
package jwtbench
