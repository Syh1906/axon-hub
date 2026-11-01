$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../../..')).Path
$runtime = Join-Path $PSScriptRoot 'runtime'
if (Get-NetTCPConnection -LocalPort 5173 -State Listen -ErrorAction SilentlyContinue) {
    throw '5173 已被占用；请保留现有开发服务，先确认它的用途。'
}
New-Item -ItemType Directory -Path $runtime -Force | Out-Null
$hostProcess = Get-Process -Id $PID
@{ id = $PID; startedAt = $hostProcess.StartTime.ToUniversalTime().ToString('o'); executable = $hostProcess.Path } |
    ConvertTo-Json | Set-Content -Encoding utf8 (Join-Path $runtime 'frontend-process.json')
$env:VITE_API_URL = 'http://127.0.0.1:8090'
Push-Location (Join-Path $repoRoot 'frontend')
try {
    Write-Host '真实前端：http://127.0.0.1:5173；支持热更新；Ctrl+C 停止。'
    & node (Join-Path $repoRoot 'frontend/node_modules/vite/bin/vite.js') --host 127.0.0.1 --port 5173 --strictPort
} finally {
    Pop-Location
}
