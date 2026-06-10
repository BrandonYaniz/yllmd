GO ?= go
GOFLAGS ?=
VERSION ?= $(shell date +%y.%m.%d.00)
DIST_DIR ?= dist
GOCACHE ?= $(CURDIR)/.cache/go-build
LDFLAGS := -X main.version=$(VERSION)

.PHONY: all check-version check-release-version test race cover build dist smoke release-check clean

all: test build

check-version:
	./scripts/validate-version.sh "$(VERSION)"

check-release-version:
	./scripts/validate-version.sh "$(VERSION)" release

test:
	GOCACHE="$(GOCACHE)" $(GO) test $(GOFLAGS) ./...

race:
	GOCACHE="$(GOCACHE)" $(GO) test $(GOFLAGS) -race ./...

cover:
	GOCACHE="$(GOCACHE)" $(GO) test $(GOFLAGS) -cover ./...

build:
	mkdir -p build
	GOCACHE="$(GOCACHE)" $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o build/yllmd ./cmd/yllmd
	GOCACHE="$(GOCACHE)" $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o build/yllmctl ./cmd/yllmctl

dist: check-version
	mkdir -p "$(DIST_DIR)"
	rm -rf "$(DIST_DIR)/yllmd_$(VERSION)_darwin_amd64" "$(DIST_DIR)/yllmd_$(VERSION)_darwin_arm64" "$(DIST_DIR)/yllmd_$(VERSION)_linux_amd64" "$(DIST_DIR)/yllmd_$(VERSION)_linux_arm64" "$(DIST_DIR)/yllmd_$(VERSION)_freebsd_amd64" "$(DIST_DIR)/yllmd_$(VERSION)_freebsd_arm64"
	rm -f "$(DIST_DIR)/yllmd_$(VERSION)_darwin_amd64.tar.gz" "$(DIST_DIR)/yllmd_$(VERSION)_darwin_arm64.tar.gz" "$(DIST_DIR)/yllmd_$(VERSION)_linux_amd64.tar.gz" "$(DIST_DIR)/yllmd_$(VERSION)_linux_arm64.tar.gz" "$(DIST_DIR)/yllmd_$(VERSION)_freebsd_amd64.tar.gz" "$(DIST_DIR)/yllmd_$(VERSION)_freebsd_arm64.tar.gz" "$(DIST_DIR)/checksums_$(VERSION).txt"
	GOCACHE="$(GOCACHE)" GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o "$(DIST_DIR)/yllmd_$(VERSION)_darwin_amd64/yllmd" ./cmd/yllmd
	GOCACHE="$(GOCACHE)" GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o "$(DIST_DIR)/yllmd_$(VERSION)_darwin_amd64/yllmctl" ./cmd/yllmctl
	GOCACHE="$(GOCACHE)" GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o "$(DIST_DIR)/yllmd_$(VERSION)_darwin_arm64/yllmd" ./cmd/yllmd
	GOCACHE="$(GOCACHE)" GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o "$(DIST_DIR)/yllmd_$(VERSION)_darwin_arm64/yllmctl" ./cmd/yllmctl
	GOCACHE="$(GOCACHE)" GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o "$(DIST_DIR)/yllmd_$(VERSION)_linux_amd64/yllmd" ./cmd/yllmd
	GOCACHE="$(GOCACHE)" GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o "$(DIST_DIR)/yllmd_$(VERSION)_linux_amd64/yllmctl" ./cmd/yllmctl
	GOCACHE="$(GOCACHE)" GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o "$(DIST_DIR)/yllmd_$(VERSION)_linux_arm64/yllmd" ./cmd/yllmd
	GOCACHE="$(GOCACHE)" GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o "$(DIST_DIR)/yllmd_$(VERSION)_linux_arm64/yllmctl" ./cmd/yllmctl
	GOCACHE="$(GOCACHE)" GOOS=freebsd GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o "$(DIST_DIR)/yllmd_$(VERSION)_freebsd_amd64/yllmd" ./cmd/yllmd
	GOCACHE="$(GOCACHE)" GOOS=freebsd GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o "$(DIST_DIR)/yllmd_$(VERSION)_freebsd_amd64/yllmctl" ./cmd/yllmctl
	GOCACHE="$(GOCACHE)" GOOS=freebsd GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o "$(DIST_DIR)/yllmd_$(VERSION)_freebsd_arm64/yllmd" ./cmd/yllmd
	GOCACHE="$(GOCACHE)" GOOS=freebsd GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o "$(DIST_DIR)/yllmd_$(VERSION)_freebsd_arm64/yllmctl" ./cmd/yllmctl
	cp README.md LICENSE config.example.yaml "$(DIST_DIR)/yllmd_$(VERSION)_darwin_amd64/"
	cp README.md LICENSE config.example.yaml "$(DIST_DIR)/yllmd_$(VERSION)_darwin_arm64/"
	cp README.md LICENSE config.example.yaml "$(DIST_DIR)/yllmd_$(VERSION)_linux_amd64/"
	cp README.md LICENSE config.example.yaml "$(DIST_DIR)/yllmd_$(VERSION)_linux_arm64/"
	cp README.md LICENSE config.example.yaml "$(DIST_DIR)/yllmd_$(VERSION)_freebsd_amd64/"
	cp README.md LICENSE config.example.yaml "$(DIST_DIR)/yllmd_$(VERSION)_freebsd_arm64/"
	printf '%s\n' "$(VERSION)" > "$(DIST_DIR)/yllmd_$(VERSION)_darwin_amd64/VERSION"
	printf '%s\n' "$(VERSION)" > "$(DIST_DIR)/yllmd_$(VERSION)_darwin_arm64/VERSION"
	printf '%s\n' "$(VERSION)" > "$(DIST_DIR)/yllmd_$(VERSION)_linux_amd64/VERSION"
	printf '%s\n' "$(VERSION)" > "$(DIST_DIR)/yllmd_$(VERSION)_linux_arm64/VERSION"
	printf '%s\n' "$(VERSION)" > "$(DIST_DIR)/yllmd_$(VERSION)_freebsd_amd64/VERSION"
	printf '%s\n' "$(VERSION)" > "$(DIST_DIR)/yllmd_$(VERSION)_freebsd_arm64/VERSION"
	cp -R docs packaging "$(DIST_DIR)/yllmd_$(VERSION)_darwin_amd64/"
	cp -R docs packaging "$(DIST_DIR)/yllmd_$(VERSION)_darwin_arm64/"
	cp -R docs packaging "$(DIST_DIR)/yllmd_$(VERSION)_linux_amd64/"
	cp -R docs packaging "$(DIST_DIR)/yllmd_$(VERSION)_linux_arm64/"
	cp -R docs packaging "$(DIST_DIR)/yllmd_$(VERSION)_freebsd_amd64/"
	cp -R docs packaging "$(DIST_DIR)/yllmd_$(VERSION)_freebsd_arm64/"
	cd "$(DIST_DIR)" && tar -czf "yllmd_$(VERSION)_darwin_amd64.tar.gz" "yllmd_$(VERSION)_darwin_amd64"
	cd "$(DIST_DIR)" && tar -czf "yllmd_$(VERSION)_darwin_arm64.tar.gz" "yllmd_$(VERSION)_darwin_arm64"
	cd "$(DIST_DIR)" && tar -czf "yllmd_$(VERSION)_linux_amd64.tar.gz" "yllmd_$(VERSION)_linux_amd64"
	cd "$(DIST_DIR)" && tar -czf "yllmd_$(VERSION)_linux_arm64.tar.gz" "yllmd_$(VERSION)_linux_arm64"
	cd "$(DIST_DIR)" && tar -czf "yllmd_$(VERSION)_freebsd_amd64.tar.gz" "yllmd_$(VERSION)_freebsd_amd64"
	cd "$(DIST_DIR)" && tar -czf "yllmd_$(VERSION)_freebsd_arm64.tar.gz" "yllmd_$(VERSION)_freebsd_arm64"
	cd "$(DIST_DIR)" && shasum -a 256 "yllmd_$(VERSION)_darwin_amd64.tar.gz" "yllmd_$(VERSION)_darwin_arm64.tar.gz" "yllmd_$(VERSION)_linux_amd64.tar.gz" "yllmd_$(VERSION)_linux_arm64.tar.gz" "yllmd_$(VERSION)_freebsd_amd64.tar.gz" "yllmd_$(VERSION)_freebsd_arm64.tar.gz" > "checksums_$(VERSION).txt"

smoke:
	GOCACHE="$(GOCACHE)" ./scripts/smoke-fake.sh

release-check: check-release-version test race smoke dist

clean:
	rm -rf "$(DIST_DIR)" build .cache yllmd yllmctl
