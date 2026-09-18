cd /home/euripides/projects/blockchain-platform/backend && export PATH=/usr/local/go/bin:$PATH
echo "=== loginevent.go 是否本就在 gofmt 之前就有差异？ ==="
gofmt -d internal/iam/loginevent.go | head -20
echo "=== 仅格式化本次改动的文件 ==="
gofmt -w internal/iam/session.go internal/config/config.go internal/api/authmw.go internal/api/authv1.go internal/api/bootstrap.go
gofmt -l internal/api internal/iam internal/config
go build -o bin/api ./cmd/api && echo BUILD_OK
sha256sum bin/api
echo SCRIPT_END
