cd /home/euripides/projects/blockchain-platform/backend && export PATH=/usr/local/go/bin:$PATH
gofmt -l internal/api internal/iam internal/config
echo "--- vet ---"
go vet ./internal/... 2>&1 | head -30
echo "--- build ---"
go build -o bin/api ./cmd/api 2>&1 | head -30 && echo BUILD_OK
echo SCRIPT_END
