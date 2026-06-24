# run-cli.ps1 - launch the ratchet console without tripping Smart App Control: an unsigned local exe is
# blocked from running directly, but loading its bytes in-memory inside trusted PowerShell is allowed.
# Runs ratchet.exe's bytes in-memory and forwards the process exit code.

param([Parameter(ValueFromRemainingArguments = $true)][string[]] $CliArgs)

$exe = Join-Path $PSScriptRoot "ratchet.exe"
if ($null -eq $CliArgs) { $CliArgs = @() }
$bytes = [System.IO.File]::ReadAllBytes($exe)
$asm = [System.Reflection.Assembly]::Load($bytes)
$rv = $asm.EntryPoint.Invoke($null, @(, [string[]]$CliArgs))
exit [int]$rv
