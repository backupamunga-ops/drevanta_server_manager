GO ?= go
NPM ?= npm

.PHONY: all build web-build go-build test vet fmt-check run cross-linux-amd64 clean

all: build

## web-build: install JS deps and build the SvelteKit static dashboard
web-build:
	cd web && $(NPM) install && $(NPM) run build

## go-build: compile drevantad with the embedded dashboard
go-build:
	$(GO) build -trimpath -o bin/drevantad ./cmd/drevantad

## build: dashboard first (go:embed needs web/build), then the daemon
build: web-build go-build

## test: run Go unit tests
test:
	$(GO) test ./...

## vet: go vet + gofmt check
vet:
	$(GO) vet ./...
	@out="$$(gofmt -l . )"; if [ -n "$$out" ]; then echo "unformatted files:"; echo "$$out"; exit 1; fi

## run: local dev run against a throwaway state dir
run:
	$(GO) run ./cmd/drevantad --addr 127.0.0.1:8080 --data-dir /tmp/drevanta-dev

## cross-linux-amd64: static release binary for the VPS
cross-linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags='-s -w' -o dist/drevantad-linux-amd64 ./cmd/drevantad

clean:
	rm -rf bin dist

## installer-test: run the deploy script test harness against a fresh build
installer-test:
	$(GO) build -o /tmp/drevantad-installer-test ./cmd/drevantad
	bash deploy/tests/test_install.sh /tmp/drevantad-installer-test

## shellcheck: lint the deploy scripts (requires shellcheck)
shellcheck:
	shellcheck deploy/install.sh deploy/rollback.sh deploy/healthgate.sh deploy/drevanta.sh deploy/bootstrap.sh deploy/tests/test_install.sh
