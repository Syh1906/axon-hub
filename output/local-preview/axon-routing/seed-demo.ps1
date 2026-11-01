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
    $project = (Invoke-PreviewGraphQL '{ projects(first: 100) { edges { node { id name } } } }').projects.edges.node | Where-Object name -eq 'Default' | Select-Object -First 1
    $headers['X-Project-ID'] = $project.id
    $existingChannels = @((Invoke-PreviewGraphQL '{ channels(first: 100) { edges { node { id name } } } }').channels.edges.node)
    $channelDefinitions = @(
        @{ name = '演示 A · 原名转发'; models = @('demo-chat', 'demo-fast-small', 'lab-demo-thinking', 'gpt-6-luna', 'gpt-6.1-luna'); settings = @{ streamFirstEventTimeoutSeconds = 20; nonStreamResponseTimeoutSeconds = 120; disableSessionReuse = $true } },
        @{ name = '演示 B · 固定 5.6'; models = @('gpt-5.6'); settings = @{ streamFirstEventTimeoutSeconds = 0; disableSessionReuse = $false } },
        @{ name = '演示 C · 固定 6.0'; models = @('gpt-6.0'); settings = @{ disableSessionReuse = $false } }
    )
    $channelIDs = @()
    foreach ($definition in $channelDefinitions) {
        $channel = $existingChannels | Where-Object name -eq $definition.name | Select-Object -First 1
        if (-not $channel) {
            $channel = (Invoke-PreviewGraphQL 'mutation($input: CreateChannelInput!) { createChannel(input: $input) { id name } }' @{
                input = @{
                    type = 'openai'; name = $definition.name; baseURL = 'http://127.0.0.1:9/v1'
                    credentials = @{ apiKey = 'local-preview-placeholder' }; supportedModels = $definition.models
                    manualModels = $definition.models; defaultTestModel = $definition.models[0]; autoSyncSupportedModels = $false
                    tags = @('本地演示'); settings = $definition.settings; remark = '本地界面演示，占位渠道；不连接真实供应商。'
                }
            }).createChannel
        }
        $channelIDs += [int]($channel.id.Split('/')[-1])
    }

    $existingModels = @((Invoke-PreviewGraphQL '{ models(first: 100) { edges { node { id name } } } }').models.edges.node)
    $modelDefinitions = @(
        @{ name = '演示 · 精确匹配'; type = 'exact'; pattern = 'demo-chat'; priority = 100; publicIDs = @('demo-chat') },
        @{ name = '演示 · 前缀匹配'; type = 'prefix'; pattern = 'demo-fast-'; priority = 100; publicIDs = @('demo-fast-small') },
        @{ name = '演示 · 后缀匹配'; type = 'suffix'; pattern = '-demo-thinking'; priority = 100; publicIDs = @('lab-demo-thinking') },
        @{ name = '演示 · Luna 正则分流'; type = 'regex'; pattern = '^gpt-.*-luna$'; priority = 50; publicIDs = @('gpt-6-luna', 'gpt-6.1-luna') }
    )
    foreach ($definition in $modelDefinitions) {
        if ($existingModels | Where-Object name -eq $definition.name) { continue }
        $associations = @(@{ type = 'channel_request'; priority = 10; channelRequest = @{ channelId = $channelIDs[0] } })
        if ($definition.type -eq 'regex') {
            $associations += @{ type = 'channel_model'; priority = 20; channelModel = @{ channelId = $channelIDs[1]; modelId = 'gpt-5.6' } }
            $associations += @{ type = 'channel_model'; priority = 30; channelModel = @{ channelId = $channelIDs[2]; modelId = 'gpt-6.0' } }
        }
        $model = (Invoke-PreviewGraphQL 'mutation($input: CreateModelInput!) { createModel(input: $input) { id } }' @{
            input = @{
                developer = 'openai'; modelID = $definition.pattern; name = $definition.name; icon = 'OpenAI'; group = '本地演示'
                matchType = $definition.type; matchPriority = $definition.priority; publicModelIds = $definition.publicIDs
                modelCard = @{ reasoning = @{ supported = $false; default = $false }; toolCall = $true; temperature = $true; vision = $false; modalities = @{ input = @('text'); output = @('text') }; limit = @{ context = 128000; output = 8192 } }
                settings = @{ disableDeveloperSettingsInheritance = $true; associations = $associations }
                remark = '本地演示数据，可直接编辑匹配方式、测试模型 ID 和渠道关联。'
            }
        }).createModel
        $null = Invoke-PreviewGraphQL 'mutation($id: ID!) { updateModelStatus(id: $id, status: enabled) }' @{ id = $model.id }
    }

    $existingKeys = @((Invoke-PreviewGraphQL '{ apiKeys(first: 100) { edges { node { id name profiles { profiles { name } } } } } }').apiKeys.edges.node)
    $keyDefinitions = @(
        @{ name = '演示密钥 A · 原名优先'; profile = '原名优先'; priorities = @(10, 20, 30) },
        @{ name = '演示密钥 B · 5.6 优先'; profile = '5.6 优先'; priorities = @(20, 10, 30) }
    )
    foreach ($definition in $keyDefinitions) {
        $apiKey = $existingKeys | Where-Object name -eq $definition.name | Select-Object -First 1
        if (-not $apiKey) {
            $apiKey = (Invoke-PreviewGraphQL 'mutation($input: CreateAPIKeyInput!) { createAPIKey(input: $input) { id name } }' @{ input = @{ name = $definition.name; type = 'user'; projectID = $project.id } }).createAPIKey
        } elseif ($apiKey.profiles.profiles.Count -gt 0) {
            continue
        }
        $priorities = @(for ($i = 0; $i -lt $channelIDs.Count; $i++) { @{ channelId = $channelIDs[$i]; priority = $definition.priorities[$i] } })
        $profile = @{ name = $definition.profile; modelMappings = @(); channelTags = @('本地演示'); channelTagsMatchMode = 'any'; channelPriorities = $priorities }
        $null = Invoke-PreviewGraphQL 'mutation($id: ID!, $input: UpdateAPIKeyProfilesInput!) { updateAPIKeyProfiles(id: $id, input: $input) { id } }' @{ id = $apiKey.id; input = @{ activeProfile = $profile.name; profiles = @($profile) } }
    }

    $templates = @((Invoke-PreviewGraphQL '{ apiKeyProfileTemplates(first: 100) { edges { node { name } } } }').apiKeyProfileTemplates.edges.node)
    if (-not ($templates | Where-Object name -eq '演示模板 · 原名优先')) {
        $templatePriorities = @(for ($i = 0; $i -lt $channelIDs.Count; $i++) { @{ channelId = $channelIDs[$i]; priority = ($i + 1) * 10 } })
        $null = Invoke-PreviewGraphQL 'mutation($input: CreateAPIKeyProfileTemplateInput!, $profile: APIKeyProfileInput!) { createApiKeyProfileTemplate(input: $input, profile: $profile) { id } }' @{
            input = @{ name = '演示模板 · 原名优先'; description = '本地演示：三个渠道的优先级为 10 / 20 / 30。'; projectID = $project.id }
            profile = @{ name = '原名优先'; modelMappings = @(); channelTags = @('本地演示'); channelTagsMatchMode = 'any'; channelPriorities = $templatePriorities }
        }
    }

    $verification = Invoke-PreviewGraphQL '{ channels(first: 100) { edges { node { name } } } models(first: 100) { edges { node { name matchType } } } apiKeys(first: 100) { edges { node { name profiles { profiles { modelMappings { from to } channelPriorities { channelId priority } } } } } } apiKeyProfileTemplates(first: 100) { edges { node { name profile { modelMappings { from to } } } } } }'
    $counts = @{
        channels = @($verification.channels.edges.node | Where-Object name -Like '演示 *').Count
        models = @($verification.models.edges.node | Where-Object name -Like '演示 *').Count
        apiKeys = @($verification.apiKeys.edges.node | Where-Object name -Like '演示密钥 *').Count
        templates = @($verification.apiKeyProfileTemplates.edges.node | Where-Object name -Like '演示模板 *').Count
    }
    if ($counts.channels -ne 3 -or $counts.models -ne 4 -or $counts.apiKeys -ne 2 -or $counts.templates -ne 1) {
        throw '演示数据数量与预期不符，未标记完成。'
    }
    $demoProfiles = @(($verification.apiKeys.edges.node | Where-Object name -Like '演示密钥 *').profiles.profiles) + @(($verification.apiKeyProfileTemplates.edges.node | Where-Object name -Like '演示模板 *').profile)
    if ($demoProfiles | Where-Object { $null -eq $_.modelMappings }) {
        throw '演示配置缺少界面所需的 modelMappings 列表，未标记完成。'
    }
    @{ phase = 'complete'; counts = $counts } | ConvertTo-Json -Depth 4 | Set-Content -Encoding utf8 -LiteralPath $statePath
    Write-Host '本地演示已就绪：4 个匹配模型、3 个占位渠道、2 把不同优先级的演示密钥、1 个配置模板。'
} finally {
    $headers.Clear()
    $signIn = $null
}
