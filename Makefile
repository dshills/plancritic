.PHONY: build test lint install clean

build:
	go build -o plancritic ./cmd/plancritic

test:
	go test ./...

lint:
	golangci-lint run

install:
	go install ./cmd/plancritic

clean:
	rm -f plancritic
