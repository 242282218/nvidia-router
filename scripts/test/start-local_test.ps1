$ErrorActionPreference = 'Stop'

$root = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$script = Join-Path $root 'scripts\start-local.ps1'

if (-not (Test-Path -LiteralPath $script)) {
    throw 'start-local.ps1 is missing'
}

$source = Get-Content -Raw -LiteralPath $script
if ($source -notmatch "'--port', '5173'") {
    throw 'Vite port must be explicit'
}
if ($source -notmatch 'Test-ProcessSnapshot \$apiLauncher' -or
    $source -notmatch 'Test-ProcessSnapshot \$viteLauncher') {
    throw 'failure cleanup must validate launcher identity'
}
if ($source -notmatch 'Test-IsDescendantProcessId') {
    throw 'startup must associate port listeners with launchers'
}
if ($source -notmatch 'treeSnapshots' -or
    $source -notmatch 'Get-ProcessSnapshot \$treeId') {
    throw 'failure cleanup must snapshot the full process tree before stopping it'
}

$output = & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $script -CheckOnly 2>&1
if ($LASTEXITCODE -ne 0 -or ($output -notmatch 'local-start-check=PASS')) {
    throw "start-local.ps1 check failed: $output"
}

$statePath = Join-Path $root 'tmp\local-start-state.json'
if (-not (Test-Path -LiteralPath $statePath)) {
    throw 'unified startup state is missing; run the normal launcher first'
}
$state = Get-Content -Raw -LiteralPath $statePath | ConvertFrom-Json
function Get-ProcessTreeIds([int]$rootId) {
    $records = @(Get-CimInstance Win32_Process | Select-Object ProcessId, ParentProcessId)
    $ids = New-Object 'System.Collections.Generic.List[int]'
    [void]$ids.Add($rootId)
    $changed = $true
    while ($changed) {
        $changed = $false
        foreach ($record in $records) {
            $processId = [int]$record.ProcessId
            if ($ids.Contains($processId)) {
                continue
            }
            if ($ids.Contains([int]$record.ParentProcessId)) {
                [void]$ids.Add($processId)
                $changed = $true
            }
        }
    }
    return @($ids)
}

foreach ($name in @('ApiLauncher', 'ApiPort', 'ViteLauncher', 'VitePort')) {
    $snapshot = $state.$name
    $process = Get-Process -Id ([int]$snapshot.Id) -ErrorAction SilentlyContinue
    $processInfo = Get-CimInstance Win32_Process -Filter "ProcessId = $($snapshot.Id)" -ErrorAction SilentlyContinue
    if ($null -eq $process -or
        $process.ProcessName -ne $snapshot.Name -or
        $process.Path -ne $snapshot.Path -or
        $process.StartTime.ToUniversalTime().ToString('o') -ne $snapshot.StartTimeUtc -or
        ($null -ne $snapshot.ParentProcessId -and
            ($null -eq $processInfo -or [int]$processInfo.ParentProcessId -ne [int]$snapshot.ParentProcessId))) {
        throw "state process mismatch: $name"
    }
}
foreach ($pair in @(
    @('ApiLauncher', 'ApiPort'),
    @('ViteLauncher', 'VitePort')
)) {
    $launcher = $state.($pair[0])
    $portProcess = $state.($pair[1])
    if (-not (Get-ProcessTreeIds ([int]$launcher.Id)).Contains([int]$portProcess.Id)) {
        throw "state port process is not owned by launcher: $($pair[1])"
    }
}

foreach ($uri in @(
    'http://127.0.0.1:3756/health/live',
    'http://127.0.0.1:5173/',
    'http://127.0.0.1:5173/@vite/client'
)) {
    $response = Invoke-WebRequest -UseBasicParsing -Uri $uri -TimeoutSec 5
    if ([int]$response.StatusCode -ne 200) {
        throw "$uri returned $($response.StatusCode)"
    }
}

$proxyStatus = & curl.exe -sS -o NUL -w '%{http_code}' 'http://127.0.0.1:5173/admin/api/models'
if ($LASTEXITCODE -ne 0 -or $proxyStatus -ne '401') {
    throw "Vite API proxy returned unexpected status: $proxyStatus"
}

Write-Output 'PASS: local startup check'
