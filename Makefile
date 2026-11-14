.PHONY: run test

run:
	@set -a; . ./.env.dev; CGO_ENABLED=0 go run -buildvcs=false ./cmd/wcs-guide

test:
	CGO_ENABLED=0 go test ./...
