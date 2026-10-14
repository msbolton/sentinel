module github.com/sentinel/api-gateway

go 1.25.0

require (
	github.com/MicahParks/keyfunc/v3 v3.8.2
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/jackc/pgx/v5 v5.8.0
	github.com/sentinel/go-common v0.0.0
	go.uber.org/zap v1.27.0
)

require (
	github.com/MicahParks/jwkset v0.11.3 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)

replace github.com/sentinel/go-common => ../../libs/common-go
