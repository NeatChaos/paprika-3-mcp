.PHONY: build install image

TAG ?= dev
ifndef REGISTRY
REGISTRY := $(shell terraform -chdir=infra output -raw artifact_repo 2>/dev/null)
endif

build:
	go build -o bin ./cmd/...

install:
	go install ./cmd/paprika-3-mcp

# Builds and pushes $(REGISTRY)/server:$(TAG), then prints the digest reference for terraform's `image` variable.
image:
	@test -n "$(REGISTRY)" || { echo "REGISTRY is empty: pass REGISTRY=<repo>, or prefix make with GOOGLE_OAUTH_ACCESS_TOKEN=... so terraform can read the state" >&2; exit 1; }
	docker build --platform linux/amd64 --build-arg VERSION=$(TAG) -t $(REGISTRY)/server:$(TAG) .
	docker push $(REGISTRY)/server:$(TAG)
	@docker inspect --format '{{index .RepoDigests 0}}' $(REGISTRY)/server:$(TAG)
