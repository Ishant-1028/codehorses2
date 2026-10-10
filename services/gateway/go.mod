module github.com/g1tsg/codehorses/services/gateway

go 1.24

require (
	github.com/coder/websocket v1.8.13
	github.com/g1tsg/codehorses/packages/store v0.0.0-00010101000000-000000000000
	github.com/redis/go-redis/v9 v9.7.0
)

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
)

replace github.com/g1tsg/codehorses/packages/store => ../../packages/store
