$ErrorActionPreference = 'Stop'
$flowdeskRoot = Split-Path $PSScriptRoot
$flowdeskSettings = @{}
Get-Content (Join-Path $flowdeskRoot '.env') | ForEach-Object {
    if ($_ -match '^([^#=]+)=(.*)$') { $flowdeskSettings[$matches[1]] = $matches[2] }
}
$env:TEST_DATABASE_URL = 'postgres://flowdesk_owner:' + $flowdeskSettings.POSTGRES_PASSWORD + '@localhost:' + $flowdeskSettings.PG_HOST_PORT + '/flowdesk?sslmode=disable'
Push-Location (Join-Path $flowdeskRoot 'flow-desk-serv/flowdesk')
try {
    $env:GOCACHE = Join-Path (Get-Location) '.gocache'
    go test -count=1 ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go vet failed' }
} finally {
    Remove-Item Env:TEST_DATABASE_URL
    Pop-Location
}
