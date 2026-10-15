.PHONY: fmt test race integration vet build compose-up compose-down validate

fmt:
	gofmt -w cmd internal migrations tests

test:
	go test ./...

race:
	go test -race ./...

integration:
	go test -tags=integration -v -timeout=10m ./tests/integration

vet:
	go vet ./...

build:
	go build ./cmd/gateway ./cmd/worker

compose-up:
	docker compose up --build

compose-down:
	docker compose down -v

validate: fmt vet test race
	docker compose config --quiet
