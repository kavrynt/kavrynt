param(
    [string]$Version = $env:KAVRYNT_VERSION,
    [string]$InstallDir = $env:INSTALL_DIR,
    [string]$Repo = $env:KAVRYNT_REPO
)

if ([string]::IsNullOrWhiteSpace($Version)) {
    $Version = "latest"
}
if ([string]::IsNullOrWhiteSpace($InstallDir)) {
    $InstallDir = Join-Path $HOME ".kavrynt\bin"
}
if ([string]::IsNullOrWhiteSpace($Repo)) {
    $Repo = "kavrynt/kavrynt"
}

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default { throw "unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}

if ($arch -eq "arm64") {
    throw "windows arm64 binaries are not published yet"
}

$archive = "kavryctl_windows_${arch}.zip"
if ($Version -eq "latest") {
    $baseUrl = "https://github.com/$Repo/releases/latest/download"
} else {
    $baseUrl = "https://github.com/$Repo/releases/download/$Version"
}

$tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ("kavryctl-" + [System.Guid]::NewGuid())
New-Item -ItemType Directory -Path $tmpDir | Out-Null

try {
    $archivePath = Join-Path $tmpDir $archive
    $checksumsPath = Join-Path $tmpDir "checksums.txt"

    Write-Host "Downloading $archive from $Repo..."
    Invoke-WebRequest -Uri "$baseUrl/$archive" -OutFile $archivePath
    Invoke-WebRequest -Uri "$baseUrl/checksums.txt" -OutFile $checksumsPath

    $line = Get-Content $checksumsPath | Where-Object { $_ -match "\s$([regex]::Escape($archive))$" } | Select-Object -First 1
    if (-not $line) {
        throw "checksum for $archive not found"
    }
    $expected = ($line -split "\s+")[0]
    $actual = (Get-FileHash -Algorithm SHA256 $archivePath).Hash.ToLowerInvariant()
    if ($actual -ne $expected.ToLowerInvariant()) {
        throw "checksum mismatch for $archive"
    }

    Expand-Archive -Path $archivePath -DestinationPath $tmpDir -Force
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    Copy-Item -Path (Join-Path $tmpDir "kavryctl.exe") -Destination (Join-Path $InstallDir "kavryctl.exe") -Force

    Write-Host "Installed kavryctl to $InstallDir\kavryctl.exe"
    if (($env:PATH -split ";") -notcontains $InstallDir) {
        Write-Host "Add $InstallDir to PATH to run kavryctl from any shell."
    }
    Write-Host "Run: kavryctl version"
} finally {
    Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
}
