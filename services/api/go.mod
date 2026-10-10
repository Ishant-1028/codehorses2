module github.com/g1tsg/codehorses/services/api

go 1.24

require (
	github.com/g1tsg/codehorses/packages/ledger v0.0.0-00010101000000-000000000000
	github.com/g1tsg/codehorses/packages/scoringrules v0.0.0-00010101000000-000000000000
	github.com/g1tsg/codehorses/packages/store v0.0.0-00010101000000-000000000000
	github.com/google/uuid v1.6.0
	github.com/lib/pq v1.12.3
	github.com/redis/go-redis/v9 v9.7.0
	golang.org/x/crypto v0.27.0
)

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.7.1 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.8.0 // indirect
	golang.org/x/text v0.18.0 // indirect
)

replace github.com/g1tsg/codehorses/packages/ledger => ../../packages/ledger

replace github.com/g1tsg/codehorses/packages/scoringrules => ../../packages/scoring-rules

replace github.com/g1tsg/codehorses/packages/store => ../../packages/store
