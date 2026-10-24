param([string]$Compiler = 'gcc.exe')
$ErrorActionPreference = 'Stop'
$buildDir = Join-Path $PSScriptRoot '../build'
$testDir = Join-Path $buildDir 'pak_windows_test'
New-Item -ItemType Directory -Path $testDir -Force | Out-Null
$testDir = (Resolve-Path $testDir).Path
& $Compiler -shared -Wall -Wextra -Werror "$PSScriptRoot/ue4ss_stub.c" `
    "$PSScriptRoot/ue4ss_stub.def" -o "$testDir/UE4SS.dll"
if ($LASTEXITCODE -ne 0) { throw 'Test stub compilation failed' }
& $Compiler -O2 -Wall -Wextra -Werror "$PSScriptRoot/test_pak_windows.c" -o "$testDir/pak_test.exe"
if ($LASTEXITCODE -ne 0) { throw 'Test host compilation failed' }
foreach ($scenario in 'normal', 'prefix-decoy', 'missing', 'ambiguous', 'other-hook', 'other-short-hook',
        'pak-plan', 'pak-deferred', 'pak-no-object', 'pak-no-entry', 'pak-ambiguous-entry', 'pak-bad-call') {
    $caseRoot = Join-Path $testDir "cases/$scenario"
    $runDir = Join-Path $caseRoot 'dlls'
    New-Item -ItemType Directory -Path $runDir -Force | Out-Null
    foreach ($file in 'UE4SS.dll', 'pak_test.exe') {
        Copy-Item -LiteralPath "$testDir/$file" -Destination "$runDir/$file" -Force
    }
    Copy-Item -LiteralPath "$buildDir/main.dll" -Destination "$runDir/main.dll" -Force
    Push-Location $runDir
    try {
        & './pak_test.exe' $scenario
        if ($LASTEXITCODE -ne 0) { throw "Scenario failed: $scenario (exit $LASTEXITCODE)" }
        Copy-Item -LiteralPath "$caseRoot/AC8IoStoreLoader.log" -Destination "$testDir/$scenario.log" -Force
    } finally { Pop-Location }
}
