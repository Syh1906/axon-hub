$ErrorActionPreference = 'Stop'
$scriptPath = Join-Path $PSScriptRoot '../../deploy/upgrade.ps1'
$tokens = $null
$parseErrors = $null
$tree = [System.Management.Automation.Language.Parser]::ParseFile(
    (Resolve-Path $scriptPath), [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count) { throw '升级脚本存在语法错误' }
$function = $tree.Find({ param($node)
    $node -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Compare-Version'
}, $true)
Invoke-Expression $function.Extent.Text

$cases = @(
    @('v1.0.0-beta10-fork.7.syh.10', 'v1.0.0-beta11-fork.1.syh.1', -1),
    @('v1.0.0-beta10-fork.7.syh.9', 'v1.0.0-beta10-fork.7.syh.10', -1),
    @('v1.0.0-beta10-fork.7.syh.10', 'v1.0.0-beta10-fork.8', -1),
    @('v1.0.0-beta10', 'v1.0.0', -1),
    @('v1.0.0', 'v1.0.0-beta10', 1),
    @('v1.0.0+build.1', 'v1.0.0+build.2', 0)
)
foreach ($case in $cases) {
    $actual = [Math]::Sign((Compare-Version $case[0] $case[1]))
    if ($actual -ne $case[2]) { throw "版本顺序不正确：$($case[0]) 与 $($case[1])，实际 $actual" }
}
Write-Output "$($cases.Count) 个 Windows 版本比较用例通过"
