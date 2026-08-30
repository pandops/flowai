import fs from "node:fs";
import path from "node:path";
import YAML from "yaml";

const root = path.resolve(import.meta.dirname, "../../..");
const source = path.join(
  root,
  "openspec/specs/state-registry/openapi/test-control.openapi.yaml",
);
const output = path.join(
  root,
  "qa-e2e/api-gateway/fixtures/generated/test-control/client.ts",
);
const document = YAML.parse(fs.readFileSync(source, "utf8"));
const operations = Object.values(document.paths)
  .flatMap((item) => Object.values(item))
  .map((operation) => operation?.operationId)
  .filter(Boolean);
for (const required of [
  "armTransactionBarrier",
  "getTransactionBarrier",
  "releaseTransactionBarrier",
  "cancelTransactionBarrier",
])
  if (!operations.includes(required))
    throw new Error(`missing OpenAPI operationId ${required}`);
fs.mkdirSync(path.dirname(output), { recursive: true });
fs.writeFileSync(
  output,
  `// Generated from openspec/specs/state-registry/openapi/test-control.openapi.yaml. Do not edit.\nexport class TestControlClient {\n  constructor(private readonly baseURL:string,private readonly token:string){}\n  private headers={authorization:\`Bearer \${this.token}\`,\"content-type\":\"application/json\"};\n  async createBarrier(name:string):Promise<{barrier_id:string}>{const response=await fetch(\`\${this.baseURL}/test-control/v1/barriers\`,{method:\"POST\",headers:this.headers,body:JSON.stringify({name})});if(response.status!==201)throw new Error(\`createBarrier: \${response.status} \${await response.text()}\`);return response.json() as Promise<{barrier_id:string}>;}\n  async waitForBarrier(id:string,waitMs=5000):Promise<void>{const response=await fetch(\`\${this.baseURL}/test-control/v1/barriers/\${id}?wait_ms=\${waitMs}\`,{headers:this.headers});if(response.status!==200)throw new Error(\`waitForBarrier: \${response.status} \${await response.text()}\`);const body=await response.json() as {state:string};if(body.state!==\"reached\")throw new Error(\`waitForBarrier: state \${body.state}\`);}\n  async releaseBarrier(id:string):Promise<void>{const response=await fetch(\`\${this.baseURL}/test-control/v1/barriers/\${id}/release\`,{method:\"POST\",headers:this.headers});if(response.status!==200)throw new Error(\`releaseBarrier: \${response.status} \${await response.text()}\`);}\n  async deleteBarrier(id:string):Promise<void>{const response=await fetch(\`\${this.baseURL}/test-control/v1/barriers/\${id}\`,{method:\"DELETE\",headers:this.headers});if(response.status!==204)throw new Error(\`deleteBarrier: \${response.status} \${await response.text()}\`);}\n}\n`,
);
