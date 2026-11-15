.PHONY: generate test vet check

# Regenerates internal/store/db from sql/queries and sql/schema.
# sqlc is a dev tool. The Dockerfile builds the committed Go and does not run this.
generate:
	sqlc generate

test:
	CGO_ENABLED=0 go test ./...

vet:
	CGO_ENABLED=0 go vet ./...

check: vet test
