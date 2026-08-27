# go-multi-binary — developer entrypoints.
# Heavy logic lives in build.py and the Go/pytest tests; these are thin wrappers.

.PHONY: all build test unit determinism qemu-user e2e clean fmt vet

all: build

## build: reproducible multi-arch build -> dist/
build:
	uv run python build.py

## test: everything that runs without special privileges
test: unit determinism

## unit: Go unit tests + vet
unit:
	go vet ./...
	go test ./...

## determinism: build twice, prove byte-identical + reconstruct law (real binaries)
determinism:
	uv run --with pytest python -m pytest test/determinism_test.py -v

## qemu-user: run each arch's canonical under QEMU user-mode (needs binfmt/qemu-user)
qemu-user:
	uv run --with pytest python -m pytest test/exec_qemu_user_test.py -v

## e2e: full SSH teleport demo under system emulation (see emulation/system/)
e2e:
	uv run --with pytest python -m pytest test/teleport_e2e_test.py -v

## clean: remove build outputs
clean:
	rm -rf dist tmp

fmt:
	gofmt -w .

vet:
	go vet ./...
