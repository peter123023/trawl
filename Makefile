BIN := bin/trawl
PKG := ./cmd/trawl

.PHONY: build run test fmt vet tidy clean web-build web-dev

build:
	go build -o $(BIN) $(PKG)

web-build:
	cd web && npm install --no-audit --no-fund && npm run build

web-dev:
	cd web && npm run dev

run: build
	./$(BIN)

test:
	go test ./...

fmt:
	gofmt -w cmd internal

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf bin
