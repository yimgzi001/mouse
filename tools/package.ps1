# Builds a zip that runs on Windows 10/11 x64 without installing anything:
#   powershell -ExecutionPolicy Bypass -File tools\package.ps1
# It holds GestureMouse.exe, runtime\ with ONNX Runtime and the Visual C++
# runtime it needs, README.txt and the third-party licenses. -NoVCRuntime
# leaves the Visual C++ runtime out; PCs then need vc_redist.x64 installed.
param(
    [string]$OutDir = 'dist',
    [switch]$NoVCRuntime,
    [string]$VCRuntimeDir = (Join-Path $env:SystemRoot 'System32')
)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root
[Environment]::CurrentDirectory = $root

$name = 'GestureMouse'
$stamp = '{0}-{1}' -f (Get-Date -Format 'yyyyMMdd'), (git rev-parse --short HEAD)
if (git status --porcelain --untracked-files=no) { $stamp += '-dirty' }
$out = [IO.Path]::GetFullPath([IO.Path]::Combine($root, $OutDir))
$stage = Join-Path $out $name
$zip = Join-Path $out "$name-$stamp-win-x64.zip"
if (Test-Path $stage) { Remove-Item $stage -Recurse -Force }
$runtime = Join-Path $stage 'runtime'
$licenses = Join-Path $stage 'licenses'
New-Item -ItemType Directory -Force $runtime, $licenses | Out-Null

$env:CGO_ENABLED = '1'
go build -trimpath -ldflags '-H=windowsgui -s -w' -o (Join-Path $stage "$name.exe") ./cmd/mouse
if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
Copy-Item (Join-Path $root 'runtime\onnxruntime.dll'), (Join-Path $root 'runtime\onnxruntime_providers_shared.dll') $runtime

function Get-PEMachine([string]$Path) {
    $stream = [IO.File]::OpenRead($Path)
    try {
        $reader = New-Object IO.BinaryReader($stream)
        $stream.Position = 0x3C
        $stream.Position = $reader.ReadInt32() + 4
        return $reader.ReadUInt16()
    } finally { $stream.Dispose() }
}

if (-not $NoVCRuntime) {
    # onnxruntime.dll comes from a recent MSVC and can crash with a Visual C++
    # runtime older than 14.40, which many PCs still have in System32.
    foreach ($dll in 'msvcp140.dll', 'msvcp140_1.dll', 'vcruntime140.dll', 'vcruntime140_1.dll') {
        $src = Join-Path $VCRuntimeDir $dll
        if (-not (Test-Path $src)) { throw "$src is missing; install vc_redist.x64 or pass -VCRuntimeDir" }
        if ((Get-PEMachine $src) -ne 0x8664) { throw "$src is not a 64-bit library" }
        $v = (Get-Item $src).VersionInfo
        if ($v.FileMajorPart -ne 14 -or $v.FileMinorPart -lt 40) { throw "$src is version $($v.FileVersion); 14.40 or later is needed" }
        Copy-Item $src $runtime
    }
}

# Notepad before Windows 10 1809 shows text without CRLF line ends on one line
# and needs the byte order mark to read UTF-8.
function Copy-Text([string]$From, [string]$To) {
    $text = [IO.File]::ReadAllText($From, [Text.Encoding]::UTF8) -replace "`r?`n", "`r`n"
    [IO.File]::WriteAllText($To, $text, (New-Object Text.UTF8Encoding($true)))
}
Copy-Text (Join-Path $root 'packaging\README.txt') (Join-Path $stage 'README.txt')
$thirdParty = Join-Path $root 'third_party'
Get-ChildItem $thirdParty -Recurse -File | ForEach-Object {
    $rel = $_.FullName.Substring($thirdParty.Length + 1)
    if (-not $_.Extension) { $rel += '.txt' }
    $dst = Join-Path $licenses $rel
    New-Item -ItemType Directory -Force (Split-Path $dst) | Out-Null
    Copy-Text $_.FullName $dst
}

if (Test-Path $zip) { Remove-Item $zip }
Add-Type -AssemblyName System.IO.Compression, System.IO.Compression.FileSystem
$archive = [IO.Compression.ZipFile]::Open($zip, [IO.Compression.ZipArchiveMode]::Create)
try {
    Get-ChildItem $stage -Recurse -File | ForEach-Object {
        # Forward slashes keep the folders when the zip is opened outside Windows.
        $entry = $_.FullName.Substring($out.Length + 1).Replace('\', '/')
        [void][IO.Compression.ZipFileExtensions]::CreateEntryFromFile($archive, $_.FullName, $entry, [IO.Compression.CompressionLevel]::Optimal)
    }
} finally { $archive.Dispose() }
Get-ChildItem $stage -Recurse -File | ForEach-Object { '{0,10}  {1}' -f $_.Length, $_.FullName.Substring($stage.Length + 1) }
'{0}  {1:N1} MB' -f $zip, ((Get-Item $zip).Length / 1MB)
