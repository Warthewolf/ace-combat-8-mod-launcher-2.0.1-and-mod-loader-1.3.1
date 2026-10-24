param(
    [string]$VisualStudio = 'D:\Coding\Microsoft Visual Studio\18\Community',
    [string]$Compiler = 'gcc.exe',
    [string]$OutDir = (Join-Path $PSScriptRoot '../build')
)
$ErrorActionPreference = 'Stop'

# The source uses GNU C extensions and assembly. Compile with x64 MinGW GCC,
# then use VS lib/link with the same explicit imports as the LLVM build.
$msvc = Get-ChildItem (Join-Path $VisualStudio 'VC/Tools/MSVC') -Directory |
    Sort-Object { [version]$_.Name } -Descending | Select-Object -First 1
if (!$msvc) { throw 'Visual Studio C++ tools not found' }
$toolDir = Join-Path $msvc.FullName 'bin/Hostx64/x64'
$libTool = Join-Path $toolDir 'lib.exe'
$linkTool = Join-Path $toolDir 'link.exe'
$gcc = (Get-Command $Compiler -ErrorAction Stop).Source
$triple = & $gcc -dumpmachine
if ($LASTEXITCODE -ne 0 -or $triple -notmatch '^x86_64.*mingw') {
    throw "Expected an x64 MinGW GCC, got: $triple"
}
New-Item -ItemType Directory -Path $OutDir -Force | Out-Null
$OutDir = (Resolve-Path $OutDir).Path
foreach ($name in 'kernel32', 'ue4ss', 'ucrtheap') {
    & $libTool /nologo /machine:x64 "/def:$PSScriptRoot/$name.def" "/out:$OutDir/$name.lib"
    if ($LASTEXITCODE -ne 0) { throw "Import library failed: $name" }
}
& $gcc -O2 -ffreestanding -fno-builtin -fno-stack-protector -funwind-tables `
    -Wall -Wextra -Werror -c "$PSScriptRoot/loader.c" -o "$OutDir/loader.obj"
if ($LASTEXITCODE -ne 0) { throw 'C compilation failed' }
& $linkTool /nologo /dll /nodefaultlib /entry:DllMain /subsystem:windows /machine:x64 `
    /export:start_mod /export:uninstall_mod `
    "/out:$OutDir/main.dll" "/implib:$OutDir/main_exports.lib" `
    "$OutDir/loader.obj" "$OutDir/kernel32.lib" "$OutDir/ue4ss.lib" "$OutDir/ucrtheap.lib"
if ($LASTEXITCODE -ne 0) { throw 'DLL link failed' }
Write-Output "Built $OutDir/main.dll"
