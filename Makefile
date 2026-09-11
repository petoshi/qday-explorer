.PHONY: build test

build:
	go build -trimpath -ldflags='-s -w' -o build/qday-explorer .

test:
	go test ./...
