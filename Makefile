run:
	go run cmd/control-plane/main.go

test-unit:
	go test ./internal/job
	go test ./internal/storage/memory_test.go