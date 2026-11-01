$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../../..')).Path
$runtime = Join-Path $PSScriptRoot 'runtime'
if (Get-NetTCPConnection -LocalPort 5174 -State Listen -ErrorAction SilentlyContinue) {
    throw '5174 已被占用；请先查看 http://127.0.0.1:5174/preview.html 是否已启动。'
}
New-Item -ItemType Directory -Path $runtime -Force | Out-Null
$hostProcess = Get-Process -Id $PID
@{ id = $PID; startedAt = $hostProcess.StartTime.ToUniversalTime().ToString('o'); executable = $hostProcess.Path } |
    ConvertTo-Json | Set-Content -Encoding utf8 (Join-Path $runtime 'gallery-process.json')
$gallery = Join-Path $repoRoot 'output/implementation/axon-routing-20261027'
Write-Host '静态效果页：http://127.0.0.1:5174/preview.html；Ctrl+C 停止。'
& python -m http.server 5174 --bind 127.0.0.1 --directory $gallery
