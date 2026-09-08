.PHONY: fmt test vet installer-check verify demo codex-smoke trae-smoke build-tools build-realyctl codex-capability-smoke server-up server-down db-up db-down postgres-test clean

fmt:
	GOCACHE=/private/tmp/realy-go-cache go fmt ./...

test:
	GOCACHE=/private/tmp/realy-go-cache go test ./...

vet:
	GOCACHE=/private/tmp/realy-go-cache go vet ./...

installer-check:
	sh -n ./install.sh

verify: fmt vet test installer-check

demo:
	GOCACHE=/private/tmp/realy-go-cache go run ./examples/multica

codex-smoke:
	GOCACHE=/private/tmp/realy-go-cache go run ./cmd/realy-codex-smoke

trae-smoke:
	GOCACHE=/private/tmp/realy-go-cache go run ./cmd/realy-trae-smoke

build-tools:
	mkdir -p bin
	GOCACHE=/private/tmp/realy-go-cache go build -o bin/realy-tool ./cmd/realy-tool
	GOCACHE=/private/tmp/realy-go-cache go build -o bin/realy-example-capability ./examples/capability-cli

build-realyctl:
	mkdir -p bin
	GOCACHE=/private/tmp/realy-go-cache go build -o bin/realyctl ./cmd/realyctl

codex-capability-smoke: build-tools
	GOCACHE=/private/tmp/realy-go-cache go run ./cmd/realy-codex-capability-smoke -tool-dir ./bin -binding ./bin/realy-example-capability

server-up:
	docker compose up -d --build --wait

server-down:
	docker compose down

db-up:
	POSTGRES_PASSWORD=realy REALY_HOST_TOKEN=local-host REALY_NODE_TOKEN=local-node docker compose up -d --wait postgres

db-down:
	POSTGRES_PASSWORD=realy REALY_HOST_TOKEN=local-host REALY_NODE_TOKEN=local-node docker compose stop postgres

postgres-test:
	REALY_TEST_DATABASE_URL='postgres://realy:realy@127.0.0.1:55432/realy?sslmode=disable' GOCACHE=/private/tmp/realy-go-cache go test ./controlplane/postgres -count=1 -v

clean:
	rm -f bin/realy-tool bin/realy-example-capability bin/realyctl
