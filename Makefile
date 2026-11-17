# Static binaries only (pure Go, no cgo).
BINDIR := builds
LDFLAGS := -s -w

.DEFAULT_GOAL := build

# `make` / `make build`: current OS, binary in this directory.
ifeq ($(shell go env GOOS),windows)
LOCAL_BIN := gitcng.exe
else
LOCAL_BIN := gitcng
endif

.PHONY: build all linux windows darwin clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(LOCAL_BIN) .

all: linux windows darwin

linux:
	@mkdir -p $(BINDIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINDIR)/gitcng .

windows:
	@mkdir -p $(BINDIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINDIR)/gitcng_windows .

darwin:
	@mkdir -p $(BINDIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BINDIR)/gitcng_apple .

clean:
	rm -f gitcng gitcng.exe
	rm -rf $(BINDIR)