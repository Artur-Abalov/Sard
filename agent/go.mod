module github.com/Artur-Abalov/sard/agent

go 1.27.1

require (
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
)

require (
	github.com/Artur-Abalov/sard/agent/plugins/sdk v0.0.0
	github.com/Artur-Abalov/sard/proto/gen/go v0.0.0
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	go.yaml.in/yaml/v3 v3.0.5
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
	pgregory.net/rapid v1.3.0
)

// Workspace modules: build without go.work (CI, Docker). See ADR 0005.
replace (
	github.com/Artur-Abalov/sard/agent/plugins/sdk => ./plugins/sdk
	github.com/Artur-Abalov/sard/proto/gen/go => ../proto/gen/go
)
