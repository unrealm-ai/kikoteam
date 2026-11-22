import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import esbuild from 'esbuild';
import {
  buildPrincipalRefsFromIds,
  buildUnifiedMemberAndDeptOptions,
  mergeConnectionDirectoryData,
  normalizeTriggerAllowedActions,
  resolveMemberKeyToCanonicalKey
} from '../src/direction2/utils/workforce.ts';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const ROOT = path.resolve(__dirname, '..');

async function loadTsModule(relEntry) {
  const result = esbuild.buildSync({
    entryPoints: [path.join(ROOT, relEntry)],
    bundle: true,
    write: false,
    format: 'esm',
    platform: 'node'
  });
  const code = result.outputFiles[0].text;
  return import(`data:text/javascript;base64,${Buffer.from(code, 'utf-8').toString('base64')}`);
}

const { D2MockBackend } = await loadTsModule('src/direction2/mock-server/handler.ts');
const { toTriggerConfigInput } = await loadTsModule('src/direction2/api/index.ts');

async function getDefaultWsId(backend) {
  const wsRes = await backend.handle('GET', '/api/workspaces');
  assert.equal(wsRes.status, 200);
  return wsRes.body[0].id;
}

function readRel(relPath) {
  return fs.readFileSync(path.join(ROOT, relPath), 'utf-8');
}

test('Issue 1: Multi-connection members/departments with identical bare IDs keep canonical connection identity end-to-end', async () => {
  const resA = {
    sourceType: 'feishu',
    connectionId: 'conn-feishu-A',
    queryMode: 'snapshot',
    page: 1,
    pageSize: 20,
    totalUsers: 1,
    departments: [{ id: 'dept-shared-1', refId: 'od_shared_01', name: '研发部', memberCount: 5, platform: 'feishu' }],
    users: [{ id: 'dir-shared-1', refId: 'fs_ou_alex_01', name: 'Alex (连接A)', department: '研发部', email: 'a@corp', platform: 'feishu', avatar: 'A' }]
  };
  const resB = {
    sourceType: 'feishu',
    connectionId: 'conn-feishu-B',
    queryMode: 'snapshot',
    page: 1,
    pageSize: 20,
    totalUsers: 1,
    departments: [{ id: 'dept-shared-1', refId: 'od_shared_01', name: '研发部', memberCount: 8, platform: 'feishu' }],
    users: [{ id: 'dir-shared-1', refId: 'fs_ou_alex_01', name: 'Alex (连接B)', department: '研发部', email: 'b@corp', platform: 'feishu', avatar: 'B' }]
  };

  let merged = mergeConnectionDirectoryData([], [], resA);
  merged = mergeConnectionDirectoryData(merged.users, merged.departments, resB);
  assert.equal(merged.users.length, 2, 'Both connection A and B users must be retained');
  assert.equal(merged.departments.length, 2, 'Both connection A and B departments must be retained');

  const fakeEmp = {
    channels: [{ platform: 'feishu', connectionId: 'conn-feishu-B', status: 'connected' }],
    unifiedAccess: { mode: 'whitelist', whitelistMemberIds: [], blacklistMemberIds: [], whitelistTargets: [], blacklistTargets: [] }
  };
  const groups = buildUnifiedMemberAndDeptOptions(fakeEmp, merged.users, merged.departments);
  const deptValues = groups[0].children.map(c => c.value);
  const userValues = groups[1].children.map(c => c.value);
  assert.ok(deptValues.includes('feishu::conn-feishu-A::department::od_shared_01'));
  assert.ok(deptValues.includes('feishu::conn-feishu-B::department::od_shared_01'));
  assert.ok(userValues.includes('feishu::conn-feishu-A::user::fs_ou_alex_01'));
  assert.ok(userValues.includes('feishu::conn-feishu-B::user::fs_ou_alex_01'));

  const pickedBKey = 'feishu::conn-feishu-B::user::fs_ou_alex_01';
  const refs = buildPrincipalRefsFromIds([pickedBKey], [], merged.users, merged.departments);
  assert.equal(refs.length, 1);
  assert.equal(refs[0].connectionId, 'conn-feishu-B');
  assert.equal(refs[0].displayName, 'Alex (连接B)');
  assert.equal(refs[0].canonicalKey, pickedBKey);

  const resolvedFromRef = resolveMemberKeyToCanonicalKey('fs_ou_alex_01', refs, merged.users, merged.departments);
  assert.equal(resolvedFromRef, pickedBKey, 'Must resolve to connection B, never connection A');

  // Backend PUT rules preserves connection B principal cleanly
  const backend = new D2MockBackend();
  const wsId = await getDefaultWsId(backend);
  const empRes = await backend.handle('GET', `/api/workspaces/${wsId}/entities/emp-01`);
  assert.equal(empRes.status, 200);
  const version = empRes.body.version;

  const saveRes = await backend.handle(
    'PUT',
    `/api/workspaces/${wsId}/entities/emp-01/rules`,
    JSON.stringify({
      expectedVersion: version,
      unifiedAccess: {
        mode: 'whitelist',
        whitelistTargets: [
          {
            targetType: 'user',
            channel: 'feishu',
            connectionId: 'conn-feishu-B',
            targetId: 'fs_ou_alex_01',
            displayName: 'Alex (连接B)',
            canonicalKey: 'feishu::conn-feishu-B::user::fs_ou_alex_01'
          }
        ],
        blacklistTargets: []
      }
    })
  );
  assert.equal(saveRes.status, 200);
  assert.equal(saveRes.body.unifiedAccess.whitelistTargets[0].connectionId, 'conn-feishu-B');
  assert.equal(saveRes.body.unifiedAccess.whitelistTargets[0].canonicalKey, 'feishu::conn-feishu-B::user::fs_ou_alex_01');
  assert.equal(saveRes.body.unifiedAccess.whitelistMemberIds[0], 'feishu::conn-feishu-B::user::fs_ou_alex_01');
});

