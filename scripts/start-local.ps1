[CmdletBinding()]
param(
    [switch]$CheckOnly
)

$ErrorActionPreference = 'Stop'

$root = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$webRoot = Join-Path $root 'web'
$tmpRoot = Join-Path $root 'tmp'
$envPath = Join-Path $root '.env'
$vitePath = Join-Path $webRoot 'node_modules\.bin\vite.cmd'
$statePath = Join-Path $tmpRoot 'local-start-state.json'

function Import-DotEnv([string]$path) {
    if (-not (Test-Path -LiteralPath $path)) {
        throw '.env is required for the local API'
    }

    foreach ($line in Get-Content -LiteralPath $path) {
        $trimmed = $line.Trim()
        if ([string]::IsNullOrWhiteSpace($trimmed) -or $trimmed.StartsWith('#')) {
            continue
        }
        $equals = $trimmed.IndexOf('=')
        if ($equals -le 0) {
            continue
        }
        $name = $trimmed.Substring(0, $equals).Trim()
        if ($name -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') {
            continue
        }
        $value = $trimmed.Substring($equals + 1)
        if ($value.Length -ge 2) {
            $doubleQuoted = $value.StartsWith('"') -and $value.EndsWith('"')
            $singleQuoted = $value.StartsWith("'") -and $value.EndsWith("'")
            if ($doubleQuoted -or $singleQuoted) {
                $value = $value.Substring(1, $value.Length - 2)
            }
        }
        [Environment]::SetEnvironmentVariable($name, $value, 'Process')
    }
}

function Assert-Command([string]$name) {
    if ($null -eq (Get-Command $name -ErrorAction SilentlyContinue)) {
        throw "$name is required"
    }
}

function Assert-LocalConfig {
    Import-DotEnv $envPath
    Assert-Command 'go.exe'
    if (-not (Test-Path -LiteralPath $vitePath)) {
        throw "Vite is missing: $vitePath"
    }
    $masterKey = [Environment]::GetEnvironmentVariable('NVIDIA_ROUTER_MASTER_KEY', 'Process')
    if ([string]::IsNullOrWhiteSpace($masterKey)) {
        throw 'NVIDIA_ROUTER_MASTER_KEY is required'
    }
}

function Get-ListeningProcessIds([int]$port) {
    return @(Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue |
        Select-Object -ExpandProperty OwningProcess -Unique)
}

function Get-ProcessSnapshot([int]$processId) {
    $process = Get-Process -Id $processId -ErrorAction SilentlyContinue
    if ($null -eq $process) {
        return $null
    }
    $processInfo = Get-CimInstance Win32_Process -Filter "ProcessId = $processId" -ErrorAction SilentlyContinue
    return [PSCustomObject]@{
        Id = $process.Id
        Name = $process.ProcessName
        Path = $process.Path
        StartTimeUtc = $process.StartTime.ToUniversalTime().ToString('o')
        ParentProcessId = if ($null -ne $processInfo) { [int]$processInfo.ParentProcessId } else { $null }
    }
}

function Test-ProcessSnapshot($snapshot) {
    if ($null -eq $snapshot) {
        return $false
    }
    $process = Get-Process -Id ([int]$snapshot.Id) -ErrorAction SilentlyContinue
    if ($null -eq $process) {
        return $false
    }
    return $process.ProcessName -eq $snapshot.Name -and
        $process.Path -eq $snapshot.Path -and
        $process.StartTime.ToUniversalTime().ToString('o') -eq $snapshot.StartTimeUtc -and
        (Test-ParentProcessId $process.Id $snapshot.ParentProcessId)
}

function Test-ParentProcessId([int]$processId, $expectedParentProcessId) {
    if ($null -eq $expectedParentProcessId) {
        return $true
    }
    $processInfo = Get-CimInstance Win32_Process -Filter "ProcessId = $processId" -ErrorAction SilentlyContinue
    return $null -ne $processInfo -and [int]$processInfo.ParentProcessId -eq [int]$expectedParentProcessId
}

function Test-IsDescendantProcessId([int]$rootId, [int]$candidateId) {
    return @(Get-ProcessTreeIds $rootId).Contains($candidateId)
}

function Stop-Snapshot($snapshot) {
    if (-not (Test-ProcessSnapshot $snapshot)) {
        return
    }
    Stop-Process -Id ([int]$snapshot.Id) -ErrorAction Stop
    try {
        Wait-Process -Id ([int]$snapshot.Id) -Timeout 10 -ErrorAction Stop
    } catch {
    }
}

function Stop-TrackedServices {
    if (-not (Test-Path -LiteralPath $statePath)) {
        foreach ($port in @(3756, 5173)) {
            if ((Get-ListeningProcessIds $port).Count -gt 0) {
                throw "Port $port is occupied; stop the existing service once before using the unified launcher"
            }
        }
        return
    }

    $state = Get-Content -Raw -LiteralPath $statePath | ConvertFrom-Json
    foreach ($name in @('ApiPort', 'ApiLauncher', 'VitePort', 'ViteLauncher')) {
        Stop-Snapshot $state.$name
    }
    Remove-Item -LiteralPath $statePath -Force -ErrorAction SilentlyContinue
    foreach ($port in @(3756, 5173)) {
        if ((Get-ListeningProcessIds $port).Count -gt 0) {
            throw "Port $port is still occupied after stopping the tracked service"
        }
    }
}

