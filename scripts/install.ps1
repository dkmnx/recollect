# Install the recollect engine from a GitHub release.
# Usage: install.ps1 [-Version vX.Y.Z] [-Dir DIR]
param(
    [string]$Version = "latest",
    [string]$Dir = "$env:LOCALAPPDATA\Programs\recollect"
)

$ErrorActionPreference = "Stop"
$Repo = "dkmnx/recollect"
$Binary = "recollect"
$BaseUrl = if ($env:RECOLLECT_BASE_URL) { $env:RECOLLECT_BASE_URL } else { "https://github.com/$Repo/releases/download" }

if ($Version -eq "latest") {
    $release = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
    $Version = $release.tag_name
}
$tag = if ($Version.StartsWith("v")) { $Version } else { "v$Version" }
$version = $tag.Substring(1)

$arch = switch ($env:PROCESSOR_ARCHITECTURE.ToUpperInvariant()) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default { throw "unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}
$asset = "${Binary}_${version}_windows_${arch}.zip"

$tmp = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $archive = Join-Path $tmp $asset
    $checksums = Join-Path $tmp "checksums.txt"
    Invoke-WebRequest "$BaseUrl/$tag/$asset" -OutFile $archive
    Invoke-WebRequest "$BaseUrl/$tag/checksums.txt" -OutFile $checksums

    $line = Get-Content $checksums |
        Where-Object { $_ -match "\s$([regex]::Escape($asset))$" } |
        Select-Object -First 1
    if (-not $line) { throw "no checksum for $asset" }
    $expected = ($line -split "\s+")[0]
    $actual = (Get-FileHash $archive -Algorithm SHA256).Hash
    if ($actual.ToLowerInvariant() -ne $expected.ToLowerInvariant()) {
        throw "checksum mismatch for $asset"
    }

    Expand-Archive $archive -DestinationPath $tmp -Force
    New-Item -ItemType Directory -Path $Dir -Force | Out-Null
    Copy-Item (Join-Path $tmp "$Binary.exe") (Join-Path $Dir "$Binary.exe") -Force

    Write-Host "installed $Dir\$Binary.exe ($tag)"
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if (($userPath -split ";") -notcontains $Dir) {
        Write-Host "Add $Dir to your user PATH."
    }
}
finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
