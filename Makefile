NAME=go-apperror

.PHONY: fmt vet lint test cover bench fuzz tidy check tags

fmt:
	gofmt -s -w .

vet:
	go vet ./...

lint:
	go run honnef.co/go/tools/cmd/staticcheck@latest ./...

test:
	go test -race -count=1 ./...

cover:
	go test -race -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

bench:
	go test -run=^$$ -bench=. -benchmem ./...

# FUZZTIME=5m make fuzz
FUZZTIME ?= 60s
fuzz:
	go test -run=^$$ -fuzz=FuzzParseReason -fuzztime=$(FUZZTIME) .

tidy:
	go mod tidy -diff

# Everything CI should run before a release.
check: tidy vet lint test

tags: check
	@bash -c ' \
		set -e; \
		version=$$(tr -d "[:space:]" < "$(CURDIR)/version" 2>/dev/null || echo "0.0.0"); \
		tag=v$$version; \
		echo "→ tag: $$tag"; \
		if [[ -n $$(git status --porcelain --untracked-files=no) ]]; then \
			echo "❌ Working tree has uncommitted changes"; exit 1; \
		fi; \
		if [[ -n $$(git tag -l "$$tag") ]]; then \
			echo "⚠️  Tag $$tag already exists"; exit 0; \
		fi; \
		git tag -a "$$tag" -m "Release $$version"; \
		git push origin "$$tag" -o ci.skip; \
		echo "✅ Tagged and pushed $$tag"; \
	'
