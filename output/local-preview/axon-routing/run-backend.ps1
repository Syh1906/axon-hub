$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../../..')).Path
$runtime = Join-Path $PSScriptRoot 'runtime'
if (Get-NetTCPConnection -LocalPort 8090 -State Listen -ErrorAction SilentlyContinue) {
    throw '8090 已被占用；请保留现有服务，先确认它的用途。'
}
New-Item -ItemType Directory -Path $runtime -Force | Out-Null
$hostProcess = Get-Process -Id $PID
@{ id = $PID; startedAt = $hostProcess.StartTime.ToUniversalTime().ToString('o'); executable = $hostProcess.Path } |
    ConvertTo-Json | Set-Content -Encoding utf8 (Join-Path $runtime 'backend-process.json')

$moduleVersion = ((Get-Content (Join-Path $repoRoot 'go.mod') | Where-Object { $_ -match '^go ' }) -replace '^go ', '').Trim()
$moduleRoot = $repoRoot.Replace('\', '/')
"go $moduleVersion`nuse `"$moduleRoot`"`n" | Set-Content -Encoding utf8 (Join-Path $runtime 'go.work')
@'
server:
  host: "127.0.0.1"
  port: 8090
  name: "AxonHub 本地预览"
  cors:
    enabled: true
    allowed_origins: ["http://127.0.0.1:5173", "http://localhost:5173"]
db:
  dialect: "sqlite3"
  dsn: "file:preview.db?cache=shared&_fk=1&_pragma=journal_mode(WAL)"
cache:
  mode: "memory"
log:
  level: "warn"
  output: "stdio"
'@ | Set-Content -Encoding utf8 (Join-Path $runtime 'config.yml')

# 仅清除这个专用预览进程继承的 AxonHub 配置，避免连接已有数据库或缓存。
Get-ChildItem Env: | Where-Object { $_.Name.StartsWith('AXONHUB_') } | ForEach-Object {
    [Environment]::SetEnvironmentVariable($_.Name, $null, 'Process')
}
$env:GOWORK = Join-Path $runtime 'go.work'
Push-Location $runtime
try {
    Write-Host '本地后端：http://127.0.0.1:8090；数据库保留在本目录 runtime/preview.db；Ctrl+C 停止。'
    & go run github.com/looplj/axonhub/cmd/axonhub
} finally {
    Pop-Location
}
