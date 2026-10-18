# Reup is a single Go module; these targets wrap the commands in AGENTS.md.

GO ?= go
BINARY ?= reup

.PHONY: build test vet fmt lint check clean

# build: compile the reup binary into ./$(BINARY)
build:
	$(GO) build -o $(BINARY) .

# test: run the full test suite
test:
	$(GO) test ./...

# vet: run go vet over every package
vet:
	$(GO) vet ./...

# fmt: format the tree in place
fmt:
	$(GO) fmt ./...

# lint: static checks (currently go vet)
lint: vet

# check: everything AGENTS.md asks for before submitting
check: fmt lint test
	git diff --check

# clean: remove build output
clean:
	rm -f $(BINARY)
