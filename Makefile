.PHONY: build install

build: 
	go build -o bin ./cmd/...

install:
	go install ./cmd/paprika-3-mcp