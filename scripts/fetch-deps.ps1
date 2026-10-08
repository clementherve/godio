<#
.SYNOPSIS
    Downloads godio's runtime dependencies: the whisper.cpp binary and its models.

.DESCRIPTION
    Places everything beside godio.exe:

        whisper\whisper-cli.exe   (+ its DLLs)
        models\ggml-<model>.bin
        models\ggml-silero-v5.1.2.bin

.PARAMETER Cuda
    Fetch the CUDA build instead of the CPU one. Much faster on an NVIDIA GPU;
    requires a matching driver.

.PARAMETER Model
    Whisper model to download. large-v3-turbo is the default: best accuracy per
    second of compute for French. Drop to 'medium' if transcription is too slow.

.EXAMPLE
    .\scripts\fetch-deps.ps1
    .\scripts\fetch-deps.ps1 -Cuda -Model medium
#>
[CmdletBinding()]
param(
    [switch]$Cuda,
    [ValidateSet('large-v3-turbo', 'large-v3', 'medium', 'small', 'base')]
    [string]$Model = 'large-v3-turbo',
    [string]$Dest = '.'
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'  # makes Invoke-WebRequest far faster

$whisperDir = Join-Path $Dest 'whisper'
$modelsDir = Join-Path $Dest 'models'
New-Item -ItemType Directory -Force -Path $whisperDir, $modelsDir | Out-Null

function Get-WhisperAsset {
    param([bool]$WantCuda)

    # whisper.cpp publishes binaries on its rolling b#### build tags. The
    # v1.x.y semver tags carry release notes but NO assets, so resolving
    # "latest" by version number silently finds nothing to download.
    $releases = Invoke-RestMethod -Uri 'https://api.github.com/repos/ggml-org/whisper.cpp/releases?per_page=20' `
        -Headers @{ 'User-Agent' = 'godio' }

    $pattern = if ($WantCuda) { '^whisper-bin-win-cuda-.*-x64\.zip$' } else { '^whisper-bin-x64\.zip$' }

    foreach ($release in $releases) {
        $asset = $release.assets | Where-Object { $_.name -match $pattern } | Select-Object -First 1
        if ($asset) {
            return [pscustomobject]@{ Tag = $release.tag_name; Name = $asset.name; Url = $asset.browser_download_url }
        }
    }
    throw "No Windows asset matching $pattern found in the 20 most recent whisper.cpp releases."
}

Write-Host 'Resolving whisper.cpp release...'
$asset = Get-WhisperAsset -WantCuda:$Cuda.IsPresent
Write-Host "  $($asset.Tag) -> $($asset.Name)"

$zip = Join-Path $env:TEMP $asset.Name
Invoke-WebRequest -Uri $asset.Url -OutFile $zip

$extract = Join-Path $env:TEMP ('whisper-' + [guid]::NewGuid())
Expand-Archive -Path $zip -DestinationPath $extract -Force

# The zip's internal layout varies between builds (sometimes a Release\ folder),
# so locate the executable rather than assuming a path.
$exe = Get-ChildItem -Path $extract -Recurse -Filter 'whisper-cli.exe' | Select-Object -First 1
if (-not $exe) {
    $exe = Get-ChildItem -Path $extract -Recurse -Filter 'main.exe' | Select-Object -First 1
}
if (-not $exe) { throw "No whisper-cli.exe found inside $($asset.Name)." }

# Copy the executable together with every DLL beside it; the CUDA builds in
# particular will not start without their runtime libraries.
Copy-Item -Path (Join-Path $exe.DirectoryName '*') -Destination $whisperDir -Recurse -Force
if ($exe.Name -eq 'main.exe') {
    Move-Item -Path (Join-Path $whisperDir 'main.exe') -Destination (Join-Path $whisperDir 'whisper-cli.exe') -Force
}
Remove-Item $zip, $extract -Recurse -Force

function Get-Model {
    param([string]$Url, [string]$Path, [string]$Label)
    if (Test-Path $Path) {
        Write-Host "  $Label already present"
        return
    }
    Write-Host "  downloading $Label..."
    Invoke-WebRequest -Uri $Url -OutFile $Path
}

Write-Host 'Fetching models...'
Get-Model -Label "ggml-$Model" `
    -Url "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-$Model.bin" `
    -Path (Join-Path $modelsDir "ggml-$Model.bin")

# Silero VAD: skips the long silences where the other party is talking, and
# stops Whisper hallucinating sentences over near-silence.
Get-Model -Label 'ggml-silero-v5.1.2' `
    -Url 'https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v5.1.2.bin' `
    -Path (Join-Path $modelsDir 'ggml-silero-v5.1.2.bin')

Write-Host ''
Write-Host 'Done. Next:'
Write-Host '  .\godio.exe doctor'
if ($Model -ne 'large-v3-turbo') {
    Write-Host ''
    Write-Host "Set whisper.model = `"models/ggml-$Model.bin`" in config.toml."
}
