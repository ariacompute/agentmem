.PHONY: build test vet eval validate check-boundaries check-trust-store run clean web web-verify web-audit e2e

BINARY := bin/agentmem
PKG := ./...

# The console is compiled in: internal/webui/dist is the Vite output and is
# committed, so `go build` and `go test ./...` stay offline and bun-free.
build:
	go build -o $(BINARY) ./cmd/agentmem

test:
	go test $(PKG) -count=1

vet:
	go vet $(PKG)

eval:
	go run ./cmd/agentmem eval --dataset testdata/eval.jsonl

validate:
	go run ./cmd/agentmem validate --env .env.example

# SDK boundaries are declared as data in plugin.yaml, never as code comments.
check-boundaries:
	@if grep -RIn --exclude-dir=.git --exclude-dir=bin --exclude-dir=dist --exclude=Makefile -e 'TODO'"(real)" .; then \
		echo "SDK boundary annotations must live in plugin.yaml sdk_boundary"; exit 1; \
	else \
		echo "SDK boundaries are declarative"; \
	fi
	@go test ./internal/plugins/... -count=1

# Offline trust-store gate for the eMudhra/emSign CA key compromise (2026-10).
# Fingerprints are matched locally; online probes produce false positives
# behind forward/intercepting proxies and are deliberately not used.
check-trust-store:
	@./scripts/check-emudhra.sh

run: build
	./$(BINARY) serve --env .env

# Vite writes straight into the embedded package (web/vite.config.js), so a
# rebuild replaces internal/webui/dist in place and `make build` picks it up.
web:
	cd web && bun install --frozen-lockfile && bun run build

# Drift check for a machine that has bun: rebuild and fail if the committed
# embed output differs from what the current sources produce.
web-verify:
	cd web && bun install --frozen-lockfile && bun run build
	@if ! git diff --quiet -- internal/webui/dist; then \
		echo "internal/webui/dist is stale: rebuild and commit it (make web)"; \
		git --no-pager diff --stat -- internal/webui/dist; exit 1; \
	else \
		echo "embedded console matches the sources"; \
	fi

# Source-level rendering/slop check (no browser needed). The CLI version is
# pinned by web/package.json's devDependency, so `bun run audit` resolves the
# lockfile copy instead of drifting with @latest. The full element-level pass
# (contrast, tiny-text) needs a running app + local Chrome/Edge:
#   cd web && bun run dev   # then: bun run audit:live
web-audit:
	cd web && bun run audit

# 用户视角黑盒验收：编译真实二进制、驱动 CLI 与真实 HTTP 服务。
# 全程离线（GOPROXY=off），不需要 Postgres、网络或凭据；临时产物自动回收。
# 例：make e2e / make e2e E2E_ARGS='--no-gates' / make e2e E2E_ARGS='--filter ^H --keep'
e2e:
	./scripts/e2e.sh $(E2E_ARGS)

# internal/webui/dist is a committed build input (`//go:embed`), never cleaned:
# removing it would break `go build` for everyone without bun.
clean:
	rm -rf bin
