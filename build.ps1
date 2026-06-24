# build.ps1 - compile the ICM host with the C# compiler that ships with Windows. No SDK, no NuGet,
# no MSBuild: we call the in-box .NET Framework csc.exe directly. Produces one exe:
#   ratchet.exe   console CLI / operator console   (src\Cli\ has its Main; -target:exe)
#
# -noconfig ignores the machine csc.rsp so the build is deterministic; -langversion:5 pins the language
# to what this in-box (pre-Roslyn) compiler supports. Non-default reference: System.Web.Extensions.dll
# (JavaScriptSerializer, the JSON layer).
#
#   powershell -ExecutionPolicy Bypass -File build.ps1

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path

$csc = "C:\Windows\Microsoft.NET\Framework64\v4.0.30319\csc.exe"
if (-not (Test-Path $csc)) { $csc = "C:\Windows\Microsoft.NET\Framework\v4.0.30319\csc.exe" }
if (-not (Test-Path $csc)) { throw "csc.exe (.NET Framework C# compiler) not found. Is .NET Framework 4.x installed?" }

$src = Get-ChildItem "$root\src" -Recurse -Filter *.cs | ForEach-Object { $_.FullName }

& $csc -nologo -noconfig -optimize+ -langversion:5 -warn:4 -target:exe -platform:anycpu `
    "-reference:System.dll" "-reference:System.Core.dll" "-reference:System.Web.Extensions.dll" `
    "-out:$root\ratchet.exe" $src
if ($LASTEXITCODE -ne 0) { throw "build failed (csc exit $LASTEXITCODE)" }
Write-Host "built $root\ratchet.exe"