test('Issue 2: Invalid Cron expressions are blocked on both frontend and mock backend', async () => {
  const backend = new D2MockBackend();
  const wsId = await getDefaultWsId(backend);
  const empRes = await backend.handle('GET', `/api/workspaces/${wsId}/entities/emp-01`);
  assert.equal(empRes.status, 200);
  const emp = empRes.body;

  const badTriggers = [
    {
      id: 'trg-bad-cron',
      name: '错误定时任务',
      type: 'schedule',
      enabled: true,
      allowedActions: ['create_task'],
      cron: 'bad cron',
      timezone: 'Asia/Shanghai',
      description: 'test',
      historyPolicy: 'isolated',
      notificationTargets: []
    }
  ];

  const trgPutRes = await backend.handle(
    'PUT',
    `/api/workspaces/${wsId}/entities/emp-01/triggers`,
    JSON.stringify({ triggers: badTriggers, expectedVersion: emp.version })
  );
  assert.equal(trgPutRes.status, 400);
  assert.equal(trgPutRes.body.error.code, 'INVALID_CRON');

  const fullPutRes = await backend.handle(
    'PUT',
    `/api/workspaces/${wsId}/entities/emp-01`,
    JSON.stringify({ triggers: badTriggers, expectedVersion: emp.version })
  );
  assert.equal(fullPutRes.status, 400);
  assert.equal(fullPutRes.body.error.code, 'INVALID_CRON');

  const emptyCronRes = await backend.handle(
    'PUT',
    `/api/workspaces/${wsId}/entities/emp-01/triggers`,
    JSON.stringify({
      triggers: [{ ...badTriggers[0], cron: '   ' }],
      expectedVersion: emp.version
    })
  );
  assert.equal(emptyCronRes.status, 400);
  assert.equal(emptyCronRes.body.error.code, 'INVALID_CRON');

  const triggersTab = readRel('src/direction2/components/workforce/WorkforceTriggersTab.vue');
  assert.match(triggersTab, /isScheduleCronInvalid/);
  assert.match(triggersTab, /:disabled="isScheduleCronInvalid"/);
});

