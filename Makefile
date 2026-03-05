.PHONY: build run test test-integration test-docker test-docker-integration coverage lint clean

build:
	go build -o bin/gateway .

run:
	go run .

test:
	go test ./... -count=1 -timeout 60s

test-integration:
	go test -tags=integration ./... -count=1 -timeout 60s

coverage:
	go test ./... -coverprofile=cover.out -count=1
	go tool cover -func=cover.out
	go tool cover -html=cover.out

lint:
	golangci-lint run

clean:
	rm -rf bin/
