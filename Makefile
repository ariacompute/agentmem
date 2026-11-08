.PHONY: build test vet eval validate check-boundaries check-trust-store run clean web web-audit

BINARY := bin/agentmem
PKG := ./...

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

web:
	cd web && bun install --frozen-lockfile && bun run build

# Source-level rendering/slop check (no browser needed). The full element-level
# pass (contrast, tiny-text) needs a running app + local Chrome/Edge:
#   cd web && bun run dev   # then: bun run audit:live
web-audit:
	cd web && bun x impeccable@latest detect src index.html

clean:
	rm -rf bin web/dist