test('Issue 3: Trigger notification group picker uses explicit connected channel/connection and empty default', () => {
  const triggersTab = readRel('src/direction2/components/workforce/WorkforceTriggersTab.vue');
  assert.ok(!triggersTab.includes('activeChannelPlatform'), 'Triggers tab must not depend on activeChannelPlatform from Channels tab');
  assert.match(triggersTab, /selectedNotifyChannelKey\.value\s*=\s*''/);
  assert.match(triggersTab, /notificationTargets:\s*\[\]/);
  assert.match(triggersTab, /connectedChannelOptions/);
});

test('Issue 4: Webhook endpoint/Key ID are read-only, secret regeneration reveals one-time secret and syncs version', async () => {
  const backend = new D2MockBackend();
  const wsId = await getDefaultWsId(backend);
  const empRes = await backend.handle('GET', `/api/workspaces/${wsId}/entities/emp-01`);
  const emp = empRes.body;
  const webhookTrg = emp.triggers.find(t => t.type === 'webhook');
  assert.ok(webhookTrg, 'Seed data must have a webhook trigger on emp-01');
  const oldVersion = emp.version;

  const regenRes = await backend.handle(
    'POST',
    `/api/workspaces/${wsId}/entities/emp-01/triggers/${encodeURIComponent(webhookTrg.id)}/regenerate-secret`,
    JSON.stringify({ allowDraft: false, expectedVersion: oldVersion })
  );
  assert.equal(regenRes.status, 200);
  assert.ok(typeof regenRes.body.secret === 'string' && regenRes.body.secret.startsWith('whsec_'));
  assert.ok(!regenRes.body.secret.includes('•'), 'Returned secret must be unmasked plaintext once');
  assert.ok(regenRes.body.trigger.secretMasked.includes('•'), 'Stored trigger only retains masked secret');
  assert.equal(regenRes.body.version, oldVersion + 1);

  // Stale save fails 409; synced version save succeeds 200
  const staleSave = await backend.handle(
    'PUT',
    `/api/workspaces/${wsId}/entities/emp-01/triggers`,
    JSON.stringify({
      expectedVersion: oldVersion,
      triggers: emp.triggers.map(({ executionLogs, secretMasked, keyId, webhookEndpoint, nextExecutionTimes, ...rest }) => rest)
    })
  );
  assert.equal(staleSave.status, 409);

  const syncedSave = await backend.handle(
    'PUT',
    `/api/workspaces/${wsId}/entities/emp-01/triggers`,
    JSON.stringify({
      expectedVersion: regenRes.body.version,
      triggers: emp.triggers.map(({ executionLogs, secretMasked, keyId, webhookEndpoint, nextExecutionTimes, ...rest }) => rest)
    })
  );
  assert.equal(syncedSave.status, 200);
  assert.equal(syncedSave.body.version, regenRes.body.version + 1);

  const triggersTab = readRel('src/direction2/components/workforce/WorkforceTriggersTab.vue');
  assert.match(triggersTab, /generatedPlaintextSecret/);
  assert.match(triggersTab, /syncServerFieldIntoSnapshot/);
});

