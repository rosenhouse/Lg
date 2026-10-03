.PHONY: build test lint

build:
	go build -o bin/lg ./cmd/lg

test:
	go tool ginkgo -r -p --race --randomize-all --randomize-suites --fail-on-pending --fail-on-empty --keep-going --label-filter='!live && !systemd && !scale'

lint:
	golangci-lint run ./... && GOOS=darwin golangci-lint run ./...
