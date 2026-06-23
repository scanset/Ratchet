# mcp-smoke.ps1 - drive the Ratchet MCP server over stdio exactly as a client would, and assert the
# JSON-RPC responses. Model-free (initialize / tools/list / a real tool call / ping / error), so it
# runs without Ollama. Usage: powershell -NoProfile -File mcp-smoke.ps1 [instanceDir]
param([string] $Dir)
if (-not $Dir) { $Dir = Join-Path $PSScriptRoot "..\RatchetBox\dotnet4-x" }   # the dotnet4-x ratchet in the sibling RatchetBox repo; pass -Dir to override

# NOTE: do not set ErrorActionPreference=Stop here - the server writes a one-line banner to stderr at
# startup, which PowerShell 5.1 wraps as a NativeCommandError; under Stop that would abort the run.
$launcher = Join-Path $PSScriptRoot "ratchet.cmd"

# A real client's opening handshake, then a few tool calls. One JSON object per line.
# We exercise a declared instance tool (csc_check, the C# compile oracle) - the working MCP surface.
# (KB-browse built-ins catalog/read_entry are not asserted here: they are pending a repoint from the
# retired root manifest to the per-library kb manifest, and are not offered for current instances.)
$msgs = @(
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"mcp-smoke","version":"1.0"}}}'
  '{"jsonrpc":"2.0","method":"notifications/initialized"}'
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"csc_check","arguments":{"code":"namespace N { public class C { public int X() { return 1; } } }"}}}'
  '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"csc_check","arguments":{"code":"namespace N { public class C { this is not valid csharp } }"}}}'
  '{"jsonrpc":"2.0","id":5,"method":"ping"}'
  '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"does_not_exist","arguments":{}}}'
)

$raw = $msgs | & $launcher mcp $Dir 2>$null
$resp = @{}
foreach ($line in $raw) {
  $t = "$line".Trim()
  if ($t.Length -eq 0) { continue }
  try { $o = $t | ConvertFrom-Json } catch { continue }
  if ($null -ne $o.id) { $resp[[int]$o.id] = $o }
}

$fails = 0
function Check($name, $cond) {
  if ($cond) { Write-Host ("  ok    " + $name) }
  else { Write-Host ("  FAIL  " + $name); $script:fails++ }
}

# 1: initialize echoes the client's protocol version + advertises tools capability + serverInfo
$init = $resp[1]
Check "initialize: protocolVersion echoed" ($init -and $init.result.protocolVersion -eq "2025-06-18")
Check "initialize: tools capability"        ($init -and $null -ne $init.result.capabilities.tools)
Check "initialize: serverInfo.name"          ($init -and $init.result.serverInfo.name)

# 2: tools/list advertises the instance's declared tools (csc_check among them)
$tools = @(); if ($resp[2]) { $tools = $resp[2].result.tools.name }
Check "tools/list: csc_check advertised"   ($tools -contains "csc_check")
Check "tools/list: several tools present"  ($tools.Count -ge 3)

# 3: a valid compilation unit passes the oracle (content, not an error)
$ok = $resp[3]
Check "csc_check (valid): returns content" ($ok -and $ok.result.content[0].text.Length -gt 0)
Check "csc_check (valid): not an error"    ($ok -and -not $ok.result.isError)

# 4: an invalid unit fails the oracle (isError set, diagnostics returned)
$bad = $resp[4]
Check "csc_check (invalid): flagged as error" ($bad -and $bad.result.isError -eq $true)

# 5: ping ok
Check "ping: ok" ($resp[5] -and $null -ne $resp[5].result)

# 6: unknown tool -> JSON-RPC error (-32602)
$err = $resp[6]
Check "unknown tool: error -32602" ($err -and $err.error -and $err.error.code -eq -32602)

Write-Host ""
if ($fails -eq 0) { Write-Host "mcp-smoke: ALL PASS"; exit 0 }
else { Write-Host ("mcp-smoke: " + $fails + " FAILED"); exit 1 }