function Remove-LocalLogs {
    foreach ($path in @(
        (Join-Path $tmpRoot 'local-api.out.log'),
        (Join-Path $tmpRoot 'local-api.err.log'),
        (Join-Path $tmpRoot 'local-vite.out.log'),
        (Join-Path $tmpRoot 'local-vite.err.log')
    )) {
        Remove-Item -LiteralPath $path -Force -ErrorAction SilentlyContinue
    }
}

function Wait-HttpOk([string]$uri, [int]$timeoutSeconds) {
    $deadline = (Get-Date).AddSeconds($timeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri $uri -TimeoutSec 2
            if ([int]$response.StatusCode -eq 200) {
                return
            }
        } catch {
        }
        Start-Sleep -Milliseconds 250
    }
    throw "Local service did not become healthy: $uri"
}

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

function Stop-StartedServices($apiLauncher, $viteLauncher) {
    $rootIds = @()
    if ($null -ne $apiLauncher -and (Test-ProcessSnapshot $apiLauncher)) {
        $rootIds += [int]$apiLauncher.Id
    }
    if ($null -ne $viteLauncher -and (Test-ProcessSnapshot $viteLauncher)) {
        $rootIds += [int]$viteLauncher.Id
    }
    foreach ($rootId in $rootIds | Select-Object -Unique) {
        $treeIds = @(Get-ProcessTreeIds $rootId)
        $treeSnapshots = @()
        foreach ($treeId in $treeIds) {
            $treeSnapshots += Get-ProcessSnapshot $treeId
        }
        for ($index = $treeSnapshots.Count - 1; $index -ge 0; $index--) {
            if ($null -ne $treeSnapshots[$index]) {
                Stop-Snapshot $treeSnapshots[$index]
            }
        }
    }
}

function Get-PortSnapshotForLauncher([int]$port, [int]$launcherId, [string]$label) {
    foreach ($processId in Get-ListeningProcessIds $port) {
        if (-not (Test-IsDescendantProcessId $launcherId ([int]$processId))) {
            continue
        }
        $snapshot = Get-ProcessSnapshot ([int]$processId)
        if ($null -ne $snapshot) {
            return $snapshot
        }
    }
    throw "$label is not listening on the expected port $port"
}

Assert-LocalConfig
if ($CheckOnly) {
    Write-Output 'local-start-check=PASS'
    exit 0
}

Stop-TrackedServices
Remove-LocalLogs
[Environment]::SetEnvironmentVariable('VITE_PROXY_ORIGIN', 'http://127.0.0.1:3756', 'Process')

$goPath = (Get-Command 'go.exe').Source
$api = $null
$vite = $null
$apiSnapshot = $null
$viteSnapshot = $null
try {
    $api = Start-Process -FilePath $goPath -ArgumentList @('run', './cmd/nvidia-router', 'serve') `
        -WorkingDirectory $root -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $tmpRoot 'local-api.out.log') `
        -RedirectStandardError (Join-Path $tmpRoot 'local-api.err.log') -PassThru
    $apiSnapshot = Get-ProcessSnapshot $api.Id
    $vite = Start-Process -FilePath $vitePath -ArgumentList @('--host', '127.0.0.1', '--port', '5173', '--strictPort') `
        -WorkingDirectory $webRoot -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $tmpRoot 'local-vite.out.log') `
        -RedirectStandardError (Join-Path $tmpRoot 'local-vite.err.log') -PassThru
    $viteSnapshot = Get-ProcessSnapshot $vite.Id

    Wait-HttpOk 'http://127.0.0.1:3756/health/live' 30
    Wait-HttpOk 'http://127.0.0.1:5173/' 30

    $state = [PSCustomObject]@{
        ApiLauncher = $apiSnapshot
        ApiPort = Get-PortSnapshotForLauncher 3756 $apiSnapshot.Id 'API service'
        ViteLauncher = $viteSnapshot
        VitePort = Get-PortSnapshotForLauncher 5173 $viteSnapshot.Id 'Vite service'
    }
    $state | ConvertTo-Json | Set-Content -LiteralPath $statePath -Encoding utf8

    Write-Output "local-api-pid=$($api.Id)"
    Write-Output "local-vite-pid=$($vite.Id)"
    Write-Output 'local-api=http://127.0.0.1:3756'
    Write-Output 'local-web=http://127.0.0.1:5173'
} catch {
    if ($null -ne $apiSnapshot -or $null -ne $viteSnapshot) {
        Stop-StartedServices $apiSnapshot $viteSnapshot
    }
    Remove-Item -LiteralPath $statePath -Force -ErrorAction SilentlyContinue
    throw
}
