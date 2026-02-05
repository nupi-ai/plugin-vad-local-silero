module github.com/nupi-ai/plugin-vad-local-silero

go 1.24.0

require (
	github.com/nupi-ai/nupi v0.0.0-00010101000000-000000000000
	github.com/yalue/onnxruntime_go v1.25.0
	google.golang.org/grpc v1.64.0
	google.golang.org/protobuf v1.33.0
)

require (
	golang.org/x/net v0.43.0 // indirect
	golang.org/x/sys v0.36.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240318140521-94a12d6c2237 // indirect
)

replace github.com/nupi-ai/nupi => ../nupi
