$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$envFile = Join-Path $projectRoot '.env'
if (Test-Path -LiteralPath $envFile) { Write-Output '.env already exists; preserved.'; exit 0 }
function New-LocalSecret { return [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(24)).ToLowerInvariant() }
$settings = @(
    "POSTGRES_PASSWORD=$(New-LocalSecret)", "BUSINESS_PASSWORD=$(New-LocalSecret)",
    "AGENT_PASSWORD=$(New-LocalSecret)", "AGENT_MIGRATION_PASSWORD=$(New-LocalSecret)", "JWT_SECRET=$(New-LocalSecret)",
    "INTERNAL_TOKEN=$(New-LocalSecret)", "DEMO_PASSWORD=$(New-LocalSecret)",
    'PG_HOST_PORT=55432', 'API_HOST_PORT=18080', 'WEB_HOST_PORT=13000',
    'MODEL_PROVIDER=mock', 'MODEL_BASE_URL=', 'MODEL_API_KEY=', 'MODEL_NAME='
)
[IO.File]::WriteAllLines($envFile, $settings, [Text.UTF8Encoding]::new($false))
Write-Output 'Created ignored .env with random local credentials. Demo accounts: demo.admin, demo.operator, demo.supervisor. Read DEMO_PASSWORD locally; do not commit it.'
