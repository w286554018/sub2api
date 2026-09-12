param(
    [string]$GoExecutable = 'go',
    [string]$OutputDirectory = (Join-Path ([IO.Path]::GetTempPath()) ('sub2api-fingerprint-offline-' + [guid]::NewGuid())),
    [string]$TestPattern = '^Test(Codex|ApplyCodex|StageCodex|OpenAIWSHTTPBridge)',
    [ValidateSet('./internal/service', './internal/handler')]
    [string]$TestPackage = './internal/service',
    [string]$Distribution = ''
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
Set-StrictMode -Version Latest
$exitCode = 1
$pushed = $false
$previous = @{}

function Assert-NativeSuccess([string]$Stage) {
    if ($LASTEXITCODE -ne 0) {
        $script:exitCode = $LASTEXITCODE
        throw "$Stage failed (exit $LASTEXITCODE). No installation or fallback was attempted."
    }
}

function Convert-WslPath([string]$WindowsPath) {
    $converted = @(& wsl.exe @wslArguments --exec wslpath -u $WindowsPath)
    Assert-NativeSuccess 'WSL path conversion'
    if ($converted.Count -ne 1 -or [string]::IsNullOrWhiteSpace($converted[0]) -or
        -not $converted[0].StartsWith('/')) {
        throw 'WSL path conversion did not return one absolute Linux path.'
    }
    return $converted[0].Trim()
}

try {
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
        throw 'This launcher requires Windows and an existing WSL2 Linux distribution.'
    }
    if ([string]::IsNullOrWhiteSpace($TestPattern)) { throw 'TestPattern must not be empty.' }
    $project = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
    $output = [IO.Path]::GetFullPath($OutputDirectory)
    if ($output.Equals($project, [StringComparison]::OrdinalIgnoreCase) -or
        $output.StartsWith($project + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Keep Linux test artifacts outside the Windows worktree.'
    }
    $wslArguments = @()
    if (-not [string]::IsNullOrWhiteSpace($Distribution)) {
        $wslArguments += @('--distribution', $Distribution)
    }
    $architecture = @(& wsl.exe @wslArguments --exec uname -m)
    Assert-NativeSuccess 'WSL architecture query'
    if ($architecture.Count -ne 1) { throw 'WSL returned an ambiguous architecture.' }
    $goarch = switch ($architecture[0].Trim()) {
        'x86_64' { 'amd64' }
        'aarch64' { 'arm64' }
        default { throw 'Only x86_64 and aarch64 WSL distributions are supported.' }
    }
    $null = New-Item -ItemType Directory -Path $output -Force
    $binary = Join-Path $output 'codex-fingerprint.test'
    $overrides = @{
        GOOS = 'linux'; GOARCH = $goarch; CGO_ENABLED = '0'; GOTOOLCHAIN = 'local'
        GOPROXY = 'off'; GOSUMDB = 'off'; GOWORK = 'off'; GOFLAGS = ''
    }
    foreach ($name in $overrides.Keys) {
        $previous[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
        [Environment]::SetEnvironmentVariable($name, $overrides[$name], 'Process')
    }
    Push-Location (Join-Path $project 'backend')
    $pushed = $true
    & $GoExecutable test -c -mod=readonly -tags=unit -buildvcs=false -o $binary $TestPackage
    Assert-NativeSuccess 'Offline Linux test compilation'
    $linuxBinary = Convert-WslPath $binary
    $linuxRunner = Convert-WslPath (Join-Path $PSScriptRoot 'run-codex-fingerprint-offline.sh')
    & wsl.exe @wslArguments --exec sh $linuxRunner $linuxBinary $TestPattern
    Assert-NativeSuccess 'Isolated Linux tests'
    $exitCode = 0
}
catch {
    [Console]::Error.WriteLine($_.Exception.Message)
}
finally {
    if ($pushed) { Pop-Location }
    foreach ($name in $previous.Keys) {
        [Environment]::SetEnvironmentVariable($name, $previous[$name], 'Process')
    }
}
exit $exitCode
