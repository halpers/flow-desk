$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'bootstrap-local.ps1')
& (Join-Path $PSScriptRoot 'compose.ps1') up --detach --build --wait --wait-timeout 180
if ($LASTEXITCODE -ne 0) { throw 'Startup failed' }
Write-Host 'FlowDesk started. See .env for WEB_HOST_PORT and DEMO_PASSWORD.'
