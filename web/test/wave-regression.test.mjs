import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const rootDir = path.resolve(__dirname, '..');
const srcDir = path.join(rootDir, 'src');

function readRel(relPath) {
  return fs.readFileSync(path.join(rootDir, relPath), 'utf8');
}

function collectFiles(dir) {
  const out = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) out.push(...collectFiles(full));
    else out.push(full);
  }
  return out;
}

test('Wave 1: legacy v1 mode, pages, and toggle entries are completely removed', () => {
  const legacyPaths = [
    'src/app',
    'src/features',
    'src/shared',
    'src/styles',
    'src/api.ts',
    'src/phase2.ts',
    'src/EntityWizard.vue',
    'src/EmployeeSettings.vue',
    'src/EntityWorkspace.vue',
    'src/ConversationList.vue',
    'src/ModelSettings.vue',
    'src/TaskPanel.vue',
    'src/AccessSettings.vue',
    'src/EmployeeTasks.vue'
  ];
  for (const rel of legacyPaths) {
    assert.equal(fs.existsSync(path.join(rootDir, rel)), false, `Legacy path should be removed: ${rel}`);
  }

  const appVue = readRel('src/App.vue');
  assert.match(appVue, /Direction2App/);
  assert.doesNotMatch(appVue, /kiko:direction2|showDirection2|toggleD2|legacy-v2-float-btn/);

  const mainTs = readRel('src/main.ts');
  assert.doesNotMatch(mainTs, /tokens\.css|frontend-v1\.css/);
});

test('Wave 1: every relative import across src/ resolves to an existing file', () => {
  const files = collectFiles(srcDir).filter(f => /\.(ts|tsx|vue|js|mjs)$/.test(f));
  const importRe = /(?:from|import)\s+['"](\.[^'"]+)['"]/g;

  for (const file of files) {
    const content = fs.readFileSync(file, 'utf8');
    let match;
    while ((match = importRe.exec(content)) !== null) {
      const spec = match[1];
      const base = path.resolve(path.dirname(file), spec);
      const candidates = [
        base,
        `${base}.ts`,
        `${base}.vue`,
        `${base}.js`,
        path.join(base, 'index.ts'),
        path.join(base, 'index.js')
      ];
      assert.equal(
        candidates.some(c => fs.existsSync(c)),
        true,
        `Unresolved import "${spec}" in ${path.relative(rootDir, file)}`
      );
    }
  }
});

test('Task center and pending count require real Platform mode while the demo keeps the link', () => {
  const shell = readRel('src/direction2/Direction2App.vue');
  assert.match(shell, /<TasksAndCollaborationView v-else-if="realPlatform && currentView === 'tasks'"/);
  assert.match(shell, /v-else-if="currentView === 'tasks'" type="info" title="任务与协同中心需要真实后端"/);
  assert.match(shell, /v-if="realPlatform"\s+class="reminder-pill-btn"/);
  assert.match(shell, /if \(!realPlatform \|\| !workspace \|\| shellStatus.value !== 'ready'\) return;/);
});

