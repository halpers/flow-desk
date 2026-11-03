param([Parameter(ValueFromRemainingArguments = $true)][string[]]$ComposeArguments)
$ErrorActionPreference = 'Stop'
$flowdeskRoot = Split-Path $PSScriptRoot
$flowdeskComposeFile = 'docker-compose.yaml'
if (-not (Test-Path -LiteralPath (Join-Path $flowdeskRoot $flowdeskComposeFile))) {
    throw "Compose file not found: $flowdeskComposeFile"
}
Push-Location $flowdeskRoot
try {
    if (Get-Command docker -ErrorAction SilentlyContinue) {
        docker compose --file $flowdeskComposeFile @ComposeArguments
    } else {
        if ($flowdeskRoot -notmatch '^[A-Za-z]:\\') { throw 'WSL fallback requires a local drive path' }
        $flowdeskLinuxRoot = '/mnt/' + $flowdeskRoot.Substring(0, 1).ToLowerInvariant() + $flowdeskRoot.Substring(2).Replace('\', '/')
        wsl.exe --cd $flowdeskLinuxRoot --exec docker compose --file $flowdeskComposeFile @ComposeArguments
    }
    if ($LASTEXITCODE -ne 0) { throw 'Docker Compose failed' }
} finally { Pop-Location }
