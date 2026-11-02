import fs from 'node:fs/promises';
import openapiTS, {astToString} from 'openapi-typescript';
import converter from 'swagger2openapi';
const input = new URL('../../shared/contracts/go.openapi.json', import.meta.url);
const output = new URL('../src/shared/api/generated.ts', import.meta.url);
const swagger=JSON.parse(await fs.readFile(new URL('../../shared/contracts/go.swagger.json',import.meta.url),'utf8'));
// goctl's wall-clock extension makes otherwise identical exports differ.
delete swagger['x-date'];
await fs.writeFile(new URL('../../shared/contracts/go.swagger.json',import.meta.url),JSON.stringify(swagger,null,2)+'\n');
const {openapi}=await converter.convertObj(swagger,{});
openapi['x-generated-by']='goctl 1.10.2 + swagger2openapi 7.0.8; edit .api sources';
await fs.writeFile(input,JSON.stringify(openapi,null,2)+'\n');
await fs.writeFile(new URL('../../flow-desk-agent/src/flowdesk_agent/tools/go.openapi.json',import.meta.url),JSON.stringify(openapi,null,2)+'\n');
const nodes = await openapiTS(input);
await fs.mkdir(new URL('../src/shared/api/', import.meta.url), {recursive:true});
await fs.writeFile(output, '/** Generated from Go .api via OpenAPI. DO NOT EDIT. */\n'+astToString(nodes));
