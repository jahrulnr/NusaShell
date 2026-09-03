.PHONY: build dev test test-race test-sdl2 test-race-sdl2 vet vet-sdl2 fmt clean install run-sdl2

GO ?= go
SDL2_TAG ?= sdl2
BINARY := bin/pets

build:
	$(GO) build -tags $(SDL2_TAG) -o $(BINARY) ./cmd/pets

dev: build
	./$(BINARY) --assets assets/pets

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

test-sdl2:
	$(GO) test -tags $(SDL2_TAG) ./...

test-race-sdl2:
	$(GO) test -race -tags $(SDL2_TAG) ./...

vet:
	$(GO) vet ./...

vet-sdl2:
	$(GO) vet -tags $(SDL2_TAG) ./...

fmt:
	$(GO) fmt ./...
	@gofmt -l . | grep -q . && { echo "gofmt drift in:"; gofmt -l .; exit 1; } || echo "gofmt clean"

clean:
	rm -rf bin

install: build
	install -D $(BINARY) $(DESTDIR)/usr/local/bin/pets

run-sdl2: build
	./$(BINARY) --assets assets/pets
