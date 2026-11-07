.PHONY: build test vet eval validate check-boundaries run clean web

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

run: build
	./$(BINARY) serve --env .env

web:
	cd web && npm ci && npm run build

clean:
	rm -rf bin web/dist
