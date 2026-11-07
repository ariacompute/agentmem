.PHONY: build test vet eval validate run clean web

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
	go run ./cmd/agentmem validate --config config.yaml

run: build
	./$(BINARY) serve --config config.yaml

web:
	cd web && npm ci && npm run build

clean:
	rm -rf bin web/dist
