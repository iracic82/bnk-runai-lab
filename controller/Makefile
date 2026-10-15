ENVTEST_K8S ?= 1.30.x
IMG ?= docker.io/iracic82/bnk-runai-tenancy:0.1.8
IMG_METER ?= docker.io/iracic82/bnk-runai-meter:0.1.2
IMG_MCP ?= docker.io/iracic82/bnk-tenancy-mcp:0.1.0
GOBIN := $(shell go env GOPATH)/bin

.PHONY: build test envtest vet image push meter-image meter-push mcp-image mcp-push

build:
	go build -o bin/manager ./cmd/manager
	go build -o bin/meter ./cmd/meter
	go build -o bin/bnk-tenancy-mcp ./cmd/mcp

vet:
	go vet ./...

test: vet
	go test ./internal/... ./cmd/...

# Real kube-apiserver at the BNK 2.4 cluster version, with the Run:ai kai.resources CRDs.
envtest:
	@test -x $(GOBIN)/setup-envtest || go install sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.23
	KUBEBUILDER_ASSETS="$$($(GOBIN)/setup-envtest use $(ENVTEST_K8S) -p path)" go test ./test/envtest/ -count=1 -v

image:
	docker build --platform linux/amd64 -t $(IMG) .

push: image
	docker push $(IMG)

meter-image:
	docker build --platform linux/amd64 -f Dockerfile.meter -t $(IMG_METER) .

meter-push: meter-image
	docker push $(IMG_METER)

# Stateless MCP server with read-only BNK tenant tools (config/mcp.yaml).
mcp-image:
	docker build --platform linux/amd64 -f Dockerfile.mcp -t $(IMG_MCP) .

mcp-push: mcp-image
	docker push $(IMG_MCP)