test('Issue 5: Run trigger returns accepted execution receipt immediately, supports Idempotency-Key replay, and completes asynchronously', async () => {
  const backend = new D2MockBackend();
  const wsId = await getDefaultWsId(backend);
  const empRes = await backend.handle('GET', `/api/workspaces/${wsId}/entities/emp-01`);
  const trg = empRes.body.triggers[0];
  const initialLogsCount = trg.executionLogs.length;

  const firstRun = await backend.handle(
    'POST',
    `/api/workspaces/${wsId}/entities/emp-01/triggers/${encodeURIComponent(trg.id)}/run`,
    JSON.stringify({ expectedVersion: empRes.body.version }),
    { 'idempotency-key': 'idem-run-trigger-abc' }
  );
  assert.equal(firstRun.status, 202);
  assert.equal(firstRun.body.status, 'accepted');
  assert.ok(firstRun.body.operationId);
  assert.ok(firstRun.body.data.executionId);
  assert.equal(firstRun.body.data.executionLog.status, 'running');
  assert.equal(firstRun.body.data.task.status, 'running');

  // Replay with identical Idempotency-Key returns same operation without duplicating task/log
  const replayRun = await backend.handle(
    'POST',
    `/api/workspaces/${wsId}/entities/emp-01/triggers/${encodeURIComponent(trg.id)}/run`,
    JSON.stringify({ expectedVersion: empRes.body.version }),
    { 'idempotency-key': 'idem-run-trigger-abc' }
  );
  assert.equal(replayRun.status, 202);
  assert.equal(replayRun.body.replayed, true);
  assert.equal(replayRun.body.operationId, firstRun.body.operationId);
  assert.equal(replayRun.body.data.executionId, firstRun.body.data.executionId);

  const midEmp = await backend.handle('GET', `/api/workspaces/${wsId}/entities/emp-01`);
  const midTrg = midEmp.body.triggers.find(t => t.id === trg.id);
  assert.equal(midTrg.executionLogs.length, initialLogsCount + 1, 'Replay must not create duplicate execution log');

  // Reusing same Idempotency-Key with conflicting payload returns 409
  const conflictRun = await backend.handle(
    'POST',
    `/api/workspaces/${wsId}/entities/emp-01/triggers/${encodeURIComponent(trg.id)}/run`,
    JSON.stringify({ expectedVersion: 999 }),
    { 'idempotency-key': 'idem-run-trigger-abc' }
  );
  assert.equal(conflictRun.status, 409);
  assert.equal(conflictRun.body.error.code, 'IDEMPOTENCY_CONFLICT');

  // Wait past async completion duration and poll GET /operations/:opId
  await new Promise(r => setTimeout(r, 1300));
  const pollRes = await backend.handle(
    'GET',
    `/api/workspaces/${wsId}/operations/${encodeURIComponent(firstRun.body.operationId)}`
  );
  assert.equal(pollRes.status, 200);
  assert.equal(pollRes.body.status, 'succeeded');
  assert.equal(pollRes.body.data.executionLog.status, 'succeeded');
  assert.equal(pollRes.body.data.task.status, 'succeeded');
});

test('Issue 6: No hardcoded connection IDs or demo model IDs; unconfigured workspace rejects model-less creation', async () => {
  const wfView = readRel('src/direction2/views/WorkforceView.vue');
  const dirList = readRel('src/direction2/components/workforce/WorkforceDirectoryList.vue');
  const resView = readRel('src/direction2/views/ResourcesView.vue');

  for (const connId of ['conn-emp-01-feishu', 'conn-emp-01-dingtalk', 'ww_wecom_sec_01']) {
    assert.ok(!wfView.includes(connId), `WorkforceView.vue must not hardcode ${connId}`);
    assert.ok(!resView.includes(connId), `ResourcesView.vue must not hardcode ${connId}`);
  }
  assert.ok(!dirList.includes('deepseek-v3'), 'WorkforceDirectoryList.vue must not hardcode deepseek-v3');
  assert.ok(!dirList.includes('DeepSeek-V3'), 'WorkforceDirectoryList.vue must not hardcode DeepSeek-V3');

  const backend = new D2MockBackend();
  const wsId = await getDefaultWsId(backend);
  const badModelRes = await backend.handle(
    'POST',
    `/api/workspaces/${wsId}/entities`,
    JSON.stringify({ name: '新员工', modelId: 'deepseek-v3-nonexistent' })
  );
  assert.equal(badModelRes.status, 400);
  assert.equal(badModelRes.body.error.code, 'INVALID_MODEL');

  // Disable all workspace models and verify creation fails with NO_AVAILABLE_MODEL
  const modelsRes = await backend.handle('GET', `/api/workspaces/${wsId}/models`);
  for (const m of modelsRes.body) {
    if (m.enabled) {
      await backend.handle('PUT', `/api/workspaces/${wsId}/models/${encodeURIComponent(m.id)}`, JSON.stringify({ enabled: false }));
    }
  }
  const noModelRes = await backend.handle(
    'POST',
    `/api/workspaces/${wsId}/entities`,
    JSON.stringify({ name: '无模型员工' })
  );
  assert.equal(noModelRes.status, 400);
  assert.equal(noModelRes.body.error.code, 'NO_AVAILABLE_MODEL');
});

