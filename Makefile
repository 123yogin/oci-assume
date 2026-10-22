.PHONY: build test shell-test install-test install-test-ps

build:
	go build -o oci-assume ./cmd/oci-assume

test:
	go test ./... -race

shell-test:
	test/shells/run.sh

install-test:
	test/install/test.sh

install-test-ps:
	pwsh -NoProfile -File test/install/test.ps1
