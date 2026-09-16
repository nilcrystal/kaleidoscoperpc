.PHONY: test race vet check fuzz cover bench

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

check: vet test race

fuzz:
	./scripts/fuzz.sh

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

bench:
	go test -run='^$$' -bench=. -benchmem ./...