test('Issue 7: Unbound channel connections on same platform are disabled; legacy invalid targets display as disabled', () => {
  const resA = {
    sourceType: 'feishu',
    connectionId: 'conn-feishu-A',
    queryMode: 'snapshot',
    page: 1,
    pageSize: 20,
    totalUsers: 1,
    departments: [{ id: 'dept-a', refId: 'od_a', name: 'A部门', memberCount: 3, platform: 'feishu' }],
    users: [{ id: 'user-a', refId: 'ou_a', name: 'Alice A', department: 'A部门', email: 'a@corp', platform: 'feishu', avatar: 'A' }]
  };
  const resB = {
    sourceType: 'feishu',
    connectionId: 'conn-feishu-B',
    queryMode: 'snapshot',
    page: 1,
    pageSize: 20,
    totalUsers: 1,
    departments: [{ id: 'dept-b', refId: 'od_b', name: 'B部门', memberCount: 5, platform: 'feishu' }],
    users: [{ id: 'user-b', refId: 'ou_b', name: 'Bob B', department: 'B部门', email: 'b@corp', platform: 'feishu', avatar: 'B' }]
  };

  let merged = mergeConnectionDirectoryData([], [], resA);
  merged = mergeConnectionDirectoryData(merged.users, merged.departments, resB);

  const staleTarget = {
    targetType: 'user',
    channel: 'feishu',
    connectionId: 'conn-feishu-legacy',
    targetId: 'ou_stale',
    displayName: '旧配置失效人员',
    canonicalKey: 'feishu::conn-feishu-legacy::user::ou_stale'
  };

  const empOnlyB = {
    channels: [{ platform: 'feishu', connectionId: 'conn-feishu-B', status: 'connected' }],
    unifiedAccess: {
      mode: 'whitelist_with_blacklist',
      whitelistMemberIds: [staleTarget.canonicalKey],
      blacklistMemberIds: [],
      whitelistTargets: [staleTarget],
      blacklistTargets: []
    }
  };

  const groups = buildUnifiedMemberAndDeptOptions(empOnlyB, merged.users, merged.departments);
  const deptAOpt = groups[0].children.find(c => c.value === 'feishu::conn-feishu-A::department::od_a');
  const deptBOpt = groups[0].children.find(c => c.value === 'feishu::conn-feishu-B::department::od_b');
  const userAOpt = groups[1].children.find(c => c.value === 'feishu::conn-feishu-A::user::ou_a');
  const userBOpt = groups[1].children.find(c => c.value === 'feishu::conn-feishu-B::user::ou_b');
  const staleOpt = groups[1].children.find(c => c.value === staleTarget.canonicalKey);

  assert.equal(deptAOpt?.disabled, true, 'Unbound Feishu connection A department must be disabled');
  assert.equal(userAOpt?.disabled, true, 'Unbound Feishu connection A user must be disabled');
  assert.equal(deptBOpt?.disabled, false, 'Bound Feishu connection B department must be selectable');
  assert.equal(userBOpt?.disabled, false, 'Bound Feishu connection B user must be selectable');
  assert.ok(staleOpt, 'Legacy stale target must remain present in options for display/removal');
  assert.equal(staleOpt.disabled, true, 'Legacy stale target must be disabled so it cannot be newly selected');

  const pickerVue = readRel('src/direction2/components/UnifiedDirectoryPicker.vue');
  assert.match(pickerVue, /isRowDisabled/);
  assert.match(pickerVue, /!activeSource\.value\.connected/);
});

