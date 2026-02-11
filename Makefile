SHELL := /bin/bash

GO ?= go
GO_TAGS ?= sqlite_fts5
BINARY := agent-trace
CMD_PATH := ./cmd/agent-trace
INSTALL_DIR ?= $(HOME)/.local/bin
INSTALL_PATH := $(INSTALL_DIR)/$(BINARY)
LOCAL_BIN := ./bin/$(BINARY)

.PHONY: help build run run-reindex install reindex rebuild test

help:
	@echo "Targets:"
	@echo "  make build         Build local binary at $(LOCAL_BIN)"
	@echo "  make run           Run from source with FTS5 tag"
	@echo "  make run-reindex   Run from source with --reindex"
	@echo "  make install       Build and install to $(INSTALL_PATH)"
	@echo "  make reindex       Run installed binary with --reindex"
	@echo "  make rebuild       Install, then reindex"
	@echo "  make test          Run go test ./..."

build:
	@mkdir -p ./bin
	$(GO) build --tags "$(GO_TAGS)" -o "$(LOCAL_BIN)" $(CMD_PATH)

run:
	$(GO) run --tags "$(GO_TAGS)" $(CMD_PATH)

run-reindex:
	$(GO) run --tags "$(GO_TAGS)" $(CMD_PATH) --reindex

install:
	@mkdir -p "$(INSTALL_DIR)"
	$(GO) build --tags "$(GO_TAGS)" -o "$(INSTALL_PATH)" $(CMD_PATH)
	@echo "Installed $(BINARY) to $(INSTALL_PATH)"
	@echo "Tip: if your shell still resolves an old command, run: hash -r"

reindex:
	"$(INSTALL_PATH)" --reindex

rebuild: install reindex

test:
	$(GO) test ./...
