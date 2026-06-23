# project-smoke.ps1 - smoke test for the instance's PROJECT tools (no model needed). Exercises the
# deterministic chain new_project -> build_project -> stage_and_build -> register_file -> read_project
# and asserts the outputs, catching tool regressions (e.g. the $Proj/[string] coercion bug and the
# relative-vs-absolute path bug we hit). Mirrors mcp-smoke.ps1. Exit 0 = all pass.
$ErrorActionPreference = "Stop"
$inst = Join-Path (Split-Path -Parent $MyInvocation.MyCommand.Path) "..\RatchetBox\dotnet4-x"   # the dotnet4-x ratchet in the sibling RatchetBox repo
$name = "__smoke__"
$pass = 0; $fail = 0
function Check($label, [bool]$ok, $detail) {
    if ($ok) { Write-Host ("  ok    " + $label); $script:pass++ }
    else { Write-Host ("  FAIL  " + $label + "  " + $detail); $script:fail++ }
}
function Tool($file, $argv) { return (& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $inst ("tools\" + $file)) @argv) 2>&1 | Out-String }

Push-Location $inst
try {
    if (Test-Path (Join-Path "workspaces" $name)) { Remove-Item -Recurse -Force (Join-Path "workspaces" $name) }

    # 1. scaffold
    $o = Tool "new_project.ps1" @("$name console")
    Check "new_project reports created" ($o -match "OK: created") $o
    Check "project.json exists" (Test-Path "workspaces\$name\project.json") ""
    Check "response.rsp exists" (Test-Path "workspaces\$name\response.rsp") ""

    # 2. project.json is a real JSON OBJECT with name (catches the [string]-param coercion bug)
    $meta = Get-Content -Raw "workspaces\$name\project.json" | ConvertFrom-Json
    Check "project.json parses to an object" ($meta -is [pscustomobject]) ("got " + $meta.GetType().Name)
    Check "project.json.name is correct" ($meta.name -eq $name) ("name=" + $meta.name)

    # 3. build the whole project -> persistent exe in its own dist\
    $o = Tool "build_project.ps1" @($name)
    Check "build_project OK" ($o -match "OK: built") $o
    Check "dist exe produced" (Test-Path "workspaces\$name\dist\$name.exe") ""

    # 4. stage a hand-written Core class and build the whole project
    $thing = "namespace App.Core { public sealed class Thing { public int Answer() { return 42; } } }"
    $o = ($thing | & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $inst "tools\stage_and_build.ps1") -Proj $name -Path "src\Core\Thing.cs") 2>&1 | Out-String
    Check "stage_and_build OK" ($o -match "OK: staged") $o
    Check "staged file on disk" (Test-Path "workspaces\$name\src\Core\Thing.cs") ""

    # path guard: refuse outside src\ or non-.cs
    $o = ("x" | & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $inst "tools\stage_and_build.ps1") -Proj $name -Path "..\evil.cs") 2>&1 | Out-String
    Check "stage_and_build rejects bad path" ($o -match "refusing|must be under") $o

    # 5. register -> project.json.files grows AND stays valid (catches coercion bug)
    $o = Tool "register_file.ps1" @("-Proj", $name, "-Path", "src\Core\Thing.cs", "-Role", "the answer")
    Check "register_file OK" ($o -match "OK: registered") $o
    $meta2 = Get-Content -Raw "workspaces\$name\project.json" | ConvertFrom-Json
    Check "register kept project.json an object" ($meta2 -is [pscustomobject]) ("got " + $meta2.GetType().Name)
    $rec = $false; foreach ($f in $meta2.files) { if ($f.path -eq "src/Core/Thing.cs" -and $f.role -eq "the answer") { $rec = $true } }
    Check "registered file recorded with role" $rec ""

    # 6. read_project shows relative paths with roles, no drift (catches the abs-path Substring bug)
    $o = Tool "read_project.ps1" @($name)
    Check "read_project shows file with role" ($o -match "src/Core/Thing.cs\s+-\s+the answer") $o
    Check "read_project paths are relative (not absolute)" (-not ($o -match "src tree[\s\S]*:\\")) "absolute path leaked into tree"
    Check "read_project no manifest drift" (-not ($o -match "MANIFEST DRIFT")) $o

    Remove-Item -Recurse -Force (Join-Path "workspaces" $name)
}
finally { Pop-Location }

Write-Host ""
if ($fail -eq 0) { Write-Host ("project-smoke: ALL PASS (" + $pass + " checks)"); exit 0 }
Write-Host ("project-smoke: " + $fail + " FAILED, " + $pass + " passed"); exit 2
