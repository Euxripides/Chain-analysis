cd /home/euripides/projects/blockchain-platform/backend && export PATH=/usr/local/go/bin:$PATH
gofmt -l internal/api/server.go internal/api/actions.go internal/api/authmw.go internal/api/sprint6_provider.go
go vet ./internal/api/ 2>&1 | head -20
go build -o bin/api ./cmd/api 2>&1 | head -30 && echo BUILD_OK
echo SCRIPT_END
