$ErrorActionPreference = 'Stop'
$runtime = Join-Path $PSScriptRoot 'runtime'
$recordPath = Join-Path $runtime 'backend-process.json'
$statePath = Join-Path $runtime 'demo-data.json'
$baseUrl = 'http://127.0.0.1:8090'

# 只允许写入由此目录启动的本地后端，避免端口被其他实例接管后误写。
$record = Get-Content -Raw -LiteralPath $recordPath | ConvertFrom-Json
$rootProcess = Get-Process -Id $record.id -ErrorAction Stop
$recordStartedAt = [datetime]$record.startedAt
if ($rootProcess.Path -ne $record.executable -or $rootProcess.StartTime.ToUniversalTime().Ticks -ne $recordStartedAt.ToUniversalTime().Ticks) {
    throw '本地预览后端的进程记录已失效，请先运行 run-backend.ps1。'
}
$listener = Get-NetTCPConnection -LocalAddress '127.0.0.1' -LocalPort 8090 -State Listen -ErrorAction Stop
$processIdToCheck = [int]$listener.OwningProcess
while ($processIdToCheck -ne 0 -and $processIdToCheck -ne $record.id) {
    $processIdToCheck = [int](Get-CimInstance Win32_Process -Filter "ProcessId = $processIdToCheck" -Property ParentProcessId).ParentProcessId
}
if ($processIdToCheck -ne $record.id) {
    throw '8090 当前不属于此本地预览后端，未写入任何演示数据。'
}

function Invoke-PreviewRequest([string]$Path, [hashtable]$Body) {
    Invoke-RestMethod -Uri "$baseUrl$Path" -Method Post -ContentType 'application/json; charset=utf-8' -Body ($Body | ConvertTo-Json -Depth 30 -Compress)
}

$status = Invoke-RestMethod -Uri "$baseUrl/admin/system/status"
$state = if (Test-Path -LiteralPath $statePath) { Get-Content -Raw -LiteralPath $statePath | ConvertFrom-Json } else { $null }
if ($status.isInitialized -and $state.phase -eq 'complete') {
    Write-Host '本地演示数据已就绪，保留已有配置和调试修改。'
    return
}
if ($status.isInitialized -and $state.phase -eq 'base-ready') {
    & python -X utf8 (Join-Path $PSScriptRoot 'seed-records.py')
    if ($LASTEXITCODE -ne 0) { throw '示例记录创建失败。' }
    return
}
if ($status.isInitialized -and -not $state) {
    Write-Warning '本地库已由用户初始化，跳过演示账号和数据创建，保留现有数据。'
    return
}

# 沿用仓库 E2E 规则中的公开测试账号；只用于隔离的本地演示库。
$demoEmail = 'my@example.com'
$demoPassword = 'pwd123456'
if (-not $status.isInitialized) {
    @{ phase = 'initializing' } | ConvertTo-Json | Set-Content -Encoding utf8 -LiteralPath $statePath
    $result = Invoke-PreviewRequest '/admin/system/initialize' @{
        ownerEmail = $demoEmail; ownerPassword = $demoPassword
        ownerFirstName = '演示'; ownerLastName = '本地'; brandName = 'AxonHub 本地演示'; preferLanguage = 'zh-CN'
    }
    if (-not $result.success) { throw '本地演示系统初始化失败。' }
}

$signIn = Invoke-PreviewRequest '/admin/auth/signin' @{ email = $demoEmail; password = $demoPassword }
$headers = @{ Authorization = "Bearer $($signIn.token)" }
function Invoke-PreviewGraphQL([string]$Query, [hashtable]$Variables = @{}) {
    $response = Invoke-RestMethod -Uri "$baseUrl/admin/graphql" -Method Post -Headers $headers -ContentType 'application/json; charset=utf-8' -Body (@{ query = $Query; variables = $Variables } | ConvertTo-Json -Depth 30 -Compress)
    if ($response.errors) { throw (($response.errors | ForEach-Object { $_.message }) -join '; ') }
    $response.data
}

try {
    $project = (Invoke-PreviewGraphQL '{ projects(first: 100) { edges { node { id name } } } }').projects.edges.node | Select-Object -First 1
    $headers['X-Project-ID'] = $project.id
    $null = Invoke-PreviewGraphQL 'mutation { updateSystemGeneralSettings(input: { timezone: "Asia/Shanghai", currencyCode: "USD" }) }'
    $definitions = @(
        @{ name = '验收 · OpenAI 主渠道'; type = 'openai'; tags = @('高优先级', '生产', '长标签展示与悬停检查'); websiteURL = 'https://example.com' },
        @{ name = '验收 · Anthropic 备用'; type = 'anthropic'; tags = @('备用', '低成本'); websiteURL = $null }
    )
    $existingChannels = @((Invoke-PreviewGraphQL '{ channels(first: 100) { edges { node { id name } } } }').channels.edges.node)
    foreach ($definition in $definitions) {
        $input = @{ type = $definition.type; name = $definition.name; baseURL = 'http://127.0.0.1:9/v1'; credentials = @{ apiKey = 'local-preview-placeholder' }; defaultTestModel = 'gpt-5.2-codex'; supportedModels = @('gpt-5.2-codex', 'claude-sonnet-4-5'); manualModels = @('gpt-5.2-codex', 'claude-sonnet-4-5'); tags = $definition.tags; remark = '人工验收占位渠道，不连接真实供应商。'; autoSyncSupportedModels = $false }
        if ($definition.websiteURL) { $input.websiteURL = $definition.websiteURL }
        $channel = $existingChannels | Where-Object name -eq $definition.name | Select-Object -First 1
        if (-not $channel) { $channel = (Invoke-PreviewGraphQL 'mutation($input: CreateChannelInput!) { createChannel(input: $input) { id } }' @{ input = $input }).createChannel }
        $null = Invoke-PreviewGraphQL 'mutation($id: ID!) { updateChannelStatus(id: $id, status: disabled) { id } }' @{ id = $channel.id }
    }
    foreach ($name in @('验收 · 研发 Key', '验收 · 未分组 Key', '验收 · 停用 Key')) {
        $null = Invoke-PreviewGraphQL 'mutation($input: CreateAPIKeyInput!) { createAPIKey(input: $input) { id name } }' @{ input = @{ name = $name; type = 'user'; projectID = $project.id } }
    }
    @{ phase = 'base-ready' } | ConvertTo-Json | Set-Content -Encoding utf8 -LiteralPath $statePath
    Write-Host '验收基础数据已创建。'
} finally {
    $headers.Clear()
    $signIn = $null
}

& python -X utf8 (Join-Path $PSScriptRoot 'seed-records.py')
if ($LASTEXITCODE -ne 0) { throw '示例记录创建失败。' }
