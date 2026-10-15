ENVTEST_K8S ?= 1.30.x
IMG ?= docker.io/iracic82/bnk-runai-tenancy:0.1.0
GOBIN := $(shell go env GOPATH)/bin

.PHONY: build test envtest vet image push

build:
	go build -o bin/manager ./cmd/manager

vet:
	go vet ./...

test: vet
	go test ./internal/...

# Real kube-apiserver at the BNK 2.4 cluster version, with the Run:ai kai.resources CRDs.
envtest:
	@test -x $(GOBIN)/setup-envtest || go install sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.23
	KUBEBUILDER_ASSETS="$$($(GOBIN)/setup-envtest use $(ENVTEST_K8S) -p path)" go test ./test/envtest/ -count=1 -v

image:
	docker build --platform linux/amd64 -t $(IMG) .

push: image
	docker push $(IMG)
