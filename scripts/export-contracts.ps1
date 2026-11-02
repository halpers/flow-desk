$ErrorActionPreference = 'Stop'
$flowdeskRoot = Split-Path $PSScriptRoot
Push-Location (Join-Path $flowdeskRoot 'flow-desk-serv/flowdesk')
try {
    goctl api validate --api flowdesk.api
    if ($LASTEXITCODE -ne 0) { throw 'Invalid API' }
    goctl api swagger --api flowdesk.api --dir ../../shared/contracts --filename go.swagger
    if ($LASTEXITCODE -ne 0) { throw 'Swagger export failed' }
} finally { Pop-Location }
Push-Location (Join-Path $flowdeskRoot 'flow-desk-web')
try {
    pnpm.cmd api:sync
    if ($LASTEXITCODE -ne 0) { throw 'Client export failed' }
} finally { Pop-Location }
Push-Location (Join-Path $flowdeskRoot 'flow-desk-agent')
try {
    .venv\Scripts\python.exe scripts/export_contract.py
    if ($LASTEXITCODE -ne 0) { throw 'Agent schema export failed' }
} finally { Pop-Location }
