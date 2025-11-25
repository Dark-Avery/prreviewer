APP=prreviewer
BIN=bin/$(APP)

.PHONY: build run test tidy fmt lint integration loadtest loadtest-env loadtest-run loadtest-down

build:
	go build -o $(BIN) ./cmd/prreviewer

run:
	go run ./cmd/prreviewer

test:
	go test ./...

coverage:
	PACKAGES=$$(go list ./... | grep -v '/tools'); \
	RUN_INTEGRATION=1 go test -cover $$PACKAGES

integration:
	go test ./integration -run TestFullFlow -count=1

tidy:
	go mod tidy

fmt:
	gofmt -w cmd internal integration tools *.go
	goimports -w cmd internal

lint:
	GOTOOLCHAIN=go1.24.4 GOBIN=$(PWD)/bin go install github.com/golangci/golangci-lint/cmd/golangci-lint@master
	GOTOOLCHAIN=go1.24.4 $(PWD)/bin/golangci-lint run 

loadtest:
	@echo "Starting loadtest environment..."
	docker compose -f deployments/docker-compose.loadtest.yml up -d --build
	@echo "Waiting for app to be ready..."
	docker compose -f deployments/docker-compose.loadtest.yml exec app_loadtest sh -c 'for i in $$(seq 1 30); do if wget -qO- http://localhost:8080/health >/dev/null 2>&1; then exit 0; fi; sleep 1; done; exit 1'
	@echo "Running load test..."
	BASE_URL=http://localhost:8080 WARMUP=1s DURATION=5s CONCURRENCY=20 RESET_DB=1 docker compose -f deployments/docker-compose.loadtest.yml exec app_loadtest /app/loadtest
	@echo "Stopping loadtest environment..."
	docker compose -f deployments/docker-compose.loadtest.yml down -v