test('Wave 1: cross-view navigation, deep-links, and same-view prop watchers are wired', () => {
  const shell = readRel('src/direction2/Direction2App.vue');
  assert.match(shell, /<ResourcesView[\s\S]*?:initial-tab="subParam"[\s\S]*?:target-id="targetResourceId"/);
  assert.match(shell, /<TasksAndCollaborationView[\s\S]*?:selected-employee-id="targetEmployeeId"/);
  assert.match(shell, /@clickoutside="showSearchPopover = false"/);
  assert.match(shell, /@keydown\.esc="showSearchPopover = false"/);
  assert.match(shell, /workspaceRoleLabel\(currentWorkspace\.role\)/);
  assert.doesNotMatch(shell, /演示态已禁用|暂未开放|旧版切换/);

  const workforce = readRel('src/direction2/views/WorkforceView.vue');
  assert.match(workforce, /props\.initialTab === 'create'/);
  assert.match(workforce, /tab === 'policies'/);
  assert.match(workforce, /emit\('navigate', 'tasks', 'tasks', currentEditingEmp\.value\.id, 'employee'\)/);

  const tasks = readRel('src/direction2/views/PlatformTasksView.vue');
  assert.match(tasks, /selectedEmployeeId\?: string \| undefined/);
  assert.match(tasks, /watch\(\s*\(\) => props\.selectedTaskId/);
  assert.match(tasks, /<TaskDetailPanel/);

  const resources = readRel('src/direction2/views/ResourcesView.vue');
  assert.match(resources, /initialTab\?: string \| undefined/);
  assert.match(resources, /targetId\?: string \| undefined/);
  assert.match(resources, /watch\(\s*\(\) => \[props\.initialTab, props\.targetId\]/);

  const settings = readRel('src/direction2/views/SettingsView.vue');
  assert.match(settings, /watch\(\s*\[\s*\(\) => props\.initialTab,\s*canManagePlatform\s*\]/);
});

import {
  getEmployeeStatusMeta,
  getTaskStatusMeta,
  getGatewayChannelStatusMeta,
  formatTimeShort,
  parsePercentValue,
  parseTrendPathPoints,
  parseSkillMarkdown,
  paginateItems
} from '../src/direction2/utils/presentation.ts';

test('Wave 2: presentation helpers cover all statuses, times, percents, trend points, markdown & pagination', () => {
  assert.equal(getEmployeeStatusMeta('warning').label, '告警异常');
  assert.equal(getEmployeeStatusMeta('warning').tagType, 'warning');
  assert.equal(getEmployeeStatusMeta('pending_setup').label, '待配置渠道');
  assert.equal(getEmployeeStatusMeta('disabled').label, '已停用');

  assert.equal(getTaskStatusMeta('rejected').label, '已拦截拒绝');
  assert.equal(getTaskStatusMeta('rejected').tagType, 'error');
  assert.equal(getTaskStatusMeta('waiting_input').label, '待补充信息');
  assert.equal(getTaskStatusMeta('unknown').label, '结果待核对');

  assert.equal(getGatewayChannelStatusMeta('degraded').label, '延迟波动');
  assert.equal(getGatewayChannelStatusMeta('offline').tagType, 'error');

  assert.equal(formatTimeShort('2026-11-13 09:45:12'), '09:45:12');
  assert.equal(formatTimeShort('2026-11-13T09:45:00Z'), '09:45:00');
  assert.equal(formatTimeShort('刚刚'), '刚刚');
  assert.equal(formatTimeShort(''), '--');

  assert.equal(parsePercentValue('18%'), 18);
  assert.equal(parsePercentValue('2.4 GB / 8.0 GB'), 30);
  assert.equal(parsePercentValue('2 / 8 并发槽位'), 25);
  assert.equal(parsePercentValue('unknown', 12), 12);

  const pts = parseTrendPathPoints('M0,100 L250,60 L500,20', ['11-01', '11-04', '11-07']);
  assert.deepEqual(pts, [
    { x: 0, y: 100, date: '11-01' },
    { x: 250, y: 60, date: '11-04' },
    { x: 500, y: 20, date: '11-07' }
  ]);

  const parsedMd = parseSkillMarkdown(`---
name: 故障定界助手
code: incident_triage
version: v2.1.0
description: 自动聚合日志并生成根因报告
tools: [query_logs, fetch_metrics]
---
# 标题忽略
请先检查最近 15 分钟的错误率曲线。`);
  assert.equal(parsedMd.name, '故障定界助手');
  assert.equal(parsedMd.code, 'incident_triage');
  assert.equal(parsedMd.version, 'v2.1.0');
  assert.equal(parsedMd.requiredToolTagsStr, 'query_logs, fetch_metrics');
  assert.match(parsedMd.promptTemplate, /请先检查最近 15 分钟的错误率曲线/);

  assert.deepEqual(paginateItems([1, 2, 3, 4, 5, 6], 2, 4), [5, 6]);
});

test('Wave 2: removed hardcoded values, typos, and unsafe actions across views', () => {
  const home = readRel('src/direction2/views/HomeDashboard.vue');
  assert.doesNotMatch(home, /<circle cx="200" cy="65"/);
  assert.doesNotMatch(home, />长连接稳定</);
  assert.doesNotMatch(home, />刚刚</);
  assert.match(home, /hasPriorityItems/);

  const settings = readRel('src/direction2/views/SettingsView.vue');
  assert.doesNotMatch(settings, /协作成人员/);
  assert.doesNotMatch(settings, /kiko-prod-01 \(只读标识\)/);
  assert.doesNotMatch(settings, /:percentage="18"|:percentage="30"|:percentage="25"/);
  assert.doesNotMatch(settings, /name="operations"|getRuntimeMonitor|inspectChannels|runCronJob/);
  assert.match(settings, /openInviteModal/);

  const tasks = readRel('src/direction2/views/PlatformTasksView.vue');
  assert.doesNotMatch(tasks, /客服助手[\s\S]*?➔[\s\S]*?研发助手/);
  assert.doesNotMatch(tasks, /\.updatedAt\.slice\(11\)/);
  assert.match(tasks, /taskApi\.list/);
  assert.doesNotMatch(tasks, /retryTask|verifyTaskResult|resendTaskDelivery/);

  const models = readRel('src/direction2/views/PlatformModelsPanel.vue');
  const directory = readRel('src/direction2/views/ResourceDirectoryMembers.vue');
  assert.match(models, /<NPopconfirm[\s\S]*?action\('revokeKey', m\)/);
  assert.match(directory, /cursors\.value/);
  assert.match(directory, /snapshot_version: version/);
  assert.match(directory, /clearTimeout\(debounce\)/);
  assert.doesNotMatch(directory, /openInviteModal|\[获取失败\]|\[验证通过\]|\[已过期\]/);

  const handler = readRel('src/direction2/mock-server/handler.ts');
  assert.match(handler, /typeof body\['promptTemplate'\] === 'string'/);
  assert.match(handler, /Array\.isArray\(body\['requiredToolTags'\]\)/);

  const chatBox = readRel('src/direction2/components/EmployeeSingleChatBox.vue');
  assert.doesNotMatch(chatBox, /输入 \/fail 测试/);
  assert.match(chatBox, /getEmployeeStatusMeta/);
  assert.match(chatBox, /getTaskStatusMeta/);

  const workforce = readRel('src/direction2/views/WorkforceView.vue');
  const workforceDirectory = readRel('src/direction2/components/workforce/WorkforceDirectoryList.vue');
  assert.match(workforceDirectory, /\['warning', '告警异常'\]/);
  assert.match(workforceDirectory, /selectedFilter === filter.value/);
  assert.doesNotMatch(workforce, /4 大业务模块已关联/);
});

test('Wave 3: visual consistency, layout auto-scroll, clean copy & Naive UI imports', () => {
  const files = collectFiles(srcDir).filter(f => f.endsWith('.vue'));
  for (const file of files) {
    const content = fs.readFileSync(file, 'utf8');
    // Check no leftover emerald green palette anywhere in direction2
    assert.doesNotMatch(
      content,
      /#0F8167|#EAF8F3|#9EDFC8|#0B6A55/i,
      `Leftover emerald color in ${path.relative(rootDir, file)}`
    );
    // Check every <N...> tag is imported in <script setup>
    const scriptMatch = /<script[\s\S]*?>([\s\S]*?)<\/script>/.exec(content);
    const tplMatch = /<template>([\s\S]*)<\/template>/.exec(content);
    if (scriptMatch && tplMatch) {
      const script = scriptMatch[1];
      const tpl = tplMatch[1];
      const usedN = new Set([...tpl.matchAll(/<(N[A-Z][A-Za-z0-9]*)\b/g)].map(m => m[1]));
      for (const comp of usedN) {
        assert.match(
          script,
          new RegExp(`\\b${comp}\\b`),
          `Component <${comp}> used in template without import in ${path.relative(rootDir, file)}`
        );
      }
    }
  }

  const chatView = readRel('src/direction2/views/ConversationsView.vue');
  assert.match(chatView, /calc\(100vh - 156px\)/);
  assert.match(chatView, /<NEmpty/);

  const chatBox = readRel('src/direction2/components/EmployeeSingleChatBox.vue');
  assert.match(chatBox, /scrollToBottom/);
  assert.match(chatBox, /messagesScrollRef/);

  const workforce = readRel('src/direction2/views/WorkforceView.vue');
  assert.doesNotMatch(workforce, /系统提示词|岗位提示词/);
  assert.match(workforce, /<h5 class="fb-title">岗位行为规范与人设约束<\/h5>/);
  assert.doesNotMatch(workforce, /权限与安全规则已排他独立隔离|绝不提交未保存的基本信息草稿|\[Mock\]/);

  const tasks = readRel('src/direction2/views/PlatformTasksView.vue');
  assert.doesNotMatch(tasks, /JIRA-4819|targetType \/ channel \/ connectionId \/ targetId/);

  const skills = readRel('src/direction2/views/PlatformSkillsPanel.vue');
  const plugins = readRel('src/direction2/views/PlatformPluginsPanel.vue');
  assert.match(skills, /highlighted:\s*targetId\s*===\s*s\.id/);
  assert.match(plugins, /highlighted:\s*targetId\s*===\s*srv\.id/);
});

test('Wave 3: no leftover emerald hex colors in any src file (.vue/.ts/.css)', () => {
  const files = collectFiles(srcDir).filter(f => /\.(vue|ts|tsx|css|js|mjs)$/.test(f));
  for (const file of files) {
    const content = fs.readFileSync(file, 'utf8');
    assert.doesNotMatch(
      content,
      /#0F8167|#EAF8F3|#9EDFC8|#0B6A55/i,
      `Leftover emerald color in ${path.relative(rootDir, file)}`
    );
  }
});

test('Wave 3: template tag balance and <NFormItem> wrapped in <NForm> across all Vue SFCs', () => {
  const files = collectFiles(srcDir).filter(f => f.endsWith('.vue'));
  const checkedTags = ['div', 'NForm', 'NModal', 'NDrawer', 'NPopover'];

  for (const file of files) {
    const rel = path.relative(rootDir, file);
    const content = fs.readFileSync(file, 'utf8');
    const tplMatch = /<template>([\s\S]*)<\/template>/.exec(content);
    assert.ok(tplMatch, `Missing <template> in ${rel}`);
    // Strip HTML comments before scanning tags
    const tpl = tplMatch[1].replace(/<!--[\s\S]*?-->/g, '');

    // Check open/close tag balance per checked tag
    for (const tag of checkedTags) {
      const tokenRe = new RegExp(`<(\\/?)${tag}\\b([^>]*?)(\\/?)>`, 'g');
      let depth = 0;
      let m;
      while ((m = tokenRe.exec(tpl)) !== null) {
        const isClosing = m[1] === '/';
        const isSelfClosing = m[3] === '/';
        if (isSelfClosing) continue;
        if (isClosing) {
          depth -= 1;
          assert.ok(depth >= 0, `Extra closing </${tag}> in ${rel} around index ${m.index}`);
        } else {
          depth += 1;
        }
      }
      assert.equal(depth, 0, `Unclosed <${tag}> (depth=${depth}) in ${rel}`);
    }

    // Check every <NFormItem> sits inside an open <NForm>
    const formTokenRe = /<(\/?)(NForm|NFormItem)\b[^>]*?(\/?)>/g;
    let formDepth = 0;
    let fm;
    while ((fm = formTokenRe.exec(tpl)) !== null) {
      const isClosing = fm[1] === '/';
      const tag = fm[2];
      const isSelfClosing = fm[3] === '/';
      if (isSelfClosing) continue;
      if (tag === 'NForm') {
        formDepth += isClosing ? -1 : 1;
      } else if (tag === 'NFormItem' && !isClosing) {
        assert.ok(formDepth > 0, `<NFormItem> outside <NForm> in ${rel} around index ${fm.index}`);
      }
    }
  }
});

import {
  resolveMemberKeyToOptionId,
  buildPrincipalRefsFromIds,
  validateManualCredentials,
  buildManualCredPayload,
  computeBusinessKeyPreview,
  patchVerifiedWecomOnEmployee
} from '../src/direction2/utils/workforce.ts';

test('Wave 4: WorkforceView split architecture and workforce pure helpers', () => {
  const wfContent = readRel('src/direction2/views/WorkforceView.vue');
  const wfLines = wfContent.split('\n').length;
  // Real API wiring, scoped CAS and explicit unavailable states add a small
  // integration layer; the original five child components remain mandatory.
  assert.ok(wfLines < 1700, `WorkforceView.vue should stay cleanly split (< 1700 lines), got ${wfLines}`);

  const requiredChildren = [
    'WorkforceDirectoryList',
    'WorkforcePackagesTab',
    'WorkforceChannelsTab',
    'WorkforceTriggersTab',
    'WorkforceRulesTab'
  ];
  for (const child of requiredChildren) {
    assert.match(wfContent, new RegExp(`<${child}\\b`), `WorkforceView.vue must mount <${child}>`);
  }

  // Test resolveMemberKeyToOptionId & buildPrincipalRefsFromIds
  const dirUsers = [
    {
      id: 'usr-internal-1',
      refId: 'ou_feishu_99',
      name: '张工',
      department: '基础架构组',
      platform: 'feishu',
      connectionId: 'conn-fs-1',
      sourceType: 'feishu'
    }
  ];
  const dirDepts = [
    {
      id: 'dept-1',
      refId: 'od_feishu_1',
      name: '运维中心',
      memberCount: 12,
      platform: 'feishu',
      connectionId: 'conn-fs-1',
      sourceType: 'feishu'
    }
  ];
  assert.equal(resolveMemberKeyToOptionId('ou_feishu_99', [], dirUsers, dirDepts), 'usr-internal-1');
  assert.equal(resolveMemberKeyToOptionId('od_feishu_1', [], dirUsers, dirDepts), 'dept-1');

  const refs = buildPrincipalRefsFromIds(['usr-internal-1', 'dept-1'], [], dirUsers, dirDepts);
  assert.equal(refs[0].canonicalKey, 'feishu::conn-fs-1::user::ou_feishu_99');
  assert.equal(refs[1].canonicalKey, 'feishu::conn-fs-1::department::od_feishu_1');

  // Test manual credential validation & payload formatting
  assert.ok(
    validateManualCredentials('feishu', { appId: '', appSecret: 'sec', botId: '', botSecret: '', appKey: '' })
  );
  assert.match(
    validateManualCredentials('wecom', { appId: '', appSecret: '', botId: 'bot1', botSecret: '••••••', appKey: '' }) || '',
    /脱敏占位符/
  );
  assert.equal(
    validateManualCredentials('dingtalk', { appId: '', appSecret: 'real_sec', botId: '', botSecret: '', appKey: 'ding_key' }),
    null
  );
  assert.deepEqual(
    buildManualCredPayload('dingtalk', { appId: '', appSecret: ' s1 ', botId: '', botSecret: '', appKey: ' k1 ' }),
    { appKey: 'k1', appSecret: 's1' }
  );

  // Test cron business key preview
  const d = new Date('2026-11-13T08:00:00Z');
  assert.equal(computeBusinessKeyPreview('empty', '', '', d), '（无业务键，每次触发独立）');
  assert.equal(computeBusinessKeyPreview('fixed', 'prod-daily', '', d), 'prod-daily');
  assert.equal(computeBusinessKeyPreview('pattern', '', 'report-${YYYYMMDD}', d), 'report-20261113');

  // Test WeCom verification propagation
  const emp = {
    humanConfirmationRules: [
      {
        target: {
          provider: 'wecom',
          userId: 'wecom_u_1',
          targetId: 'wecom_u_1',
          connectionId: 'conn-wc-1',
          identityVerified: false,
          bindingStatus: 'pending_binding'
        }
      }
    ],
    channels: [
      {
        platform: 'wecom',
        connectionId: 'conn-wc-1',
        sharingRules: [
          {
            targetUserId: 'wecom_u_1',
            syncStatus: 'auth_error',
            authErrorDetail: '未私聊核验'
          }
        ]
      }
    ]
  };
  patchVerifiedWecomOnEmployee(emp, 'wecom_u_1', 'conn-wc-1', 'bot-wc-9');
  assert.equal(emp.humanConfirmationRules[0].target.identityVerified, true);
  assert.equal(emp.humanConfirmationRules[0].target.bindingStatus, 'verified');
  assert.equal(emp.humanConfirmationRules[0].target.botId, 'bot-wc-9');
  assert.equal(emp.channels[0].sharingRules[0].syncStatus, 'applied');
  assert.equal(emp.channels[0].sharingRules[0].authErrorDetail, undefined);
});

test('Wave 4: mobile responsiveness across shell, views, modals, and grids', () => {
  const responsiveViews = [
    'src/direction2/Direction2App.vue',
    'src/direction2/views/HomeDashboard.vue',
    'src/direction2/views/ConversationsView.vue',
    'src/direction2/views/WorkforceView.vue',
    'src/direction2/views/PlatformTasksView.vue',
    'src/direction2/styles/resource-library.css',
    'src/direction2/views/SettingsView.vue',
    'src/direction2/components/EmployeeSingleChatBox.vue',
    'src/direction2/components/UnifiedDirectoryPicker.vue',
    'src/direction2/components/workforce/WorkforceDirectoryList.vue',
    'src/direction2/components/workforce/WorkforceChannelsTab.vue',
    'src/direction2/components/workforce/workforce-shared.css'
  ];

  for (const rel of responsiveViews) {
    const content = readRel(rel);
    assert.match(content, /@media\s*\(/, `Missing @media responsive breakpoint in ${rel}`);
  }

  const shell = readRel('src/direction2/Direction2App.vue');
  assert.match(shell, /isMobileSidebarOpen/);
  assert.match(shell, /mobile-menu-btn/);
  assert.match(shell, /isSidebarCollapsed/);
  assert.match(shell, /isRailCollapsed/);
  assert.match(shell, /<NTooltip[\s\S]*?:disabled="!isRailCollapsed"/);
  assert.match(shell, /rail-collapse-btn/);
  assert.match(shell, /isMobileViewport \? 'top-start' : 'right-end'/);
  assert.match(shell, /\.d2-rail-nav\.collapsed\s*\{[\s\S]*?width:\s*64px;/);

  const vueFiles = collectFiles(srcDir).filter(f => f.endsWith('.vue'));
  for (const file of vueFiles) {
    const rel = path.relative(rootDir, file);
    const content = fs.readFileSync(file, 'utf8');
    assert.doesNotMatch(
      content,
      /<NModal\b[^>]*\bwidth:\s*[4-9]\d{2}px;/,
      `Rigid modal width without min(..., 94vw) in ${rel}`
    );
    assert.doesNotMatch(
      content,
      /minmax\([3-9]\d{2}px\s*,/,
      `Wide minmax() grid track overflows narrow phones without min(..., 100%) in ${rel}`
    );
  }
});
