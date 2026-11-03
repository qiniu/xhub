import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { checkCompatibility } from './check-contract-compatibility.mjs';
const binary = process.env.OASDIFF_BIN || 'oasdiff';

/** 创建最小真实Git仓库和契约基线；调用方负责finally删除，兼容检查运行真实oasdiff，无成功mock。 */
function fixture() {
  const root = mkdtempSync(join(tmpdir(), 'xhub-compat-test-'));
  const spec = {
  "openapi": "3.0.3",
  "info": {
    "title": "Contract fixture",
    "version": "1.0"
  },
  "paths": {
    "/items": {
      "post": {
        "operationId": "createItem",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "$ref": "#/components/schemas/Input"
              }
            }
          }
        },
        "responses": {
          "200": {
            "description": "Created",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "required": [
                    "id"
                  ],
                  "properties": {
                    "id": {
                      "type": "string"
                    }
                  }
                }
              }
            }
          }
        }
      }
    }
  },
  "components": {
    "schemas": {
      "Input": {
        "type": "object",
        "required": [
          "name"
        ],
        "properties": {
          "name": {
            "type": "string"
          }
        }
      }
    }
  }
};
  mkdirSync(join(root, 'contracts/items'), { recursive: true });
  const path = join(root, 'contracts/items/openapi.json');
  writeFileSync(path, JSON.stringify(spec));
  for (const args of [['init','-q'], ['add','.'], ['-c','user.name=Contract Test','-c','user.email=contract@example.test','commit','-qm','baseline']]) execFileSync('git', args, {cwd:root});
  return {root, path, spec};
}

/** 兼容正例：不变、文档编辑、新增可选输入和新域均通过真实工具；每例隔离Git并清理。 */
test('real comparator accepts compatible evolution', () => {
  const f = fixture();
  try {
    assert.equal(checkCompatibility(f.root, 'HEAD', binary).length, 1);
    f.spec.info.description = 'Documentation only';
    f.spec.components.schemas.Input.properties.note = {type:'string'};
    writeFileSync(f.path, JSON.stringify(f.spec));
    mkdirSync(join(f.root, 'contracts/new'));
    writeFileSync(join(f.root, 'contracts/new/openapi.json'), JSON.stringify(f.spec));
    const result = checkCompatibility(f.root, 'HEAD', binary);
    assert.equal(result.length, 2);
    assert.equal(result.filter(r => r.added).length, 1);
  } finally { rmSync(f.root, { recursive:true }); }
});

/** 真实破坏性变更矩阵；使用$ref、必填、类型及响应变化验证工具确实拒绝，finally清理。 */
for (const [name, mutate] of [
  ['remove endpoint', s => { s.paths['/other'] = s.paths['/items']; delete s.paths['/items']; }],
  ['require request field', s => { s.components.schemas.Input.properties.extra = {type:'string'}; s.components.schemas.Input.required.push('extra'); }],
  ['change referenced request type', s => { s.components.schemas.Input.properties.name.type = 'integer'; }],
  ['remove response property', s => { delete s.paths['/items'].post.responses['200'].content['application/json'].schema.properties.id; }],
  ['change response type', s => { s.paths['/items'].post.responses['200'].content['application/json'].schema.properties.id.type = 'integer'; }],
]) test(`real comparator rejects ${name}`, () => {
  const f = fixture();
  try { mutate(f.spec); writeFileSync(f.path, JSON.stringify(f.spec)); assert.throws(() => checkCompatibility(f.root, 'HEAD', binary), /Compatibility check failed/); }
  finally { rmSync(f.root, {recursive:true}); }
});

/** 基线、缺文件、非法格式、工具不可用及零操作不能伪通过；逐项恢复前置文件，临时Git最终清理。 */
test('gate rejects infrastructure and missing contract evidence', () => {
  const f = fixture();
  try {
    assert.throws(() => checkCompatibility(f.root, '', binary), /baseline/i);
    assert.throws(() => checkCompatibility(f.root, 'missing-ref', binary));
    assert.throws(() => checkCompatibility(f.root, 'HEAD', join(f.root,'missing-tool')), /Compatibility check failed/);
    writeFileSync(f.path, '{');
    assert.throws(() => checkCompatibility(f.root, 'HEAD', binary), SyntaxError);
    writeFileSync(f.path, JSON.stringify({...f.spec, paths:{}}));
    assert.throws(() => checkCompatibility(f.root, 'HEAD', binary), /Empty contract/);
    f.spec.components.schemas.Input.properties.name = {$ref:'https://example.invalid/schema'};
    writeFileSync(f.path, JSON.stringify(f.spec));
    assert.throws(() => checkCompatibility(f.root, 'HEAD', binary), /Non-local reference/);
    rmSync(f.path);
    assert.throws(() => checkCompatibility(f.root, 'HEAD', binary), /Deleted contract/);
    execFileSync('git', ['add','-u'], {cwd:f.root});
    execFileSync('git', ['-c','user.name=Test','-c','user.email=t@example.test','commit','-qm','empty'], {cwd:f.root});
    assert.throws(() => checkCompatibility(f.root, 'HEAD', binary), /zero contracts/);
  } finally { rmSync(f.root, { recursive:true }); }
});
