.PHONY: build test shell-test install-test

build:
	go build -o oci-assume ./cmd/oci-assume

test:
	go test ./... -race

shell-test:
	test/shells/run.sh

install-test:
	test/install/test.sh
