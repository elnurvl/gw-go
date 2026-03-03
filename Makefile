.PHONY: build run test lint clean

build:
	go build -o bin/gateway .

run:
	go run .

test:
	go test ./...

lint:
	golangci-lint run

clean:
	rm -rf bin/
