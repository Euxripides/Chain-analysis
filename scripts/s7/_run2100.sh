set -e
cd /home/euripides/projects/blockchain-platform/backend && export PATH=/usr/local/go/bin:$PATH
gofmt -w internal/config/config.go cmd/migrate/main.go
go vet ./internal/config/ ./cmd/migrate/ ./internal/api/ 2>&1 | head -20
go build -o bin/api ./cmd/api && go build -o bin/migrate ./cmd/migrate && go build -o bin/worker ./cmd/worker && go build -o bin/admin-init ./cmd/admin-init && go build -o bin/admin-fixture ./cmd/admin-fixture
echo BUILD_OK
sha256sum bin/api bin/migrate bin/worker
echo SCRIPT_END
