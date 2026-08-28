BIN := 0xg0.st
VERSION ?= dev
DIST_DIR ?= dist
LDFLAGS ?= -s -w
PLATFORMS := linux/amd64 linux/arm64 freebsd/amd64 freebsd/arm64

.PHONY: build test clean release

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) .

test:
	go test ./...

clean:
	rm -rf $(DIST_DIR) $(BIN)

release:
	@set -eu; \
	mkdir -p $(DIST_DIR); \
	if [ -n "${GOOS:-}" ] && [ -n "${GOARCH:-}" ]; then \
		platforms="$$GOOS/$$GOARCH"; \
	else \
		platforms="$(PLATFORMS)"; \
	fi; \
	for platform in $$platforms; do \
		os=$${platform%/*}; \
		arch=$${platform#*/}; \
		outdir="$(DIST_DIR)/$${os}-$${arch}"; \
		archive="$(DIST_DIR)/$(BIN)-$(VERSION)-$${os}-$${arch}.tar.gz"; \
		mkdir -p "$${outdir}"; \
		CGO_ENABLED=0 GOOS=$${os} GOARCH=$${arch} go build -trimpath -ldflags="$(LDFLAGS)" -o "$${outdir}/$(BIN)" .; \
		tar -C "$${outdir}" -czf "$${archive}" $(BIN); \
		sha256sum "$${archive}" > "$${archive}.sha256"; \
	done
