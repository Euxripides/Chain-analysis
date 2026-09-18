$ErrorActionPreference='Continue'
$R='E:\Code\远程虚拟机\harness\blockchain-platform'
$EV='E:\Code\远程虚拟机\harness\blockchain-platform\acceptance-evidence\dual-system-v1\sprint7\security-evidence-20260912T223257Z'
$DEP=Join-Path $EV 'deps'
New-Item -ItemType Directory -Force -Path $DEP | Out-Null
$CMDS=Join-Path $DEP 'commands.log'
function Log($m){ Write-Output $m; Add-Content -Path $CMDS -Value $m }

Log "=== TOOL VERSIONS ==="
Log ("node: " + (node --version 2>&1))
Log ("pnpm: " + (pnpm --version 2>&1))
Log ("go:   " + (go version 2>&1))
Log ("cwd:  $R")

Log ""
Log "=== PRE-SCAN INTEGRITY FINGERPRINTS (prove read-only) ==="
$targets = @('backend\go.mod','backend\go.sum','frontend\pnpm-lock.yaml','frontend\package.json','frontend\apps\admin-console\package.json','frontend\apps\user-console\package.json','frontend\packages\api-client\package.json','frontend\packages\design-tokens\package.json','frontend\packages\ui\package.json')
foreach($t in $targets){ $p=Join-Path $R $t; if(Test-Path $p){ $h=(Get-FileHash $p -Algorithm SHA256).Hash; Log ("PRE  " + $h + "  " + $t) } else { Log ("PRE  MISSING  " + $t) } }

Log ""
Log "=== [1] pnpm audit --json (frontend workspace) ==="
Push-Location (Join-Path $R 'frontend')
$env:NO_COLOR='1'
$auditJson = Join-Path $DEP 'pnpm-audit.json'
$auditErr  = Join-Path $DEP 'pnpm-audit.stderr.txt'
& pnpm audit --json 1> $auditJson 2> $auditErr
Log ("pnpm-audit EXIT=" + $LASTEXITCODE)
Log "--- stderr ---"
if(Test-Path $auditErr){ Get-Content $auditErr | ForEach-Object { Log $_ } }
Log ("--- json size: " + (Get-Item $auditJson -ErrorAction SilentlyContinue).Length + " bytes ---")
Pop-Location

Log ""
Log "=== [2] pnpm licenses list --json ==="
Push-Location (Join-Path $R 'frontend')
$lic = Join-Path $DEP 'pnpm-licenses.json'
& pnpm licenses list --json 1> $lic 2>&1
Log ("pnpm-licenses EXIT=" + $LASTEXITCODE + "  size=" + (Get-Item $lic -ErrorAction SilentlyContinue).Length)
Pop-Location

Log ""
Log "=== [3] go list -m all ==="
Push-Location (Join-Path $R 'backend')
$env:GOFLAGS='-mod=readonly'
$golist = Join-Path $DEP 'go-list-m-all.txt'
$golistErr = Join-Path $DEP 'go-list-m-all.stderr.txt'
& go list -m all 1> $golist 2> $golistErr
Log ("go-list-m-all EXIT=" + $LASTEXITCODE)
if(Test-Path $golist){ Log ("module lines: " + (Get-Content $golist | Measure-Object -Line).Lines) }
if(Test-Path $golistErr){ Get-Content $golistErr -TotalCount 10 | ForEach-Object { Log $_ } }
Pop-Location

Log ""
Log "=== [4] govulncheck install attempt ==="
$existing = (Get-Command govulncheck -ErrorAction SilentlyContinue).Source
Log ("govulncheck pre-present: " + $existing)
$inst = Join-Path $DEP 'install-govulncheck.txt'
& go install golang.org/x/vuln/cmd/govulncheck@latest 1> $inst 2>&1
Log ("go install EXIT=" + $LASTEXITCODE)
if(Test-Path $inst){ Get-Content $inst | Select-Object -Last 12 | ForEach-Object { Log $_ } }
$gv = Join-Path (go env GOPATH) 'bin\govulncheck.exe'
Log ("GOPATH bin candidate: $gv  exists=" + (Test-Path $gv))

Log ""
Log "=== [5] POST-SCAN INTEGRITY FINGERPRINTS ==="
foreach($t in $targets){ $p=Join-Path $R $t; if(Test-Path $p){ $h=(Get-FileHash $p -Algorithm SHA256).Hash; Log ("POST " + $h + "  " + $t) } else { Log ("POST MISSING  " + $t) } }
Log "DONE_PHASE1=1"
