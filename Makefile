# nudge build — requires Go 1.26+. `make build` for the current platform,
# `make build-all` cross-compiles all release targets into dist/.

VERSION ?= 0.1.0
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet eval build-all clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o nudge$(shell go env GOEXE) ./cmd/nudge

test:
	go test ./...

vet:
	go vet ./...

# needs a local model server running
eval:
	NUDGE_EVAL=1 go test ./eval -v -timeout 30m

build-all: clean
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/nudge_windows_amd64.exe ./cmd/nudge
	GOOS=windows GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/nudge_windows_arm64.exe ./cmd/nudge
	GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/nudge_linux_amd64 ./cmd/nudge
	GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/nudge_linux_arm64 ./cmd/nudge
	GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/nudge_darwin_amd64 ./cmd/nudge
	GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/nudge_darwin_arm64 ./cmd/nudge

clean:
	rm -rf dist
