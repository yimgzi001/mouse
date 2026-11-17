# Converts the hand models bundled with the MediaPipe Python package to ONNX.
# Needs a Python environment with tensorflow and tf2onnx:
#   powershell -ExecutionPolicy Bypass -File tools\convert_models.ps1 -Python <python.exe> -OutDir tmp
param(
    [Parameter(Mandatory = $true)][string]$Python,
    [string]$Modules = 'D:\python31011\Lib\site-packages\mediapipe\modules',
    [string]$OutDir = 'tmp'
)
$ErrorActionPreference = 'Continue'
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$jobs = @(
    @{ src = "$Modules\palm_detection\palm_detection_full.tflite"; dst = "$OutDir\palm_detection_full.onnx" },
    @{ src = "$Modules\hand_landmark\hand_landmark_full.tflite"; dst = "$OutDir\hand_landmark_full.onnx" }
)
foreach ($j in $jobs) {
    & $Python -m tf2onnx.convert --tflite $j.src --output $j.dst --opset 13 2>&1 |
        Select-Object -Last 8 | ForEach-Object { "$_" }
    "CONVERTED_$LASTEXITCODE $($j.dst)"
}
"ALL_DONE"
