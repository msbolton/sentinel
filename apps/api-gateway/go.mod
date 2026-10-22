module github.com/sentinel/api-gateway

go 1.25.0

require (
	github.com/MicahParks/keyfunc/v3 v3.8.2
	github.com/coder/websocket v1.8.15
	github.com/confluentinc/confluent-kafka-go/v2 v2.5.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/jackc/pgx/v5 v5.8.0
	github.com/maxmind/mmdbwriter v1.2.0
	github.com/oschwald/maxminddb-golang/v2 v2.6.0
	github.com/sentinel/go-common v0.0.0
	github.com/sentinel/proto-gen v0.0.0
	go.uber.org/zap v1.27.0
	golang.org/x/time v0.15.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/MicahParks/jwkset v0.11.3 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go4.org/netipx v0.0.0-20231129151722-fdeea329fbba // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)

replace github.com/sentinel/go-common => ../../libs/common-go

replace github.com/sentinel/proto-gen => ../../libs/proto-gen/src/go
