// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2

package config

import "github.com/zeromicro/go-zero/rest"

type Config struct {
	rest.RestConf
	DatabaseUrl             string
	JwtSecret               string
	InternalToken           string
	AgentUrl                string
	AllowedOrigins          string
	CookieSecure            bool
	FileStoreProvider       string `json:",optional"`
	FileStoreCleanupEnabled bool   `json:",optional"`
	FileStoreEndpoint       string `json:",optional"`
	FileStoreRegion         string `json:",optional"`
	FileStoreBucket         string `json:",optional"`
	FileStoreAccessKey      string `json:",optional"`
	FileStoreSecretKey      string `json:",optional"`
	FileStoreAllowHTTP      bool   `json:",optional"`
}