test('Issue 8: Trigger run polling errors preserve operationId and allow resuming status check without resubmitting', () => {
  const triggersTab = readRel('src/direction2/components/workforce/WorkforceTriggersTab.vue');
  assert.match(triggersTab, /resumePollTriggerOperation/);
  assert.match(triggersTab, /状态暂时获取失败/);
  assert.match(triggersTab, /继续查询状态/);
  assert.match(triggersTab, /pollTimedOut/);
  assert.match(triggersTab, /isTriggerOperationInFlight/);
});

test('Issue 9: Schedule and GitHub triggers normalize allowedActions to create_task and backend rejects invalid actions', async () => {
  assert.deepEqual(normalizeTriggerAllowedActions('schedule', ['create_task', 'supplement_task']), ['create_task']);
  assert.deepEqual(normalizeTriggerAllowedActions('github_issue', ['create_task', 'cancel_task']), ['create_task']);
  assert.deepEqual(normalizeTriggerAllowedActions('webhook', ['create_task', 'supplement_task']), ['create_task', 'supplement_task']);

  const normalizedInput = toTriggerConfigInput({
    id: 'trg-test-sched',
    name: '每日巡检',
    type: 'schedule',
    enabled: true,
    allowedActions: ['create_task', 'supplement_task'],
    cron: '0 9 * * 1-5',
    timezone: 'Asia/Shanghai',
    description: 'test',
    historyPolicy: 'isolated',
    notificationTargets: [],
    executionLogs: []
  });
  assert.deepEqual(normalizedInput.allowedActions, ['create_task']);

  const backend = new D2MockBackend();
  const wsId = await getDefaultWsId(backend);
  const empRes = await backend.handle('GET', `/api/workspaces/${wsId}/entities/emp-01`);
  const emp = empRes.body;

  for (const badType of ['schedule', 'github_issue']) {
    const res = await backend.handle(
      'PUT',
      `/api/workspaces/${wsId}/entities/emp-01/triggers`,
      JSON.stringify({
        expectedVersion: emp.version,
        triggers: [
          {
            id: `trg-bad-${badType}`,
            name: `非法动作-${badType}`,
            type: badType,
            enabled: true,
            allowedActions: ['create_task', 'supplement_task'],
            cron: badType === 'schedule' ? '0 9 * * 1-5' : undefined,
            timezone: 'Asia/Shanghai',
            githubRepo: badType === 'github_issue' ? 'org/repo' : undefined,
            description: 'test',
            historyPolicy: 'isolated',
            notificationTargets: []
          }
        ]
      })
    );
    assert.equal(res.status, 400);
    assert.equal(res.body.error.code, 'INVALID_TRIGGER_ACTIONS');
  }

  const triggersTab = readRel('src/direction2/components/workforce/WorkforceTriggersTab.vue');
  assert.match(triggersTab, /allowedActions:\s*\['create_task'\]/);
  assert.match(triggersTab, /normalizeTriggerAllowedActions/);
});

