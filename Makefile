.PHONY: check fmt vet test agents bench

check: fmt vet test agents

fmt:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

test:
	go test -race -count=1 ./...

# Tool-specific agent files must stay one-line imports of AGENTS.md.
agents:
	@./scripts/check-agent-files.sh

bench:
	cd bench && GOFLAGS=-mod=mod go test -run xxx -bench . -benchmem -count 3
