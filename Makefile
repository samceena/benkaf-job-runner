.PHONY: run test test-unit test-integration test-race ci

run:
	go run cmd/control-plane/main.go

test:
	go test -v ./...

test-unit:
	go test -v ./internal/job ./internal/storage

test-integration:
	go test -v ./cmd/...

test-race:
	go test -v -race ./...

# github actions CI
ci: test-race
	go vet ./...
	go build ./...
