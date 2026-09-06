# go-multi-binary — developer entrypoints.
# Everything is Go: the build orchestrator is cmd/fatbuild, the tests are Go
# tests (some behind build tags). These targets are thin wrappers; no Python.

.PHONY: all build test unit determinism qemu-user e2e clean fmt vet

all: build

## build: reproducible multi-arch build -> dist/
build:
	go run ./cmd/fatbuild

## test: everything that runs without special privileges
test: unit determinism

## unit: Go unit tests + vet
unit:
	go vet ./...
	go test ./...

## determinism: build twice, prove byte-identical + reconstruct law (real binaries)
## Limit the arch set to go faster, e.g. FATBUILD_TEST_ARCHES=amd64,arm64
determinism:
	go test -tags reprobuild -run TestReproducibleBuilds -v ./internal/fatbuild/

## qemu-user: run each arch's canonical under QEMU user-mode (needs qemu-user)
qemu-user:
	go test -tags qemu -v ./emulation/user/

## e2e: full SSH teleport demo under system emulation (see emulation/system/)
e2e:
	go test -tags e2e -v ./emulation/system/

## clean: remove build outputs
clean:
	rm -rf dist tmp

fmt:
	gofmt -w .

vet:
	go vet ./...
