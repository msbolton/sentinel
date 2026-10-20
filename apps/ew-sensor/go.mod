module github.com/sentinel/ew-sensor

go 1.24.0

replace github.com/sentinel/go-common => ../../libs/common-go

replace github.com/sentinel/proto-gen => ../../libs/proto-gen/src/go

require (
	github.com/sentinel/go-common v0.0.0
	github.com/sentinel/proto-gen v0.0.0
	google.golang.org/protobuf v1.36.11
	gopkg.in/yaml.v3 v3.0.1
)
