module github.com/kayushkin/llm-bridge-runner

go 1.25.0

require (
	github.com/gorilla/websocket v1.5.3
	github.com/kayushkin/llm-bridge v0.0.0
)

replace github.com/kayushkin/llm-bridge => ../llm-bridge