test('Issue 10: New Webhook draft secret binds to subsequent trigger save via draftSecretToken', async () => {
  const backend = new D2MockBackend();
  const wsId = await getDefaultWsId(backend);
  const empRes = await backend.handle('GET', `/api/workspaces/${wsId}/entities/emp-01`);
  const emp = empRes.body;
  const newTriggerId = 'trg-wh-new-draft-1';

  const draftRes = await backend.handle(
    'POST',
    `/api/workspaces/${wsId}/entities/emp-01/triggers/${newTriggerId}/regenerate-secret`,
    JSON.stringify({ allowDraft: true })
  );
  assert.equal(draftRes.status, 200);
  assert.equal(draftRes.body.isDraft, true);
  assert.ok(draftRes.body.draftSecretToken, 'Draft secret generation must return binding token');
  assert.ok(draftRes.body.secret.startsWith('whsec_'));

  const payloadTrigger = toTriggerConfigInput({
    id: newTriggerId,
    name: '新建闭环 Webhook',
    type: 'webhook',
    enabled: true,
    allowedActions: ['create_task', 'supplement_task'],
    description: 'ci hook',
    historyPolicy: 'isolated',
    notificationTargets: [],
    executionLogs: [],
    draftSecretToken: draftRes.body.draftSecretToken
  });

  const existingInputs = emp.triggers.map(toTriggerConfigInput);
  const saveRes = await backend.handle(
    'PUT',
    `/api/workspaces/${wsId}/entities/emp-01/triggers`,
    JSON.stringify({
      expectedVersion: emp.version,
      triggers: [...existingInputs, payloadTrigger]
    })
  );
  assert.equal(saveRes.status, 200);
  const savedTrg = saveRes.body.triggers.find(t => t.id === newTriggerId);
  assert.ok(savedTrg);
  assert.equal(savedTrg.keyId, draftRes.body.trigger.keyId, 'Saved trigger keyId must match pre-save draft keyId');
  assert.equal(savedTrg.secretMasked, draftRes.body.trigger.secretMasked, 'Saved trigger secretMasked must match pre-save draft secret');
  assert.equal(savedTrg.webhookEndpoint, draftRes.body.trigger.webhookEndpoint);

  // Consumed or invalid draftSecretToken must be rejected for another new webhook
  const badTokenSave = await backend.handle(
    'PUT',
    `/api/workspaces/${wsId}/entities/emp-01/triggers`,
    JSON.stringify({
      expectedVersion: saveRes.body.version,
      triggers: [
        ...existingInputs,
        { ...payloadTrigger, id: 'trg-wh-new-draft-2', draftSecretToken: draftRes.body.draftSecretToken }
      ]
    })
  );
  assert.equal(badTokenSave.status, 400);
  assert.equal(badTokenSave.body.error.code, 'INVALID_DRAFT_SECRET_TOKEN');
});

test('Issue 12: Group availability switch (allowGroupMembers) is unified under unifiedAccess, defaults to true, and Feishu group scope selector is removed', async () => {
  const backend = new D2MockBackend();
  const wsId = await getDefaultWsId(backend);

  const empRes = await backend.handle('GET', `/api/workspaces/${wsId}/entities/emp-01`);
  assert.equal(empRes.status, 200);
  assert.equal(empRes.body.unifiedAccess.allowGroupMembers, true);

  // Saving rules without allowGroupMembers preserves default ON (true)
  const defaultSave = await backend.handle(
    'PUT',
    `/api/workspaces/${wsId}/entities/emp-01/rules`,
    JSON.stringify({
      expectedVersion: empRes.body.version,
      unifiedAccess: {
        mode: empRes.body.unifiedAccess.mode,
        whitelistTargets: empRes.body.unifiedAccess.whitelistTargets || [],
        blacklistTargets: empRes.body.unifiedAccess.blacklistTargets || []
      }
    })
  );
  assert.equal(defaultSave.status, 200);
  assert.equal(defaultSave.body.unifiedAccess.allowGroupMembers, true);

  // Explicitly disabling allowGroupMembers persists false
  const disableSave = await backend.handle(
    'PUT',
    `/api/workspaces/${wsId}/entities/emp-01/rules`,
    JSON.stringify({
      expectedVersion: defaultSave.body.version,
      unifiedAccess: {
        mode: defaultSave.body.unifiedAccess.mode,
        whitelistTargets: defaultSave.body.unifiedAccess.whitelistTargets || [],
        blacklistTargets: defaultSave.body.unifiedAccess.blacklistTargets || [],
        allowGroupMembers: false
      }
    })
  );
  assert.equal(disableSave.status, 200);
  assert.equal(disableSave.body.unifiedAccess.allowGroupMembers, false);

  const rulesTab = readRel('src/direction2/components/workforce/WorkforceRulesTab.vue');
  assert.match(rulesTab, /<strong>群内可用<\/strong>/);
  assert.match(rulesTab, /allowGroupMembers/);
  assert.match(rulesTab, /NTooltip/);
  assert.match(rulesTab, /NSwitch/);

  const channelsTab = readRel('src/direction2/components/workforce/WorkforceChannelsTab.vue');
  assert.doesNotMatch(channelsTab, /4\. 渠道使用者与群聊可用范围/);
  assert.doesNotMatch(channelsTab, /feishuScope/);
});
