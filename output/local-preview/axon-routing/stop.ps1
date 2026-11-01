[CmdletBinding(SupportsShouldProcess)]
param()
$ErrorActionPreference = 'Stop'
$runtime = Join-Path $PSScriptRoot 'runtime'

function Get-PreviewProcessTree([int]$RootProcessId) {
    $children = Get-CimInstance Win32_Process -Filter "ParentProcessId = $RootProcessId" -Property ProcessId, ParentProcessId
    foreach ($child in $children) {
        Get-PreviewProcessTree -RootProcessId $child.ProcessId
    }
    Get-Process -Id $RootProcessId -ErrorAction SilentlyContinue
}

foreach ($role in @('frontend', 'backend', 'gallery')) {
    $recordPath = Join-Path $runtime "$role-process.json"
    if (-not (Test-Path -LiteralPath $recordPath)) { continue }
    $record = Get-Content -Raw -LiteralPath $recordPath | ConvertFrom-Json
    $rootProcess = Get-Process -Id $record.id -ErrorAction SilentlyContinue
    if (-not $rootProcess) { continue }
    $recordStartedAt = [datetime]$record.startedAt
    if ($rootProcess.Path -ne $record.executable -or $rootProcess.StartTime.ToUniversalTime().Ticks -ne $recordStartedAt.ToUniversalTime().Ticks) {
        Write-Warning "$role 的 PID 已被其他进程复用，跳过。"
        continue
    }
    foreach ($process in @(Get-PreviewProcessTree -RootProcessId $record.id)) {
        $current = Get-Process -Id $process.Id -ErrorAction SilentlyContinue
        if (-not $current -or $current.StartTime -ne $process.StartTime) { continue }
        if ($PSCmdlet.ShouldProcess("$role / $($process.ProcessName) / PID $($process.Id)", '停止本地预览进程')) {
            Stop-Process -InputObject $current
        }
    }
}
Write-Host '预览数据保留在 runtime/preview.db；停止服务不会删除你的本地配置。'
