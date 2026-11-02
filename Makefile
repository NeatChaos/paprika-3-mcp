.PHONY: build install image

TAG ?= dev
REGISTRY ?= $(shell terraform -chdir=infra output -raw artifact_repo)

build:
	go build -o bin ./cmd/...

install:
	go install ./cmd/paprika-3-mcp

# Builds and pushes $(REGISTRY)/server:$(TAG), then prints the digest reference for terraform's `image` variable.
image:
	docker build --platform linux/amd64 --build-arg VERSION=$(TAG) -t $(REGISTRY)/server:$(TAG) .
	docker push $(REGISTRY)/server:$(TAG)
	@docker inspect --format '{{index .RepoDigests 0}}' $(REGISTRY)/server:$(TAG)
