// Initial seed data for the local stateful Direction 2 mock server.
import {
  buildCanonicalPrincipalKey,
  type PlatformType, type CapabilityPackageBinding, type WorkspaceSkill, type McpServerEndpoint,
  type ChannelSyncSourceDetail, type WorkspaceEmailInvitation, type DirectorySyncStatus,
  type DigitalEmployee, type EmployeeConversationSession, type WorkItem, type TaskRecord,
  type HumanApproval, type ChannelDirectoryDepartment, type ChannelDirectoryUser,
  type ModelItem, type WorkspaceSettings, type WorkspaceMember, type ActivityEvent,
  type FeishuRegexTemplateItem, type WorkspaceInfo, type RuntimeMonitorData,
  type WecomVerifySession, type ChannelQrRegistrationSession, type ChannelGroupOption,
  type DirectorySyncJob, type ChatExecutionStatus, type PrincipalRef, type AuthProviderType
} from "../types";

export const platformCandidateGroupItems: Record<PlatformType, ChannelGroupOption[]> = {
  feishu: [
    { groupId: 'grp-feishu-01', connectionId: 'conn-feishu-default', platform: 'feishu', name: '飞书「#前端研发核心群」', memberCount: 38 },
    { groupId: 'grp-feishu-02', connectionId: 'conn-feishu-default', platform: 'feishu', name: '飞书「#线上发布与值班群」', memberCount: 14 },
    { groupId: 'grp-feishu-03', connectionId: 'conn-feishu-default', platform: 'feishu', name: '飞书「#技术支持咨询群」', memberCount: 42 },
    { groupId: 'grp-feishu-cust-01', connectionId: 'conn-feishu-default', platform: 'feishu', name: '飞书「#客户技术支持咨询群」', memberCount: 64 },
    { groupId: 'grp-feishu-05', connectionId: 'conn-feishu-default', platform: 'feishu', name: '飞书「#产品与设计评审群」', memberCount: 25 }
  ],
  wecom: [
    { groupId: 'grp-wecom-01', connectionId: 'ww_wecom_sec_01', platform: 'wecom', name: '企业微信「#技术支持快速协同群」', memberCount: 18 },
    { groupId: 'grp-wecom-02', connectionId: 'ww_wecom_sec_01', platform: 'wecom', name: '企业微信「#法务与风控合规群」', memberCount: 12 },
    { groupId: 'grp-wecom-03', connectionId: 'ww_wecom_sec_01', platform: 'wecom', name: '企业微信「#运营活动通知群」', memberCount: 29 }
  ],
  dingtalk: [
    { groupId: 'grp-ding-01', connectionId: 'conn-dingtalk-default', platform: 'dingtalk', name: '钉钉「#架构技术委员会」', memberCount: 22 },
    { groupId: 'grp-ding-02', connectionId: 'conn-dingtalk-default', platform: 'dingtalk', name: '钉钉「#SRE运维应急值班群」', memberCount: 16 }
  ]
};

export const platformCandidateGroups: Record<PlatformType, string[]> = {
  feishu: platformCandidateGroupItems.feishu.map(g => g.name),
  wecom: platformCandidateGroupItems.wecom.map(g => g.name),
  dingtalk: platformCandidateGroupItems.dingtalk.map(g => g.name)
};

export const workspaceCapabilityPackagesCatalog: CapabilityPackageBinding[] = [
  {
    id: 'pkg-gitlab-review',
    name: '代码库审查与 MR 差异分析包',
    description: '组合 GitLab 差异检出、AST 语法坏味道检查与 PR 评审报告生成。',
    enabled: true,
    isBound: true,
    resources: {
      skills: ['skill-git-review'],
      mcp: ['mcp-gitlab'],
      mcpTools: ['get_diff', 'create_mr_comment'],
      bash: false
    },
    scope: {
      userScope: {
        mode: 'inherit_with_blacklist',
        whitelistMemberIds: [],
        blacklistMemberIds: []
      },
      channelPolicies: [
        { platform: 'feishu', enabled: true, allowPrivate: true, allowGroup: true },
        { platform: 'wecom', enabled: false, allowPrivate: false, allowGroup: false },
        { platform: 'dingtalk', enabled: true, allowPrivate: true, allowGroup: true }
      ],
      conversationKinds: ['private', 'group'],
      triggerUsageScope: { mode: 'all', allowTriggerIds: [], denyTriggerIds: [] },
      handoffUsageScope: {
        mode: 'inherit_with_blacklist',
        allowEmployeeIds: [],
        denyEmployeeIds: []
      }
    }
  },
  {
    id: 'pkg-ci-deploy',
    name: 'Jenkins CI/CD 构建与测试执行包',
    description: '执行回归测试用例，导出覆盖率报告，禁止生产直发。',
    enabled: true,
    isBound: true,
    resources: {
      skills: ['skill-fe-diagnose'],
      mcp: ['mcp-jenkins'],
      mcpTools: ['trigger_build', 'get_build_status'],
      bash: true
    },
    scope: {
      userScope: {
        mode: 'whitelist_with_blacklist',
        whitelistMemberIds: ['dept-fs-fe', 'dir-web-1'],
        blacklistMemberIds: ['dir-fs-1']
      },
      channelPolicies: [
        { platform: 'feishu', enabled: true, allowPrivate: true, allowGroup: true },
        { platform: 'wecom', enabled: false, allowPrivate: false, allowGroup: false },
        { platform: 'dingtalk', enabled: false, allowPrivate: false, allowGroup: false }
      ],
      conversationKinds: ['group'],
      triggerUsageScope: { mode: 'allowlist', allowTriggerIds: ['trg-1', 'trg-2'], denyTriggerIds: [] },
      handoffUsageScope: {
        mode: 'whitelist',
        allowEmployeeIds: ['emp-02'],
        denyEmployeeIds: []
      }
    }
  },
  {
    id: 'pkg-sre-observability',
    name: 'SRE 云原生指标观测与告警下钻包',
    description: '集成 Prometheus PromQL 查询与 Kubernetes Pod 日志排查。',
    enabled: true,
    isBound: false,
    resources: {
      skills: ['skill-pod-logs'],
      mcp: ['mcp-prometheus', 'mcp-k8s'],
      mcpTools: ['query_range', 'get_pod_logs', 'describe_node'],
      bash: false
    },
    scope: {
      userScope: {
        mode: 'inherit_with_blacklist',
        whitelistMemberIds: [],
        blacklistMemberIds: []
      },
      channelPolicies: [
        { platform: 'feishu', enabled: true, allowPrivate: true, allowGroup: true },
        { platform: 'dingtalk', enabled: true, allowPrivate: true, allowGroup: true },
        { platform: 'wecom', enabled: false, allowPrivate: false, allowGroup: false }
      ],
      conversationKinds: ['private', 'group'],
      triggerUsageScope: { mode: 'all', allowTriggerIds: [], denyTriggerIds: [] },
      handoffUsageScope: {
        mode: 'inherit_with_blacklist',
        allowEmployeeIds: [],
        denyEmployeeIds: []
      }
    }
  },
  {
    id: 'pkg-customer-faq',
    name: '智能知识库检索与工单流转包',
    description: '接入企业私有向量知识库，并与飞书多维表格工单联动。',
    enabled: true,
    isBound: false,
    resources: {
      skills: ['skill-faq-search'],
      mcp: ['mcp-feishu-bitable'],
      mcpTools: ['search_records', 'append_record'],
      bash: false
    },
    scope: {
      userScope: {
        mode: 'inherit_with_blacklist',
        whitelistMemberIds: [],
        blacklistMemberIds: []
      },
      channelPolicies: [
        { platform: 'feishu', enabled: true, allowPrivate: true, allowGroup: true },
        { platform: 'wecom', enabled: true, allowPrivate: true, allowGroup: true },
        { platform: 'dingtalk', enabled: true, allowPrivate: true, allowGroup: true }
      ],
      conversationKinds: ['private', 'group'],
      triggerUsageScope: { mode: 'all', allowTriggerIds: [], denyTriggerIds: [] },
      handoffUsageScope: {
        mode: 'inherit_with_blacklist',
        allowEmployeeIds: [],
        denyEmployeeIds: []
      }
    }
  }
];

// 模拟员工列表

// ================= 工作区 Skill 资源库目录 =================
export const initialWorkspaceSkillsCatalog: WorkspaceSkill[] = [
  {
    id: 'skill-git-review',
    name: 'Git 代码 diff 差异与合并请求安全审查',
    code: 'gitlab_mr_code_reviewer',
    version: 'v2.3.0',
    source: 'git_synced',
    gitRepoUrl: 'https://gitlab.corp.internal/eng-infra/mr-review-skill.git',
    gitBranch: 'main',
    gitCommit: 'b74c90a',
    description: '提取 GitLab Merge Request 差异代码，执行安全规范扫描、性能回归预警与逐行评审建议。',
    promptTemplate: '请结合 GitLab diff 检查本次 MR 的安全边界、空指针隐患与首屏体积增量，给出结构化审查意见。',
    requiredToolTags: ['get_diff', 'list_commits', 'create_mr_comment'],
    boundEmployeesCount: 5,
    enabled: true,
    createdAt: '2026-10-10',
    historyVersions: [
      {
        version: 'v2.3.0',
        commitHash: 'b74c90a',
        sourceType: 'git',
        changeLog: '增强 TypeScript strictNullChecks 与 XSS 危险函数规则检出率',
        promptTemplate: '请结合 GitLab diff 检查本次 MR 的安全边界、空指针隐患与首屏体积增量，给出结构化审查意见。',
        updatedAt: '2026-11-09 15:20'
      },
      {
        version: 'v2.1.0',
        commitHash: '5e12d44',
        sourceType: 'git',
        changeLog: '支持 GitLab MR 行级评审建议自动评论格式化',
        promptTemplate: '提取 GitLab MR 代码 diff 并按风险级别输出改进建议。',
        updatedAt: '2026-10-18 11:00'
      }
    ]
  },
  {
    id: 'skill-fe-diagnose',
    name: '前端组件故障诊断与白屏排查',
    code: 'frontend_diagnostics',
    version: 'v2.1.0',
    source: 'git_synced',
    gitRepoUrl: 'https://gitlab.corp.internal/fe-platform/white-screen-diagnostics.git',
    gitBranch: 'main',
    gitCommit: '9a3e11b',
    description: '通过 DOM 树解析与控制台 Trace 捕获前端白屏异常，提供精准修复代码建议。',
    promptTemplate: '你是一名资深前端架构专家，负责排查白屏错误。请先分析控制台错误栈，比对 DOM 变更，再提出修复策略。',
    requiredToolTags: ['browser_eval', 'get_diff'],
    boundEmployeesCount: 4,
    enabled: true,
    createdAt: '2026-10-15',
    historyVersions: [
      {
        version: 'v2.1.0',
        commitHash: '9a3e11b',
        sourceType: 'git',
        changeLog: '优化 React 19 异步组件水合报错的 Trace 堆栈解析与 source-map 逆向映射',
        promptTemplate: '你是一名资深前端架构专家，负责排查白屏错误。请先分析控制台错误栈，比对 DOM 变更，再提出修复策略。',
        updatedAt: '2026-11-10 16:30'
      },
      {
        version: 'v2.0.0',
        commitHash: '7b31d4e',
        sourceType: 'git',
        changeLog: '支持 DOM 树 diff 与控制台错误栈合并分析',
        promptTemplate: '你是一名资深前端专家，排查前端错误栈并提取 DOM 变动节点。',
        updatedAt: '2026-10-28 11:20'
      },
      {
        version: 'v1.0.0',
        commitHash: '2c89f01',
        sourceType: 'git',
        changeLog: '初始版本：基础控制台错误捕获与堆栈格式化',
        promptTemplate: '基础前端错误日志格式化与定位。',
        updatedAt: '2026-10-15 09:00'
      }
    ]
  },
  {
    id: 'skill-sql-tune',
    name: 'SQL 慢查询性能调优建议',
    code: 'sql_performance_tuning',
    version: 'v1.4.2',
    source: 'local_import',
    importedFileName: 'sql-tuning-pack-v1.4.2.zip',
    description: '分析 Explain 执行计划并生成索引优化策略与重写 SQL 模板。',
    promptTemplate: '分析以下慢查询日志和执行计划，针对耗时大于 200ms 的子查询给出联合索引设计建议。',
    requiredToolTags: ['mysql_explain', 'table_schema'],
    boundEmployeesCount: 6,
    enabled: true,
    createdAt: '2026-11-01',
    historyVersions: [
      {
        version: 'v1.4.2',
        commitHash: '3f82a10',
        sourceType: 'zip',
        changeLog: '新增 MySQL 8.0 CTE 递归查询与 Window Function 联合索引设计指导',
        promptTemplate: '分析以下慢查询日志和执行计划，针对耗时大于 200ms 的子查询给出联合索引设计建议。',
        updatedAt: '2026-11-01 14:00'
      },
      {
        version: 'v1.4.0',
        commitHash: '1a77d89',
        sourceType: 'zip',
        changeLog: '支持复合索引前缀匹配与最左匹配失效研判',
        promptTemplate: '分析慢查询 Explain 执行计划，给出基础索引建议。',
        updatedAt: '2026-10-20 18:30'
      }
    ]
  },
  {
    id: 'skill-hotfix-audit',
    name: '生产环境发布与热修复安全审计',
    code: 'prod_change_auditor',
    version: 'v3.0.0',
    source: 'git_synced',
    gitRepoUrl: 'https://github.com/corp-ops/prod-safety-rules.git',
    gitBranch: 'release-v3',
    gitCommit: '4d19aa2',
    description: '审查发布变更分支的 diff 和变更清单，识别生产配置覆盖与高危指令。',
    promptTemplate: '严格比对生产发布变更，若发现破坏性脚本或未声明环境变量，立即挂起并请求人工裁决。',
    requiredToolTags: ['get_diff', 'trigger_build'],
    boundEmployeesCount: 3,
    enabled: true,
    createdAt: '2026-11-08',
    historyVersions: [
      {
        version: 'v3.0.0',
        commitHash: '4d19aa2',
        sourceType: 'git',
        changeLog: '升级高危命令拦截规则库（增加 DROP TABLE/TRUNCATE 等高危 SQL 拦截）',
        promptTemplate: '严格比对生产发布变更，若发现破坏性脚本或未声明环境变量，立即挂起并请求人工裁决。',
        updatedAt: '2026-11-08 10:15'
      },
      {
        version: 'v2.5.0',
        commitHash: '8e01bf3',
        sourceType: 'git',
        changeLog: '支持 Kubernetes Helm Release 配置文件审计',
        promptTemplate: '比对发布变更清单，识别未授权环境变量。',
        updatedAt: '2026-10-12 17:00'
      }
    ]
  },
  {
    id: 'skill-cs-sla',
    name: '客服投诉 SLA 违约风险评估',
    code: 'cs_sla_compensation_eval',
    version: 'v1.1.0',
    source: 'markdown_file',
    importedFileName: 'cs-sla-sop-matrix.md',
    description: '研判客户异常工单是否属于非标赔偿请求，并评估跨部门审批影响。',
    promptTemplate: '根据客户工单等级判断 SLA 阈值，超出常规补偿标准的事项发起人工主管裁决。',
    requiredToolTags: ['cs_ticket_lookup'],
    boundEmployeesCount: 2,
    enabled: true,
    createdAt: '2026-11-05',
    historyVersions: [
      {
        version: 'v1.1.0',
        commitHash: 'md-hash-99a',
        sourceType: 'markdown',
        changeLog: '更新双十一大促期间专属退换货补偿上限规则（从 200 元调整为 500 元）',
        promptTemplate: '根据客户工单等级判断 SLA 阈值，超出常规补偿标准的事项发起人工主管裁决。',
        updatedAt: '2026-11-05 15:40'
      }
    ]
  }
];

// ================= 工作区私有 Streamable HTTP MCP 端点目录 =================
export const initialWorkspaceMcpCatalog: McpServerEndpoint[] = [
  {
    id: 'mcp-gitlab',
    name: 'GitLab Enterprise MCP',
    endpoint: 'http://192.168.10.88:8080/mcp',
    protocol: 'Streamable HTTP',
    authType: 'bearer_token',
    status: 'connected',
    tokenMasked: 'glpat-••••••••827a',
    tools: [
      { name: 'get_diff', description: '提取 MR 分支比对的代码增删 diff', riskLevel: 'safe' },
      { name: 'list_commits', description: '列出目标分支最近提交记录', riskLevel: 'safe' },
      { name: 'create_mr_comment', description: '在 MR 变更行上回写评审建议', riskLevel: 'safe' },
      { name: 'delete_branch', description: '删除远程分支 (高风险禁止勾选)', riskLevel: 'high' }
    ],
    allowedTools: ['get_diff', 'list_commits', 'create_mr_comment'],
    whitelistMatched: true,
    enabled: true
  },
  {
    id: 'mcp-jenkins',
    name: 'Jenkins CI/CD MCP',
    endpoint: 'http://192.168.10.90:8081/mcp',
    protocol: 'Streamable HTTP',
    authType: 'custom_headers',
    customHeaders: [
      { key: 'X-Jenkins-Worker', value: 'cluster-prod-02' },
      { key: 'X-Auth-Token', value: '11fe92••••••••fca1' }
    ],
    status: 'connected',
    tokenMasked: '11fe92••••••••fca1',
    tools: [
      { name: 'trigger_build', description: '触发自动化测试流水线构建', riskLevel: 'medium' },
      { name: 'get_build_status', description: '查询构建执行进度与通过率', riskLevel: 'safe' },
      { name: 'abort_build', description: '中止当前正在运行的构建', riskLevel: 'medium' }
    ],
    allowedTools: ['trigger_build', 'get_build_status'],
    whitelistMatched: true,
    enabled: true
  },
  {
    id: 'mcp-db-readonly',
    name: 'Production DB ReadOnly MCP',
    endpoint: 'http://192.168.10.92:8082/mcp',
    protocol: 'Streamable HTTP',
    authType: 'api_key',
    apiKeyName: 'X-DB-Gateway-Key',
    apiKeyValue: 'dbro-••••••••331b',
    status: 'connected',
    tokenMasked: 'dbro-••••••••331b',
    tools: [
      { name: 'mysql_explain', description: '执行 Explain 分析慢查询计划', riskLevel: 'safe' },
      { name: 'table_schema', description: '读取数据库元数据表结构与索引定义', riskLevel: 'safe' }
    ],
    allowedTools: ['mysql_explain', 'table_schema'],
    whitelistMatched: true,
    enabled: true
  }
];

export const initialChannelSyncSources: ChannelSyncSourceDetail[] = [
  {
    id: 'src-feishu',
    channel: 'feishu',
    connectionId: 'conn-emp-01-feishu',
    channelName: '飞书开放平台',
    appName: '智能中台飞书自建应用',
    appIdOrBotId: 'mock-feishu-app-id',
    syncInterval: '每日 02:00 自动同步 + 成员变更实时同步',
    status: 'synced',
    progressPercent: 100,
    lastSyncedAt: '12 分钟前 (2026-11-11 11:15:00)',
    snapshotVersion: 'snap-fs-20261111-0941',
    snapshotNodeCount: 480,
    snapshotGeneratedAt: '2026-11-11 11:15:02',
    lastSuccessfulSnapshot: {
      snapshotVersion: 'snap-fs-20261111-0941',
      syncedAt: '2026-11-11 11:15:02',
      totalMembers: 480,
      totalDepartments: 128
    },
    totalMembers: 480,
    totalDepartments: 128,
    history: [
      { syncId: 'sync-fs-901', trigger: 'periodic', time: '11:15:00', duration: '1.8s', nodesSynced: 480, status: 'success', detail: '飞书通讯录同步完成：已同步 128 个部门' },
      { syncId: 'sync-fs-900', trigger: 'webhook', time: '10:30:12', duration: '0.4s', nodesSynced: 1, status: 'success', detail: '成员【周凯】部门变更已同步' }
    ]
  },
  {
    id: 'src-wecom',
    channel: 'wecom',
    connectionId: 'ww_wecom_sec_01',
    channelName: '企业微信智能机器人',
    appName: '技术与客服中台机器人',
    appIdOrBotId: 'bot_wxa8392170b',
    syncInterval: '按成员搜索并核验身份',
    status: 'synced',
    progressPercent: 95,
    lastSyncedAt: '35 分钟前 (2026-11-11 10:50:00)',
    snapshotVersion: 'snap-wecom-42m',
    snapshotNodeCount: 44,
    snapshotGeneratedAt: '2026-11-11 10:50:00',
    lastSuccessfulSnapshot: {
      snapshotVersion: 'snap-wecom-42m',
      syncedAt: '2026-11-11 10:50:00',
      totalMembers: 44,
      totalDepartments: 8
    },
    totalMembers: 44,
    totalDepartments: 8,
    history: [
      { syncId: 'sync-wc-301', trigger: 'manual', time: '10:50:00', duration: '0.6s', nodesSynced: 44, status: 'success', detail: '成员身份同步完成：42 名已核验，2 名待验证' }
    ]
  },
  {
    id: 'src-dingtalk',
    channel: 'dingtalk',
    connectionId: 'conn-emp-01-dingtalk',
    channelName: '钉钉开放平台',
    appName: 'SRE 运维团队微应用',
    appIdOrBotId: 'ding_sre_op_01',
    syncInterval: '每 60 分钟自动同步',
    status: 'synced',
    progressPercent: 100,
    lastSyncedAt: '12 分钟前 (2026-11-11 11:15:02)',
    snapshotVersion: 'snap-ding-34m',
    snapshotNodeCount: 34,
    snapshotGeneratedAt: '2026-11-11 11:15:02',
    lastSuccessfulSnapshot: {
      snapshotVersion: 'snap-ding-34m',
      syncedAt: '2026-11-11 11:15:02',
      totalMembers: 34,
      totalDepartments: 4
    },
    totalMembers: 34,
    totalDepartments: 4,
    history: [
      { syncId: 'sync-ding-108', trigger: 'periodic', time: '11:15:02', duration: '1.2s', nodesSynced: 34, status: 'success', detail: '钉钉通讯录同步完成：已同步 34 名 SRE 成员' }
    ]
  }
];

export const initialEmailInvitations: WorkspaceEmailInvitation[] = [
  {
    id: 'inv-01',
    email: 'consultant.wang@external-partner.com',
    name: '王咨询 (外部实施顾问)',
    targetEmployeeIds: ['emp-01'],
    invitedAt: '2026-11-10 14:00',
    expiresAt: '2026-11-17 14:00 (剩余 6 天)',
    status: 'pending'
  },
  {
    id: 'inv-02',
    email: 'sec-audit@security-firm.cn',
    name: '李安全 (等保三级审计专家)',
    targetEmployeeIds: ['emp-01', 'emp-03'],
    invitedAt: '2026-11-05 09:30',
    expiresAt: '2026-11-12 09:30 (剩余 1 天)',
    status: 'activated'
  }
];

export const initialDirectorySyncStatus: DirectorySyncStatus = {
  lastSyncedAt: '12 分钟前 (2026-11-11 11:15:00)',
  syncInterval: 'daily_0200',
  status: 'synced',
  totalSyncedCount: 128,
  errorCount: 0,
  errorLogs: [
    { time: '11:15:00', message: '飞书通讯录同步完成：已同步 128 个部门', channel: 'feishu' },
    { time: '11:15:02', message: '钉钉通讯录同步完成：已同步 34 名 SRE 成员', channel: 'dingtalk' }
  ]
};

export const mockEmployees: DigitalEmployee[] = [
  {
    id: 'emp-01',
    code: '#EMP-001',
    version: 1,
    etag: 'W/"emp-emp-01-v1"',
    name: '研发助手 AI',
    role: '负责代码审查、缺陷定位与发布巡检',
    avatar: 'cn-v2-03',
    status: 'active',
    executionEngine: 'pi',
    modelId: 'deepseek-v3',
    modelName: 'DeepSeek-V3 (工作区默认)',
    thinkingLevel: 'medium',
    prompt: '你是一位资深高级全栈研发工程师，负责代码审查、测试自动化与缺陷定位。回答需严谨、精确，发现高风险操作时主动请求确认。',
    unifiedAccess: {
      mode: 'all_with_blacklist',
      whitelistMemberIds: [],
      blacklistMemberIds: ['dir-fs-2'],
      allowGroupMembers: true
    },
    webChatAccess: {
      mode: 'all',
      allowMemberIds: [],
      denyMemberIds: []
    },
    handoffPolicy: {
      allowAll: false,
      mode: 'whitelist',
      allowEmployeeIds: ['emp-02'],
      denyEmployeeIds: []
    },
    channels: [
      {
        platform: 'feishu',
        platformName: '飞书机器人',
        connectionId: 'conn-emp-01-feishu',
        status: 'connected',
        statusText: '已连接',
        credentialStatus: 'saved',
        connectionState: 'connected',
        permissionReady: true,
        connectionMethod: 'existing_app',
        appId: 'cli_a891024feishu',
        appSecretMasked: 'sec_••••••••8a9f',
        lastVerifiedAt: '2026-11-11 11:30',
        businessCapabilities: [
          {
            id: 'cap-chat',
            name: '聊天与群消息',
            description: '单聊消息收发、群聊消息全量监听、群卡片投递',
            platformAuthStatus: 'authorized',
            resourceAccessStatus: 'ready',
            statusDetail: '已连接，具备消息与群聊读写权限 (im:message、im:chat)'
          },
          {
            id: 'cap-contact',
            name: '组织通讯录',
            description: '读取部门架构、检索员工信息与组织关系',
            platformAuthStatus: 'partial',
            resourceAccessStatus: 'ready',
            statusDetail: '已获部分部门权限 (技术研发中心、基础架构部)'
          },
          {
            id: 'cap-docs',
            name: '云文档与多维表格',
            description: '自动化读取排障文档、写入缺陷工单与表格',
            platformAuthStatus: 'authorized',
            resourceAccessStatus: 'pending_collaborator',
            statusDetail: '开放平台权限已获批；注意：具体文档仍需在飞书中将机器人添加为协作者方可读写'
          },
          {
            id: 'cap-meeting',
            name: '视频会议与日程',
            description: '创建紧急排障会议、日历预约与会议纪要转录',
            platformAuthStatus: 'unauthorized',
            resourceAccessStatus: 'not_configured',
            statusDetail: '尚未向飞书开放平台申请 vc:room 权限'
          }
        ],
        usageScope: {
          mode: 'whitelist',
          whitelistMembers: ['dir-fs-1', 'dir-fs-2'],
          whitelistDepartments: ['技术研发中心', '基础架构部'],
          blacklistMembers: [],
          allowedGroupIds: ['grp-feishu-01', 'grp-feishu-02']
        },
        capabilities: {
          supportsOrdinaryGroup: true,
          supportsKeywords: true,
          supportsRecentContext: true,
          ordinaryGroupEnabled: true,
          keywordTriggersEnabled: true,
          keywords: ['/review', '/fix', '前端报错', 'Bug排查'],
          regexMatchEnabled: true,
          regexPattern: '/(?:review|pr|mr)\\s*#?\\d+/i',
          feishuRules: [
            { id: 'fs-r1', type: 'keyword', name: '常用快捷指令', pattern: '/review, /fix, /diagnose', enabled: true, description: '研发日常快速评审与诊断' },
            { id: 'fs-r2', type: 'keyword', name: '故障缺陷词', pattern: '前端报错, Bug排查, 页面崩溃', enabled: true, description: '自动识别线上或测试环境缺陷' },
            { id: 'fs-r3', type: 'regex', name: 'PR/MR合并请求正则', pattern: '/(?:review|pr|mr)\\s*#?\\d+/i', enabled: true, description: '匹配如 PR #214 或 mr 58 提单' },
            { id: 'fs-r4', type: 'regex', name: '堆栈异常日志规则', pattern: '/(?:error|exception|stacktrace):\\s*\\S+/i', enabled: false, description: '自动捕获日志中的异常堆栈' }
          ],
          recentContextEnabled: true,
          recentContextWindow: 20
        },
        sharingRules: [
          {
            id: 'cgr-1',
            name: '研发排查读取客服群历史',
            mode: 'directed',
            sourceGroups: ['飞书「#技术支持咨询群」', '飞书「#客户技术支持咨询群」'],
            targetGroups: ['飞书「#前端研发核心群」'],
            status: 'active',
            syncStatus: 'applied',
            savedAt: '2026-11-10 14:20'
          },
          {
            id: 'cgr-2',
            name: '研发与发布群双向互通',
            mode: 'interconnect',
            sourceGroups: ['飞书「#前端研发核心群」', '飞书「#线上发布与值班群」'],
            targetGroups: [],
            status: 'active',
            syncStatus: 'applied',
            savedAt: '2026-11-10 16:30'
          }
        ],
        boundGroups: [
          { id: 'grp-feishu-01', name: '飞书「#前端研发核心群」', memberCount: 38, activeAt: '10分钟前' },
          { id: 'grp-feishu-02', name: '飞书「#线上发布与值班群」', memberCount: 14, activeAt: '35分钟前' }
        ]
      },
      {
        platform: 'wecom',
        platformName: '企业微信智能机器人',
        connectionId: 'ww_wecom_sec_01',
        status: 'connected',
        statusText: '已连接',
        credentialStatus: 'saved',
        connectionState: 'connected',
        permissionReady: true,
        connectionMethod: 'existing_app',
        botId: 'bot_wecom_sec_002',
        botSecretMasked: 'bsec_••••••••812e',
        corpId: 'ww719823091238',
        agentId: '1000002',
        appSecretMasked: 'sec_••••••••901b',
        lastVerifiedAt: '2026-11-11 11:15',
        usageScope: {
          mode: 'whitelist',
          whitelistMembers: ['dir-wc-1'],
          whitelistDepartments: ['法务合规部'],
          blacklistMembers: [],
          allowedGroupIds: ['grp-wecom-01']
        },
        capabilities: {
          supportsOrdinaryGroup: false,
          supportsKeywords: false,
          supportsRecentContext: true,
          recentContextEnabled: true,
          recentContextWindow: 10
        },
        sharingRules: [
          {
            id: 'cgr-wecom-1',
            name: '法务咨询定向读取支持群',
            mode: 'directed',
            sourceGroups: ['企业微信「#技术支持快速协同群」'],
            targetGroups: [],
            targetUserId: 'dir-wc-1',
            targetUserLabel: '赵法务 (企业微信)',
            status: 'active',
            syncStatus: 'auth_error',
            authErrorDetail: '目标人员赵法务尚未在企微端完成成员身份绑定，授权暂无法生效',
            savedAt: '2026-11-11 10:00'
          }
        ],
        boundGroups: [
          { id: 'grp-wecom-01', name: '企业微信「#技术支持快速协同群」', memberCount: 18, activeAt: '昨天' }
        ]
      },
      {
        platform: 'dingtalk',
        platformName: '钉钉机器人',
        connectionId: 'conn-emp-01-dingtalk',
        status: 'connected',
        statusText: '已连接',
        credentialStatus: 'saved',
        connectionState: 'connected',
        permissionReady: true,
        connectionMethod: 'existing_app',
        appKey: 'ding_stream_client_99',
        appSecretMasked: 'sec_••••••••663a',
        adminConsoleUrl: 'https://open-dev.dingtalk.com/fe/app#/corp/app',
        lastVerifiedAt: '2026-11-11 11:25',
        usageScope: {
          mode: 'all',
          whitelistMembers: [],
          whitelistDepartments: [],
          blacklistMembers: [],
          allowedGroupIds: ['grp-ding-01']
        },
        capabilities: {
          supportsOrdinaryGroup: false,
          supportsKeywords: false,
          supportsRecentContext: false
        },
        sharingRules: [],
        boundGroups: [
          { id: 'grp-ding-01', name: '钉钉「#架构技术委员会」', memberCount: 22, activeAt: '2小时前' }
        ]
      }
    ],
    capabilityPackages: [
      {
        id: 'pkg-gitlab-review',
        name: '代码库审查与 MR 差异分析包',
        description: '组合 GitLab 差异检出、AST 语法坏味道检查与 PR 评审报告生成。',
        enabled: true,
        isBound: true,
        resources: {
          skills: ['skill-git-review'],
          mcp: ['mcp-gitlab'],
          mcpTools: ['get_diff', 'create_mr_comment'],
          bash: false
        },
        scope: {
          userScope: {
            mode: 'inherit_with_blacklist',
            whitelistMemberIds: [],
            blacklistMemberIds: []
          },
          channelPolicies: [
            { platform: 'feishu', enabled: true, allowPrivate: true, allowGroup: true },
            { platform: 'wecom', enabled: false, allowPrivate: false, allowGroup: false },
            { platform: 'dingtalk', enabled: true, allowPrivate: true, allowGroup: true }
          ],
          conversationKinds: ['private', 'group'],
          triggerUsageScope: { mode: 'all', allowTriggerIds: [], denyTriggerIds: [] },
          handoffUsageScope: {
            mode: 'inherit_with_blacklist',
            allowEmployeeIds: [],
            denyEmployeeIds: []
          }
        }
      },
      {
        id: 'pkg-ci-deploy',
        name: 'Jenkins CI/CD 构建与测试执行包',
        description: '执行回归测试用例，导出覆盖率报告，禁止生产直发。',
        enabled: true,
        isBound: true,
        resources: {
          skills: ['skill-fe-diagnose'],
          mcp: ['mcp-jenkins'],
          mcpTools: ['trigger_build', 'get_build_status'],
          bash: true
        },
        scope: {
          userScope: {
            mode: 'whitelist_with_blacklist',
            whitelistMemberIds: ['dept-fs-fe', 'dir-web-1'],
            blacklistMemberIds: ['dir-fs-1']
          },
          channelPolicies: [
            { platform: 'feishu', enabled: true, allowPrivate: true, allowGroup: true },
            { platform: 'wecom', enabled: false, allowPrivate: false, allowGroup: false },
            { platform: 'dingtalk', enabled: false, allowPrivate: false, allowGroup: false }
          ],
          conversationKinds: ['group'],
          triggerUsageScope: { mode: 'allowlist', allowTriggerIds: ['trg-1', 'trg-2'], denyTriggerIds: [] },
          handoffUsageScope: {
            mode: 'whitelist',
            allowEmployeeIds: ['emp-02'],
            denyEmployeeIds: []
          }
        }
      }
    ],
    triggers: [
      {
        id: 'trg-1',
        name: '每日早 9 点 master 分支健康巡检',
        type: 'schedule',
        cron: '0 9 * * 1-5',
        timezone: 'Asia/Shanghai',
        allowedActions: ['create_task'],
        description: '检出 master 分支最新 commit，运行 lint 与单元测试，生成巡检卡片。',
        historyPolicy: 'isolated',
        enabled: true,
        notificationTargets: [
          { platform: 'feishu', connectionId: 'conn-emp-01-feishu', groupId: 'grp-feishu-01', groupName: '飞书「#前端研发核心群」' }
        ],
        executionLogs: [
          { id: 'log-101', triggeredAt: '今天 09:00:02', status: 'succeeded', duration: '14.2s', deliveryStatus: '已投递飞书群', summary: 'Lint 与 52 项单元测试全部通过' },
          { id: 'log-102', triggeredAt: '昨天 09:00:01', status: 'succeeded', duration: '13.8s', deliveryStatus: '已投递飞书群', summary: '无新增缺陷' }
        ]
      },
      {
        id: 'trg-2',
        name: 'GitHub Issue 自动化巡检与缺陷联动',
        type: 'github_issue',
        allowedActions: ['create_task'],
        githubRepo: 'kiko/frontend-core',
        githubIssueSyncMode: 'on_opened',
        description: '当收到 GitHub Issue 打开或更新时，自动化提取变更文件并分析。若需合入生产，挂起等待主管确认。',
        historyPolicy: 'employee_shared',
        enabled: true,
        notificationTargets: [
          { platform: 'feishu', connectionId: 'conn-emp-01-feishu', groupId: 'grp-feishu-01', groupName: '飞书「#前端研发核心群」' }
        ],
        executionLogs: [
          { id: 'log-201', triggeredAt: '今天 10:14:20', status: 'succeeded', duration: '8.4s', deliveryStatus: '已投递飞书群', summary: 'PR #214 性能审查完成' }
        ]
      },
      {
        id: 'trg-3',
        name: '外部告警 Webhook 签名调用',
        type: 'webhook',
        allowedActions: ['create_task', 'cancel_task'],
        webhookEndpoint: '/api/triggers/webhook/trg-3-alert-hook',
        keyId: 'key_sec_alert_092',
        secretMasked: 'whsec_••••••••4f2b',
        description: '接收云原生告警中心推送的 P0 告警，自动触发初步根因排查。',
        historyPolicy: 'isolated',
        enabled: true,
        notificationTargets: [
          { platform: 'feishu', connectionId: 'conn-emp-01-feishu', groupId: 'grp-feishu-02', groupName: '飞书「#线上发布与值班群」' }
        ],
        executionLogs: [
          { id: 'log-301', triggeredAt: '今天 11:02:11', status: 'succeeded', duration: '5.1s', deliveryStatus: '已投递飞书群', summary: '提取异常指标完成' }
        ]
      }
    ],
    humanConfirmationRules: [
      {
        id: 'hcr-1',
        questionScope: 'deploy-prod',
        target: {
          kind: 'platform_member',
          label: '@youzai (工作区所有者)',
          userId: 'm-1',
          provider: 'platform'
        },
        fallbackChannel: '平台对话'
      },
      {
        id: 'hcr-2',
        questionScope: 'wecom-ops-decision',
        target: {
          kind: 'channel_user',
          label: '赵法务 (企业微信)',
          userId: 'dir-wc-1',
          provider: 'wecom',
          connectionId: 'ww_wecom_sec_01',
          identityVerified: false // 企微待核验
        },
        fallbackChannel: '企业微信私聊卡片'
      }
    ],
    stats: {
      todayTasks: 18,
      todayConversations: 34,
      successRate: '98.5%'
    }
  },
  {
    id: 'emp-02',
    code: '#EMP-002',
    version: 1,
    etag: 'W/"emp-emp-02-v1"',
    name: '客户支持助手',
    role: '负责售前咨询初筛、客诉Bug登记与跨群转交',
    avatar: 'cn-v2-08',
    status: 'active',
    executionEngine: 'pi',
    modelId: 'qwen-max',
    modelName: '通义千问 Qwen-Max',
    thinkingLevel: 'none',
    prompt: '你是一位友好专业的技术支持顾问，负责初筛客户问题。若发现系统故障，提取关键错误码并交接给研发助手。',
    unifiedAccess: {
      mode: 'whitelist_with_blacklist',
      whitelistMemberIds: ['dept-fs-fe', 'dir-web-1'],
      blacklistMemberIds: ['dir-fs-1'],
      allowGroupMembers: true
    },
    webChatAccess: {
      mode: 'all',
      allowMemberIds: [],
      denyMemberIds: []
    },
    handoffPolicy: {
      allowAll: true,
      mode: 'all_with_blacklist',
      allowEmployeeIds: [],
      denyEmployeeIds: []
    },
    channels: [
      {
        platform: 'feishu',
        platformName: '飞书机器人',
        connectionId: 'conn-emp-02-feishu',
        status: 'connected',
        statusText: '已连接',
        credentialStatus: 'saved',
        connectionState: 'connected',
        permissionReady: true,
        connectionMethod: 'existing_app',
        appId: 'cli_cust_service_01',
        appSecretMasked: 'sec_••••••••412c',
        lastVerifiedAt: '2026-11-11 10:00',
        usageScope: {
          mode: 'all',
          whitelistMembers: [],
          whitelistDepartments: [],
          blacklistMembers: [],
          allowedGroupIds: ['grp-feishu-cust-01']
        },
        capabilities: {
          supportsOrdinaryGroup: true,
          supportsKeywords: true,
          supportsRecentContext: true,
          ordinaryGroupEnabled: true,
          keywordTriggersEnabled: false,
          recentContextEnabled: true,
          recentContextWindow: 15
        },
        sharingRules: [],
        boundGroups: [
          { id: 'grp-feishu-cust-01', name: '飞书「#客户技术支持咨询群」', memberCount: 64, activeAt: '5分钟前' }
        ]
      }
    ],
    capabilityPackages: [],
    triggers: [],
    humanConfirmationRules: [
      {
        id: 'hcr-3',
        questionScope: 'sla-compensation',
        target: {
          kind: 'platform_member',
          label: '王敏 (客服主管)',
          userId: 'm-3',
          provider: 'platform'
        },
        fallbackChannel: '飞书群卡片'
      }
    ],
    stats: {
      todayTasks: 12,
      todayConversations: 52,
      successRate: '99.1%'
    }
  },
  {
    id: 'emp-03',
    code: '#EMP-003',
    version: 1,
    etag: 'W/"emp-emp-03-v1"',
    name: '待入职数据分析助手',
    role: '新创建尚未完成渠道与权限配置',
    avatar: 'cn-v2-14',
    status: 'pending_setup',
    executionEngine: 'pi',
    modelId: 'deepseek-v3',
    modelName: 'DeepSeek-V3',
    thinkingLevel: 'none',
    prompt: '你是一位新入职的 AI 员工，待管理员接入飞书、企微或钉钉渠道。',
    unifiedAccess: {
      mode: 'all_with_blacklist',
      whitelistMemberIds: [],
      blacklistMemberIds: [],
      allowGroupMembers: true
    },
    webChatAccess: {
      mode: 'all',
      allowMemberIds: [],
      denyMemberIds: []
    },
    handoffPolicy: {
      allowAll: true,
      mode: 'all_with_blacklist',
      allowEmployeeIds: [],
      denyEmployeeIds: []
    },
    channels: [],
    capabilityPackages: [],
    triggers: [],
    humanConfirmationRules: [],
    stats: {
      todayTasks: 0,
      todayConversations: 0,
      successRate: '100%'
    }
  }
];

// 隔离的网页端独立会话 Mock
export const mockEmployeeSessions: Record<string, EmployeeConversationSession[]> = {
  'emp-01': [
    {
      id: 'sess-dev-01',
      employeeId: 'emp-01',
      title: '研发助手 AI · 专属持久协同会话',
      createdAt: '2026-11-11 09:30',
      updatedAt: '2026-11-11 11:08',
      messages: [
        {
          id: 'msg-1',
          sender: 'user',
          content: '请协助审查 master 分支最新的 PR #214，测试首屏加载时间并生成分析报告。',
          time: '09:30'
        },
        {
          id: 'msg-2',
          sender: 'assistant',
          content: '收到任务！我已运行 Lighthouse 测试与包体积比对。\n\n分析结果如下：\n- 首屏打包体积增加：3.4KB (符合 <10KB 规范)\n- Lighthouse 性能评分：94 分\n- 报告文件已导出为 HTML 格式。',
          time: '09:32',
          thinking: '调用 GitLab 提取变更行数 ➔ 执行性能测试 ➔ 检查报告输出',
          generatedTaskId: 'task_run_891024'
        },
        {
          id: 'msg-3',
          sender: 'user',
          content: '请准备执行生产发布脚本 ./scripts/deploy-prod.sh --version=v2.4.0',
          time: '11:05'
        },
        {
          id: 'msg-4',
          sender: 'assistant',
          content: '检测到该指令涉及生产环境变更，已暂停执行并提交待人工确认，请管理员审批后继续。',
          time: '11:08',
          thinking: '识别到生产环境发布指令 ➔ 需要高风险审批 ➔ 提交至待人工确认中心',
          generatedTaskId: 'task_run_891025'
        }
      ],
      sessionTasks: [
        {
          id: 'tsk-001',
          taskId: 'task_run_891024',
          workItemId: 'wi-1038',
          employeeName: '研发助手 AI',
          employeeId: 'emp-01',
          channelGroup: '网页端会话 (Web Chat)',
          platform: 'web',
          toolUsed: 'GitLab MCP / check-diff',
          status: 'succeeded',
          delivery: 'sent',
          duration: '14.2s',
          createdAt: '09:30:15',
          outputArtifact: '产物：PR-214-Performance-Report.html (体积 142KB)',
          retryHistory: [
            { runIndex: 1, time: '09:30:15', duration: '14.2s', result: '执行成功并生成报告' }
          ],
          supplementAdopted: true,
          logs: [
            '[09:30:15] [INFO] 收到网页会话指令：审查 PR #214',
            '[09:30:16] [EXEC] git fetch origin pull/214/head:pr-214',
            '[09:30:24] [OK] 测试套件全部通过 (52/52)',
            '[09:30:29] [DONE] 报告卡片投递完成'
          ]
        },
        {
          id: 'tsk-002',
          taskId: 'task_run_891025',
          workItemId: 'wi-1038',
          employeeName: '研发助手 AI',
          employeeId: 'emp-01',
          channelGroup: '飞书「#线上发布与值班群」',
          platform: 'feishu',
          toolUsed: 'Local Bash / deploy-prod.sh',
          status: 'waiting_approval',
          delivery: 'pending',
          duration: '2.1s (已安全挂起)',
          createdAt: '11:08:44',
          outputArtifact: '待执行脚本草稿已归档，等待管理员核准',
          retryHistory: [],
          supplementAdopted: false,
          logs: [
            '[11:08:44] [INFO] 收到指令：执行生产环境发布脚本',
            '[11:08:45] [WARN] 命中高风险安全策略：触碰生产写操作',
            '[11:08:45] [CHECKPOINT] 任务已暂停，等待人工确认'
          ]
        }
      ]
    }
  ],
  'emp-02': [
    {
      id: 'sess-cust-01',
      employeeId: 'emp-02',
      title: '客户登录 502 超时问题初筛与交接',
      createdAt: '2026-11-11 10:15',
      updatedAt: '2026-11-11 10:20',
      messages: [
        {
          id: 'msg-201',
          sender: 'user',
          content: '客户在群里反映今早 10 点登录接口偶现 502 超时，请确认情况。',
          time: '10:15'
        },
        {
          id: 'msg-202',
          sender: 'assistant',
          content: '已获取报错详情与时间点。由于属于线上系统问题，我已创建协同事项 #WI-1042 并转交给「研发助手 AI」跟进排查。',
          time: '10:18',
          thinking: '识别为系统故障 ➔ 提取错误特征 ➔ 转交给对应负责员工',
          generatedTaskId: 'task_run_891028'
        }
      ],
      sessionTasks: [
        {
          id: 'tsk-007',
          taskId: 'task_run_891028',
          workItemId: 'wi-1042',
          employeeName: '客户支持助手',
          employeeId: 'emp-02',
          channelGroup: '飞书「#客户技术支持咨询群」',
          platform: 'feishu',
          toolUsed: 'WorkItem Handoff / create-and-dispatch',
          status: 'succeeded',
          delivery: 'sent',
          duration: '3.2s',
          createdAt: '10:18:10',
          outputArtifact: '已立项 #WI-1042 并交接至「研发助手 AI」',
          retryHistory: [
            { runIndex: 1, time: '10:18:10', duration: '3.2s', result: '已创建事项并完成跨群交接' }
          ],
          supplementAdopted: false,
          logs: [
            '[10:18:10] [INFO] 捕获飞书客户群 502 超时报错上下文',
            '[10:18:12] [HANDOFF] 创建协同事项 #WI-1042，转派给「研发助手 AI」',
            '[10:18:13] [DONE] 已向客户群同步受理状态与事项编号'
          ]
        }
      ]
    }
  ]
};

// 模拟协作事项
export const mockWorkItems: WorkItem[] = [
  {
    id: 'wi-1042',
    code: '#WI-1042',
    title: '客户反馈登录接口偶现 502 网关超时缺陷排查',
    status: 'in_progress',
    originPlatform: '飞书',
    originGroup: '飞书「#客户技术支持咨询群」',
    originEmployeeId: 'emp-02',
    ownerEmployeeId: 'emp-01',
    goal: '定位 10:00~10:15 期间网关偶现 502 的具体原因并修复。',
    criteria: '出具根因定位报告，并在测试环境复现验证通过。',
    currentSummary: '客服助手初筛提取错误码为 HTTP 502，已跨群转交研发助手。研发助手正在分析 nginx 日志。',
    nextStep: '提取生产 upstream 超时日志，排查 DB 连接池占用率。',
    createdAt: '2026-11-11 10:15',
    updatedAt: '2026-11-11 10:45',
    linkedTaskIds: ['task_run_891024'],
    supplementAdopted: true,
    assignments: [
      { id: 'asg-1', employeeName: '客户支持助手', sourceEmployeeName: '客户支持群用户', action: '接收报障并初筛', timestamp: '10:15' },
      { id: 'asg-2', employeeName: '研发助手 AI', sourceEmployeeName: '客户支持助手', action: '跨群转交 (Handoff)', timestamp: '10:18' }
    ],
    events: [
      { id: 'ev-1', type: 'handoff', title: '跨群交接成功', time: '10:18', detail: '从客服咨询群转交至前端研发核心群' },
      { id: 'ev-2', type: 'user_note', title: '管理员补充了排查提示', time: '10:30', detail: '建议排查双十一活动页的防刷限流中间件' }
    ],
    deliveryStatus: '已同步至原客服群',
    deliveryCardPreview: {
      title: '事项进展回告卡片 · #WI-1042',
      fields: [
        { label: '当前状态', value: '研发排查中 (推进中)' },
        { label: '初步结论', value: '排查到限流组件突发峰值触发软拒绝' },
        { label: '预计完成', value: '11:45 重新发布修正热补丁' }
      ]
    }
  },
  {
    id: 'wi-1038',
    code: '#WI-1038',
    title: '前端发布 PR #214 性能基准测试与自动化审查',
    status: 'awaiting_acceptance',
    originPlatform: '飞书',
    originGroup: '飞书「#前端研发核心群」',
    originEmployeeId: 'emp-01',
    ownerEmployeeId: 'emp-01',
    goal: '对 master 分支 PR #214 进行首屏加载时间对比与打包体积分析。',
    criteria: '体积增长不超过 10KB，Lighthouse 性能得分不低于 90。',
    currentSummary: '自动化测试完成，体积增加 3.4KB，Lighthouse 评分 94，已生成对比报告。',
    nextStep: '等待技术主管 @youzai 验收通过后合并。',
    createdAt: '2026-11-11 09:30',
    updatedAt: '2026-11-11 10:10',
    linkedTaskIds: ['task_run_891024', 'task_run_891025'],
    supplementAdopted: true,
    assignments: [
      { id: 'asg-3', employeeName: '研发助手 AI', sourceEmployeeName: '研发助手 AI', action: '命中群关键词 /review 自动触发', timestamp: '09:30' }
    ],
    events: [
      { id: 'ev-4', type: 'acceptance_requested', title: '提交成果待验收', time: '10:10', detail: '测试用例全部通过，已在群内生成性能报告卡片' }
    ],
    deliveryStatus: '已在原群回告卡片'
  },
  {
    id: 'wi-1029',
    code: '#WI-1029',
    title: '双十一线上数据库每日容量与慢查询例行巡检',
    status: 'completed',
    originPlatform: '钉钉',
    originGroup: '钉钉「#SRE运维应急值班群」',
    originEmployeeId: 'emp-01',
    ownerEmployeeId: 'emp-01',
    goal: '全量扫描慢查询日志超过 1000ms 的 SQL，汇总发至值班群。',
    criteria: '自动归档 TOP5 慢查询语句，形成调优建议。',
    currentSummary: '巡检完成，发现 2 条未命中索引的慢查询，已附执行计划回告至钉钉群。',
    createdAt: '2026-11-11 08:00',
    updatedAt: '2026-11-11 08:08',
    linkedTaskIds: ['task_run_891020'],
    assignments: [
      { id: 'asg-4', employeeName: '研发助手 AI', sourceEmployeeName: '定时触发器', action: 'Cron 调度执行', timestamp: '08:00' }
    ],
    events: [
      { id: 'ev-5', type: 'accepted', title: '巡检报告自动归档', time: '08:08', detail: '指标在正常水位，事项正常闭环' }
    ],
    deliveryStatus: '已成功投递钉钉'
  },
  {
    id: 'wi-1045',
    code: '#WI-1045',
    title: '临时测试环境一键销毁与数据擦除',
    status: 'canceled',
    originPlatform: '企业微信',
    originGroup: '企业微信「#QA测试协作群」',
    originEmployeeId: 'emp-01',
    ownerEmployeeId: 'emp-01',
    goal: '自动回收 staging-04 临时测试容器集群及数据卷。',
    criteria: '容器销毁并解除 DNS 绑定。',
    currentSummary: '发起人已在群内发送终止指令，管理员已于 10:20 取消该事项。已取消事项不可直接验收。',
    createdAt: '2026-11-11 10:18',
    updatedAt: '2026-11-11 10:20',
    linkedTaskIds: [],
    assignments: [
      { id: 'asg-5', employeeName: '研发助手 AI', sourceEmployeeName: 'QA工程师', action: '企微指令触发', timestamp: '10:18' }
    ],
    events: [
      { id: 'ev-6', type: 'canceled', title: '事项已被人工取消', time: '10:20', detail: '因测试任务延长，发起人申请取消环境回收。' }
    ],
    deliveryStatus: '已通知企微群终止'
  },
  {
    id: 'wi-1050',
    code: '#WI-1050',
    title: '跨云双活同步网关延迟突增现场核对',
    status: 'unknown',
    originPlatform: '钉钉',
    originGroup: '钉钉「#网络与基础设施值班」',
    originEmployeeId: 'emp-01',
    ownerEmployeeId: 'emp-01',
    goal: '探测专线打通状态并拉取各区域心跳包延迟。',
    criteria: '双向探针 RTT 小于 20ms。',
    currentSummary: '远端服务响应超时，当前同步状态待确认，请先核对实际状态后再决定后续操作。',
    nextStep: '执行现场轻量级 ping/traceroute 探测，核对远端执行结果。',
    createdAt: '2026-11-11 11:10',
    updatedAt: '2026-11-11 11:22',
    linkedTaskIds: ['task_run_891035'],
    assignments: [
      { id: 'asg-6', employeeName: '研发助手 AI', sourceEmployeeName: 'SRE运维', action: '探针告警联动', timestamp: '11:10' }
    ],
    events: [
      { id: 'ev-7', type: 'unknown_warning', title: '网关探针超时未返回', time: '11:22', detail: '上游网关连接未建立，状态未知待核对。' }
    ],
    deliveryStatus: '挂起中'
  },
  {
    id: 'wi-1055',
    code: '#WI-1055',
    title: '大客户定制数据导出与合规交接流转',
    status: 'waiting_input',
    originPlatform: '飞书',
    originGroup: '飞书「#大客户商务保障群」',
    originEmployeeId: 'emp-02',
    ownerEmployeeId: 'emp-02',
    goal: '导出近 30 天脱敏审计流水，跨群交接法务 AI 员工复核。',
    criteria: '法务出具脱敏确认函并签署数字签名。',
    currentSummary: '客服助手已初筛生成流水草稿，但在交接至法务助理时受阻，等待处理中。',
    waitReason: '交接受阻：目标法务助理 AI 员工未配置企微导出能力包，且当前会话无授权，流转停滞。',
    nextStep: '建议重新转派具备合规权限的研发助手，或转为人工直接接管处理。',
    createdAt: '2026-11-11 11:00',
    updatedAt: '2026-11-11 11:25',
    linkedTaskIds: [],
    assignments: [
      { id: 'asg-7', employeeName: '客户支持助手', sourceEmployeeName: '商务经理', action: '接收导出诉求', timestamp: '11:00' }
    ],
    events: [
      { id: 'ev-8', type: 'handoff_blocked', title: '跨员工交接受阻', time: '11:25', detail: '目标员工权限不足无法承接，等待平台管理员介入转派或人工接管。' }
    ],
    deliveryStatus: '群内等待中'
  }
];

// 模拟执行任务
export const mockTasks: TaskRecord[] = [
  {
    id: 'tsk-001',
    taskId: 'task_run_891024',
    workItemId: 'wi-1042',
    employeeName: '研发助手 AI',
    employeeId: 'emp-01',
    channelGroup: '飞书「#前端研发核心群」',
    platform: 'feishu',
    toolUsed: 'GitLab MCP / check-diff',
    status: 'succeeded',
    delivery: 'sent',
    duration: '14.2s',
    createdAt: '11:05:12',
    outputArtifact: '产物：PR-214-Performance-Report.html (体积 142KB)',
    runs: [
      {
        runIndex: 1,
        startedAt: '09:30:15',
        duration: '14.2s',
        status: 'succeeded',
        triggerBy: '飞书群命中 /review 指令',
        toolExecutions: [
          {
            id: 'call-1',
            toolName: 'mcp-gitlab:get_diff',
            inputArgs: '{"repo": "frontend-core", "source_branch": "feature/pr-214", "target_branch": "master"}',
            outputResult: '{"changed_files": 4, "insertions": 128, "deletions": 34, "diff_stat": "+94 lines"}',
            duration: '2.1s',
            status: 'success'
          },
          {
            id: 'call-2',
            toolName: 'mcp-jenkins:trigger_build',
            inputArgs: '{"job": "frontend-perf-lighthouse", "params": {"PR_NUM": 214}}',
            outputResult: '{"build_num": 884, "lighthouse_score": 94, "bundle_size_diff": "+3.4KB"}',
            duration: '10.8s',
            status: 'success'
          }
        ],
        logs: [
          '[09:30:15] [INFO] 收到飞书群事件：/review PR #214',
          '[09:30:16] [MCP] 调用 GitLab MCP: get_diff 提取代码变更',
          '[09:30:18] [MCP] 调用 Jenkins MCP: 触发 Lighthouse 自动化基准性能构建',
          '[09:30:29] [OK] 测试套件全部通过 (52/52)，性能得分 94',
          '[09:30:29] [DELIVERY] 生成对比报告卡片并推送飞书群 Webhook'
        ],
        outputArtifact: '产物：PR-214-Performance-Report.html (体积 142KB)'
      }
    ],
    materials: [
      {
        id: 'mat-1',
        name: 'GitLab 仓库只读权限 (frontend-core)',
        resourceType: 'repository',
        authStatus: 'authorized',
        authScope: 'read:repository, read:merge_requests',
        grantedBy: '工作区预设凭据 (GitLab MCP)'
      },
      {
        id: 'mat-2',
        name: 'Jenkins CI 流水线触发授权',
        resourceType: 'api',
        authStatus: 'authorized',
        authScope: 'build:execute, build:read',
        grantedBy: 'SRE 管理员已授权'
      }
    ],
    inputRevisions: [
      {
        id: 'rev-1',
        submittedAt: '09:30:20',
        author: '张伟 (系统架构师)',
        content: '重点分析 chunk-vendors.js 是否引入未 Tree-shaking 的 lodash 全量包',
        intent: 'execute',
        adoptionStatus: 'adopted',
        effectiveStage: '在 Run #1 构建分析中采纳',
        notes: '构建脚本已将 lodash 导入检查列入报告重点分析段落'
      }
    ],
    deliveries: [
      {
        id: 'del-1',
        targetChannel: '飞书「#前端研发核心群」',
        recipientType: 'group_webhook',
        recipientName: '#前端研发核心群',
        status: 'delivered',
        sentAt: '09:30:29',
        statusCode: '200 OK',
        statusDetail: '对方已确认接收并完成群内卡片渲染回告',
        resendCount: 0
      }
    ],
    retryHistory: [
      { runIndex: 1, time: '11:05:12', duration: '14.2s', result: '第一次执行成功' }
    ],
    supplementAdopted: true,
    logs: [
      '[11:05:12] [INFO] 收到飞书群指令：/review PR #214',
      '[11:05:13] [INFO] 启动执行环境...',
      '[11:05:15] [EXEC] git fetch origin pull/214/head:pr-214',
      '[11:05:18] [EXEC] npm run lint:check && npm run test:perf',
      '[11:05:24] [OK] 测试套件全部通过 (52/52)',
      '[11:05:26] [DONE] 已向飞书群发送结果卡片'
    ]
  },
  {
    id: 'tsk-002',
    taskId: 'task_run_891025',
    workItemId: 'wi-1038',
    employeeName: '研发助手 AI',
    employeeId: 'emp-01',
    channelGroup: '飞书「#线上发布与值班群」',
    platform: 'feishu',
    toolUsed: 'Local Bash / deploy-prod.sh',
    status: 'waiting_approval',
    delivery: 'pending',
    duration: '2.1s (挂起中)',
    createdAt: '11:08:44',
    outputArtifact: '待执行脚本草稿已归档，等待管理员授权',
    retryHistory: [],
    supplementAdopted: false,
    logs: [
      '[11:08:44] [INFO] 收到指令：执行生产环境发布脚本',
      '[11:08:45] [WARN] 命中高风险安全策略：触碰生产环境写操作',
      '[11:08:45] [CHECKPOINT] 任务已暂停，等待人工确认'
    ]
  },
  {
    id: 'tsk-003',
    taskId: 'task_run_891020',
    workItemId: 'wi-1029',
    employeeName: '研发助手 AI',
    employeeId: 'emp-01',
    channelGroup: '钉钉「#SRE运维应急值班群」',
    platform: 'dingtalk',
    toolUsed: 'Prometheus MCP / query_range',
    status: 'succeeded',
    delivery: 'sent',
    duration: '4.8s',
    createdAt: '10:50:00',
    outputArtifact: '巡检报告 JSON 已投递至钉钉群 Webhook',
    retryHistory: [
      { runIndex: 1, time: '10:50:00', duration: '4.8s', result: '巡检成功' }
    ],
    supplementAdopted: false,
    logs: [
      '[10:50:00] [CRON] 定时任务触发：线上高负荷指标巡检',
      '[10:50:02] [QUERY] node_cpu_seconds_total 10m 平均值',
      '[10:50:04] [INFO] CPU 峰值 48.2%，内存使用率 61.4%，状态安全',
      '[10:50:05] [DONE] 心跳包上报完成'
    ]
  },
  {
    id: 'tsk-004',
    taskId: 'task_run_891033',
    workItemId: 'wi-1042',
    employeeName: '研发助手 AI',
    employeeId: 'emp-01',
    channelGroup: '飞书「#前端研发核心群」',
    platform: 'feishu',
    toolUsed: 'CDN Uploader / sync-assets',
    status: 'succeeded',
    delivery: 'failed',
    duration: '18.4s',
    createdAt: '11:15:30',
    outputArtifact: '构建产物 dist-v2.4.tar.gz 已上传至 OSS，但飞书群消息发送失败',
    materials: [
      {
        id: 'mat-cdn-1',
        name: 'CDN 静态资源写入权限 (oss:assets-bucket)',
        resourceType: 'api',
        authStatus: 'authorized',
        authScope: 'oss:PutObject, oss:DeleteObject',
        grantedBy: 'CI/CD 构建角色已授权'
      }
    ],
    deliveries: [
      {
        id: 'del-cdn-1',
        targetChannel: '飞书「#前端研发核心群」',
        recipientType: 'group_webhook',
        recipientName: '#前端研发核心群',
        status: 'failed',
        sentAt: '11:15:48',
        statusCode: '发送失败',
        statusDetail: '飞书渠道连接波动，消息未发送成功，可尝试重发通知。',
        resendCount: 1
      }
    ],
    retryHistory: [
      { runIndex: 1, time: '11:15:30', duration: '18.4s', result: '任务执行成功，回告失败' }
    ],
    supplementAdopted: true,
    logs: [
      '[11:15:30] [INFO] 任务执行完成，产物已成功生成并存入 OSS',
      '[11:15:48] [WARN] 发送飞书群消息失败：服务暂时不可用',
      '[11:15:48] [ERROR] 群通知发送失败（任务本身已成功完成，可直接重发通知）'
    ]
  },
  {
    id: 'tsk-005',
    taskId: 'task_run_891034',
    workItemId: 'wi-1038',
    employeeName: '研发助手 AI',
    employeeId: 'emp-01',
    channelGroup: '飞书「#线上发布与值班群」',
    platform: 'feishu',
    toolUsed: 'Direct Shell / rm-rf-deprecated-cluster.sh',
    status: 'rejected',
    delivery: 'pending',
    duration: '0.5s',
    createdAt: '10:40:12',
    rejectReason: '安全管理员判定此脚本包含非白名单生产集群强制删除逻辑，已人工安全拒绝',
    outputArtifact: '高风险指令已硬终止，拒绝原因已进入安全存证流水',
    retryHistory: [],
    supplementAdopted: false,
    logs: [
      '[10:40:12] [SECURITY] 检测到高危集群销毁指令申请执行',
      '[10:40:15] [AUDIT] 管理员「youzai」在审批中心判定存在不可逆风险，执行「拒绝并终止」',
      '[10:40:15] [REJECTED] 任务已被人工拒绝并终止，如需处理请重新提交申请。'
    ]
  },
  {
    id: 'tsk-006',
    taskId: 'task_run_891035',
    workItemId: 'wi-1050',
    employeeName: '研发助手 AI',
    employeeId: 'emp-01',
    channelGroup: '钉钉「#网络与基础设施值班」',
    platform: 'dingtalk',
    toolUsed: 'CrossCloud Gateway / health-probe',
    status: 'unknown',
    delivery: 'pending',
    duration: '30.0s (超时)',
    createdAt: '11:10:00',
    outputArtifact: '网络探针检测中途连接中断，结果待确认',
    materials: [
      {
        id: 'mat-probe-1',
        name: '跨云网络探针检测授权 (probe:network)',
        resourceType: 'api',
        authStatus: 'insufficient_scope',
        authScope: 'probe:network:internal',
        statusReason: '跨云专用网关探测需要合规安全审计二阶段授权，当前权限不足',
        grantedBy: '网络运维组'
      }
    ],
    deliveries: [
      {
        id: 'del-probe-1',
        targetChannel: '钉钉「#网络与基础设施值班」',
        recipientType: 'group_webhook',
        recipientName: '#网络与基础设施值班',
        status: 'unknown',
        sentAt: '11:10:00',
        statusCode: '504 Gateway Timeout',
        statusDetail: '远端服务超过 30s 未响应，结果待确认，请先核对实际状态。',
        resendCount: 0
      }
    ],
    retryHistory: [],
    supplementAdopted: false,
    logs: [
      '[11:10:00] [PROBE] 向跨云双活同步网关发送心跳探针请求...',
      '[11:10:30] [TIMEOUT] 远端探针网关超过 30s 未响应，收到 HTTP 504 Gateway Timeout',
      '[11:10:30] [UNKNOWN] 远端结果暂未返回，请先核对实际状态。'
    ]
  },
  {
    id: 'tsk-007',
    taskId: 'task_run_891028',
    workItemId: 'wi-1042',
    employeeName: '客户支持助手',
    employeeId: 'emp-02',
    channelGroup: '飞书「#客户技术支持咨询群」',
    platform: 'feishu',
    toolUsed: 'WorkItem Handoff / create-and-dispatch',
    status: 'succeeded',
    delivery: 'sent',
    duration: '3.2s',
    createdAt: '10:18:10',
    outputArtifact: '已立项 #WI-1042 并交接至「研发助手 AI」',
    retryHistory: [
      { runIndex: 1, time: '10:18:10', duration: '3.2s', result: '已创建事项并完成跨群交接' }
    ],
    supplementAdopted: false,
    logs: [
      '[10:18:10] [INFO] 捕获飞书客户群 502 超时报错上下文',
      '[10:18:12] [HANDOFF] 创建协同事项 #WI-1042，按交接白名单转派给「研发助手 AI」',
      '[10:18:13] [DONE] 已向客户群同步受理状态与事项编号'
    ]
  }
];

// 模拟人在回路待确认项 (包含通知受阻与重新选择确认人)
export const mockHumanApprovals: HumanApproval[] = [
  {
    id: 'appr-01',
    taskId: 'task_run_891025',
    workItemId: 'wi-1038',
    employeeId: 'emp-01',
    employeeName: '研发助手 AI',
    channelGroup: '飞书「#线上发布与值班群」',
    initiator: '@youzai (私聊委派)',
    kind: 'risk_command',
    riskLevel: 'high',
    questionScope: 'deploy-prod',
    title: '生产环境前端静态资源热发布申请',
    content: `#!/bin/bash
# 变更范围: 前端生产集群静态资源同步
./scripts/deploy-prod.sh --version=v2.4.0 --target=oss-prod-cluster
-- 风险影响: 将覆盖线上 100% 流量的静态资产`,
    contextReason: 'master 分支自动化测试已通过（评分 94），准备发布 v2.4.0 生产环境，需管理员二次核准。',
    createdAt: '2026-11-11 11:08',
    status: 'pending',
    notificationStatus: 'sent',
    recipientLabel: '@youzai (工作区所有者)'
  },
  {
    id: 'appr-02',
    taskId: 'task_run_891029',
    workItemId: 'wi-1042',
    employeeId: 'emp-02',
    employeeName: '客户支持助手',
    channelGroup: '飞书「#客户技术支持咨询群」',
    initiator: '客户群业务会话',
    kind: 'business_decision',
    riskLevel: 'medium',
    questionScope: 'sla-compensation',
    title: '客户要求承诺 SLA 故障补偿方案，需业务主管裁决',
    content: '客户在群内要求：针对今早超时的 15 分钟，申请下月费用 5% 的 SLA 抵扣。由于标准合同未约定该条款，请主管确认是否同意。',
    contextReason: '非标业务诉求，超出 AI 员工预设权限范围。',
    options: ['同意客户抵扣申请 (特殊安抚)', '拒绝抵扣，发送标准致歉邮件', '转交专职大客户经理线下沟通'],
    optionImpacts: {
      '同意客户抵扣申请 (特殊安抚)': {
        impact: '将触发下月财务账单 5% 优惠减免抵扣逻辑，并在飞书客户群自动发送故障安抚专用标准函件。',
        consequence: '对本季度技术服务预算带来额外支出，需自动抄送财务结算中心复核。'
      },
      '拒绝抵扣，发送标准致歉邮件': {
        impact: '向客户群回告根据合同 SLA 细则未达单次补偿阈值的官方答复，并附上详细排查根因与改进报告。',
        consequence: '存在客户体验降低并继续向高层申诉争议的客诉升级风险。'
      },
      '转交专职大客户经理线下沟通': {
        impact: '暂停机器人在此问题上的群内自动回复，立即向大客户经理发起企微/钉钉协同待办，转为人工线下关怀。',
        consequence: '避免在公开大群产生非标承诺争议，为业务客情维护留出充足缓冲期。'
      }
    },
    createdAt: '2026-11-11 11:20',
    status: 'pending',
    notificationStatus: 'sent',
    recipientLabel: '王敏 (客服主管)'
  },
  {
    id: 'appr-03',
    taskId: 'task_run_891032',
    workItemId: 'wi-1042',
    employeeId: 'emp-01',
    employeeName: '研发助手 AI',
    channelGroup: '企业微信「#技术支持快速协同群」',
    initiator: '企微协同事件',
    kind: 'missing_info',
    riskLevel: 'medium',
    questionScope: 'wecom-ops-decision',
    title: '企微确认人未绑定身份 · 确认请求受阻',
    content: '当前确认规则指定的企业微信确认人「赵法务」尚未在企微端完成成员身份核验与绑定，导致审批卡片无法送达。',
    contextReason: '没有具备员工使用权的有效确认人，请检查使用范围与确认人配置。',
    createdAt: '2026-11-11 11:28',
    status: 'pending',
    notificationStatus: 'blocked',
    blockedReason: '没有具备员工使用权的有效确认人，请检查使用范围或重新选择确认人。',
    recipientLabel: '赵法务 (企业微信 · 待核验)'
  }
];

// 组织部门架构 Mock (支持以部门为维度授权，并支持排除个别人)
export const mockDirectoryDepartments: ChannelDirectoryDepartment[] = [
  { id: 'dept-web-admin', refId: 'web_dept_admin', name: '管理组 · 平台Owner', platform: 'web', sourceType: 'web', connectionId: 'ws-web-directory', memberCount: 1 },
  { id: 'dept-web-ops', refId: 'web_dept_ops', name: '服务运维部', platform: 'web', sourceType: 'web', connectionId: 'ws-web-directory', memberCount: 1 },
  { id: 'dept-fs-fe', refId: 'fs_dept_fe_01', name: '前端研发中心', platform: 'feishu', sourceType: 'feishu', connectionId: 'conn-emp-01-feishu', memberCount: 3 },
  { id: 'dept-fs-infra', refId: 'fs_dept_infra_02', name: '基础架构部', platform: 'feishu', sourceType: 'feishu', connectionId: 'conn-emp-01-feishu', memberCount: 2 },
  { id: 'dept-wc-legal', refId: 'wc_dept_legal_01', name: '法务合规部', platform: 'wecom', sourceType: 'wecom', connectionId: 'ww_wecom_sec_01', memberCount: 2 },
  { id: 'dept-wc-finance', refId: 'wc_dept_fin_02', name: '财务结算中心', platform: 'wecom', sourceType: 'wecom', connectionId: 'ww_wecom_sec_01', memberCount: 1 },
  { id: 'dept-dd-ops', refId: 'dd_dept_ops_01', name: '运维保障中心', platform: 'dingtalk', sourceType: 'dingtalk', connectionId: 'conn-emp-01-dingtalk', memberCount: 2 },
  { id: 'dept-dd-biz', refId: 'dd_dept_biz_02', name: '商业化运营组', platform: 'dingtalk', sourceType: 'dingtalk', connectionId: 'conn-emp-01-dingtalk', memberCount: 2 }
];

// 通讯录 Mock (明确携带 sourceType + connectionId + refId 稳定引用)
export const mockDirectoryUsers: ChannelDirectoryUser[] = [
  // 1. Web 网页 / 平台工作区成员
  { id: 'dir-web-1', refId: 'web_usr_youzai', name: 'youzai (工作区所有者)', department: '管理组 · 平台Owner', email: 'youzai@kiko.corp', platform: 'web', sourceType: 'web', connectionId: 'ws-web-directory', avatar: 'YZ', botAuthorized: true },
  { id: 'dir-web-2', refId: 'web_usr_wangmin', name: '王敏 (平台技术支持)', department: '服务运维部', email: 'min.wang@kiko.corp', platform: 'web', sourceType: 'web', connectionId: 'ws-web-directory', avatar: 'WM', botAuthorized: true },

  // 2. 飞书组织快照人员 (conn-emp-01-feishu)
  { id: 'dir-fs-1', refId: 'fs_ou_alex_01', name: '李工 (alex_li)', department: '前端研发中心', email: 'alex@kiko.corp', platform: 'feishu', sourceType: 'feishu', connectionId: 'conn-emp-01-feishu', avatar: 'LX', botAuthorized: true },
  { id: 'dir-fs-2', refId: 'fs_ou_wei_02', name: '张伟 (系统架构师)', department: '基础架构部', email: 'wei.zhang@kiko.corp', platform: 'feishu', sourceType: 'feishu', connectionId: 'conn-emp-01-feishu', avatar: 'DEV', botAuthorized: true },
  { id: 'dir-fs-3', refId: 'fs_ou_zhoukai_03', name: '周凯 (高级前端)', department: '前端研发中心', email: 'kai.zhou@kiko.corp', platform: 'feishu', sourceType: 'feishu', connectionId: 'conn-emp-01-feishu', avatar: 'ZK', botAuthorized: true },
  { id: 'dir-fs-4', refId: 'fs_ou_lin_04', name: '林汐 (质量效能)', department: '前端研发中心', email: 'xi.lin@kiko.corp', platform: 'feishu', sourceType: 'feishu', connectionId: 'conn-emp-01-feishu', avatar: 'LX2', botAuthorized: true },
  { id: 'dir-fs-5', refId: 'fs_ou_he_05', name: '何远 (中间件SRE)', department: '基础架构部', email: 'yuan.he@kiko.corp', platform: 'feishu', sourceType: 'feishu', connectionId: 'conn-emp-01-feishu', avatar: 'HY', botAuthorized: true },

  // 3. 企业微信连接实时搜索成员 (ww_wecom_sec_01)
  { id: 'dir-wc-1', refId: 'wc_usr_zhao_01', name: '赵法务', department: '法务合规部', email: 'zhao@kiko.corp', platform: 'wecom', sourceType: 'wecom', connectionId: 'ww_wecom_sec_01', avatar: 'ZF', botAuthorized: false, corpUserId: 'wecom_zhao_991', isWecomVerified: false, bindingStatus: 'pending_binding' },
  { id: 'dir-wc-2', refId: 'wc_usr_chen_02', name: '陈财务 (待核验)', department: '财务结算中心', email: 'chen@kiko.corp', platform: 'wecom', sourceType: 'wecom', connectionId: 'ww_wecom_sec_01', avatar: 'CC', botAuthorized: false, corpUserId: 'wecom_chen_882', isWecomVerified: false, bindingStatus: 'pending_binding' },
  { id: 'dir-wc-3', refId: 'wc_usr_sun_03', name: '孙风控', department: '法务合规部', email: 'sun@kiko.corp', platform: 'wecom', sourceType: 'wecom', connectionId: 'ww_wecom_sec_01', avatar: 'SF', botAuthorized: false, corpUserId: 'wecom_sun_773', isWecomVerified: false, bindingStatus: 'pending_binding' },

  // 4. 钉钉连接快照成员 (conn-emp-01-dingtalk)
  { id: 'dir-dd-1', refId: 'dd_usr_zhang_01', name: '张工 (SRE值班)', department: '运维保障中心', email: 'zhang@kiko.corp', platform: 'dingtalk', sourceType: 'dingtalk', connectionId: 'conn-emp-01-dingtalk', avatar: 'ZG', botAuthorized: true },
  { id: 'dir-dd-2', refId: 'dd_usr_zhou_02', name: '周运营', department: '商业化运营组', email: 'zhou@kiko.corp', platform: 'dingtalk', sourceType: 'dingtalk', connectionId: 'conn-emp-01-dingtalk', avatar: 'ZY', botAuthorized: true },
  { id: 'dir-dd-3', refId: 'dd_usr_ma_03', name: '马工 (数据库DBA)', department: '运维保障中心', email: 'ma@kiko.corp', platform: 'dingtalk', sourceType: 'dingtalk', connectionId: 'conn-emp-01-dingtalk', avatar: 'MG', botAuthorized: true },
  { id: 'dir-dd-4', refId: 'dd_usr_liu_04', name: '刘商业 (增长)', department: '商业化运营组', email: 'liu@kiko.corp', platform: 'dingtalk', sourceType: 'dingtalk', connectionId: 'conn-emp-01-dingtalk', avatar: 'LS', botAuthorized: true }
];

export const mockWorkspaces = [
  { id: 'ws-main', name: "youzai's workspace", role: '所有者 (Owner)', desc: '企业私有部署空间 · 生产环境' },
  { id: 'ws-test', name: "研发架构预发空间", role: '管理员', desc: '用于测试新员工与复杂工作流' }
];

export const mockMonitoringData = {
  healthRate: 98.6,
  totalEmployees: 3,
  runningEmployees: 2,
  warningEmployees: 0,
  errorEmployees: 0,
  totalChannels: 128,
  channels: [
    {
      name: '飞书 (Lark) 渠道',
      status: 'online',
      latency: '42ms',
      lastHeartbeat: '刚刚',
      groupsActive: 86
    },
    {
      name: '钉钉 (DingTalk) 渠道',
      status: 'online',
      latency: '38ms',
      lastHeartbeat: '刚刚',
      groupsActive: 34
    },
    {
      name: '企业微信 (WeCom) 智能机器人',
      status: 'online',
      latency: '55ms',
      lastHeartbeat: '1秒前',
      groupsActive: 8
    }
  ],
  runtimeHost: {
    name: '工作区托管执行环境',
    status: 'running',
    cpuUsage: '18.4%',
    memoryUsage: '2.4 GB / 8.0 GB',
    activeSlots: '2 / 8',
    storageUsed: '1.6 GB'
  },
  cronJobs: [
    { name: '每日早9点 master 分支健康巡检', schedule: '0 9 * * 1-5', targetEmployee: '研发助手 AI', nextRun: '明天 09:00', lastStatus: '成功' },
    { name: '数据库容量与慢查询例行巡检', schedule: '0 8 * * *', targetEmployee: '研发助手 AI', nextRun: '明天 08:00', lastStatus: '成功' }
  ]
};


export const initialModels: ModelItem[] = [
  {
    id: 'deepseek-v3',
    name: 'DeepSeek-V3 (推荐工作区基座)',
    modelCode: 'deepseek-chat-v3',
    protocol: 'chat_completions',
    endpoint: 'https://api.deepseek.com/v1',
    apiKeyMasked: 'mock-••••-key',
    boundEmployeesCount: 18,
    supportsThinking: true,
    enabled: true,
    latency: '185ms',
    isDefault: true,
    connectionTestReceipt: 'receipt_live_deepseek_98a'
  },
  {
    id: 'qwen-max',
    name: '通义千问 Qwen-Max',
    modelCode: 'qwen-max-latest',
    protocol: 'chat_completions',
    endpoint: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
    apiKeyMasked: 'sk-aliyun••••••••bc11',
    boundEmployeesCount: 4,
    supportsThinking: false,
    enabled: true,
    latency: '240ms',
    isDefault: false,
    connectionTestReceipt: 'receipt_live_qwen_77c'
  },
  {
    id: 'claude-35-sonnet',
    name: 'Claude 3.5 Sonnet (编程增强)',
    modelCode: 'claude-3-5-sonnet-20241022',
    protocol: 'anthropic_messages',
    endpoint: 'https://gateway.kiko.corp/anthropic/v1',
    apiKeyMasked: 'sk-ant••••••••8812',
    boundEmployeesCount: 2,
    supportsThinking: true,
    enabled: true,
    latency: '410ms',
    isDefault: false,
    connectionTestReceipt: 'receipt_live_claude_12f'
  },
  {
    id: 'vllm-local-qwen',
    name: '内网私有部署 vLLM (Qwen2.5-Coder)',
    modelCode: 'qwen2.5-coder-32b-instruct',
    protocol: 'openai_responses',
    endpoint: 'http://192.168.10.60:8000/v1',
    apiKeyMasked: 'EMPTY (内网白名单免密)',
    boundEmployeesCount: 6,
    supportsThinking: false,
    enabled: true,
    latency: '48ms',
    isDefault: false,
    connectionTestReceipt: 'receipt_live_vllm_44x'
  }
];

export const initialMembers: WorkspaceMember[] = [
  { id: 'm-1', name: 'youzai', email: 'youzai@kiko.corp', role: 'owner', status: 'active', joinedAt: '2026-01-10' },
  { id: 'm-2', name: '李工 (alex_li)', email: 'alex@kiko.corp', role: 'admin', status: 'active', joinedAt: '2026-02-15' },
  { id: 'm-3', name: '王敏 (客服主管)', email: 'min.wang@kiko.corp', role: 'member', status: 'active', joinedAt: '2026-03-01' },
  { id: 'm-4', name: '张工 (SRE值班)', email: 'zhang@kiko.corp', role: 'admin', status: 'active', joinedAt: '2026-03-20' },
  { id: 'm-5', name: '实习生 (已离职)', email: 'intern@kiko.corp', role: 'member', status: 'disabled', joinedAt: '2026-04-01' }
];

export const initialActivityEvents: ActivityEvent[] = [
  { id: '1', time: '11:20', category: 'alerts', content: '客户支持助手在「飞书客服群」遇到非标 SLA 申请，已发起人工决策请求', tone: 'warning', badge: '待决策' },
  { id: '2', time: '11:08', category: 'tasks', content: '研发助手触发生产环境发布脚本，高风险拦截机制生效，已挂起等待管理员确认', tone: 'error', badge: '高风险拦截' },
  { id: '3', time: '10:45', category: 'policy_changes', content: '「飞书 #前端研发核心群」群策略更新：保留最近 20 条群聊消息上下文已生效', tone: 'info', badge: '策略更新' },
  { id: '4', time: '08:08', category: 'tasks', content: '事项 #WI-1029 数据库每日慢查询例行巡检已完成并闭环，成果卡片已发回钉钉群', tone: 'success', badge: '事项闭环' }
];

export const feishuRegexTemplates: FeishuRegexTemplateItem[] = [
  { label: 'PR/MR 提单号', pattern: '/(?:review|pr|mr)\\s*#?\\d+/i', name: 'PR/MR合并请求匹配', description: '匹配 PR #214 等提单' },
  { label: 'Bug/Issue 工单号', pattern: '/(?:bug|issue|缺陷)\\s*#?\\d+/i', name: 'Bug/Issue工单匹配', description: '匹配 Bug #104 工单' },
  { label: '堆栈异常日志', pattern: '/(?:error|exception|stacktrace):\\s*\\S+/i', name: '堆栈异常关键词捕获', description: '提取错误异常日志' },
  { label: 'URL 链接提取', pattern: '/https?:\\/\\/\\S+/i', name: '群内网页链接捕获', description: '提取群内网页链接' }
];

export interface WorkspaceState {
  info: WorkspaceInfo;
  settings: WorkspaceSettings;
  members: WorkspaceMember[];
  models: ModelItem[];
  skills: WorkspaceSkill[];
  mcpServers: McpServerEndpoint[];
  capabilityPackagesCatalog: CapabilityPackageBinding[];
  directorySyncStatus: DirectorySyncStatus;
  channelSyncSources: ChannelSyncSourceDetail[];
  directoryDepartments: ChannelDirectoryDepartment[];
  directoryUsers: ChannelDirectoryUser[];
  emailInvitations: WorkspaceEmailInvitation[];
  employees: DigitalEmployee[];
  employeeSessions: Record<string, EmployeeConversationSession>;
  workItems: WorkItem[];
  tasks: TaskRecord[];
  humanApprovals: HumanApproval[];
  activityEvents: ActivityEvent[];
  runtimeMonitor: RuntimeMonitorData;
  wecomVerifySessions: Record<string, WecomVerifySession>;
  qrRegistrationSessions: Record<string, ChannelQrRegistrationSession>;
  modelSecrets: Record<string, string>;
  mcpSecrets: Record<string, string>;
  channelSecrets: Record<string, string>;
  wecomScopedBindings: Record<string, boolean>;
  directorySyncJobs: Record<string, DirectorySyncJob>;
  chatExecutions: Record<string, ChatExecutionStatus>;
  directorySnapshots: Record<string, {
    snapshotVersion: string;
    syncedAt: string;
    users: ChannelDirectoryUser[];
    departments: ChannelDirectoryDepartment[];
  }>;
  directorySnapshotVersions: Record<string, Record<string, {
    snapshotVersion: string;
    syncedAt: string;
    users: ChannelDirectoryUser[];
    departments: ChannelDirectoryDepartment[];
  }>>;
}

export interface MockAuthAccount {
  id: string;
  name: string;
  email: string;
  password?: string | undefined;
  role: string;
  avatarText: string;
  authProvider: AuthProviderType;
  emailVerified: boolean;
  googleSub?: string | undefined;
  workspaceIds: string[];
}

export interface MockServerDatabase {
  currentUser: {
    id: string;
    name: string;
    email: string;
    role: string;
    avatarText: string;
    authProvider?: AuthProviderType | undefined;
    emailVerified?: boolean | undefined;
  } | null;
  authUsers: MockAuthAccount[];
  workspaces: Record<string, WorkspaceState>;
}

function deepClone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

function normalizeInitialSessions(): Record<string, EmployeeConversationSession> {
  const result: Record<string, EmployeeConversationSession> = {};
  for (const [empId, list] of Object.entries(mockEmployeeSessions)) {
    const first = list[0];
    if (first) {
      result[empId] = deepClone(first);
    }
  }
  return result;
}

function attachPrincipalToUser(u: ChannelDirectoryUser, connOverride?: string): ChannelDirectoryUser {
  const connectionId = connOverride || u.connectionId || (u.platform === 'feishu' ? 'conn-emp-01-feishu' : u.platform === 'dingtalk' ? 'conn-emp-01-dingtalk' : u.platform === 'wecom' ? 'ww_wecom_sec_01' : 'ws-web-directory');
  const targetId = u.refId || u.id;
  const principal: PrincipalRef = {
    targetType: 'user',
    channel: u.platform,
    connectionId,
    targetId,
    displayName: u.name
  };
  principal.canonicalKey = buildCanonicalPrincipalKey(principal);
  return { ...u, connectionId, sourceType: u.platform, principal };
}

function attachPrincipalToDept(d: ChannelDirectoryDepartment, connOverride?: string): ChannelDirectoryDepartment {
  const connectionId = connOverride || d.connectionId || (d.platform === 'feishu' ? 'conn-emp-01-feishu' : d.platform === 'dingtalk' ? 'conn-emp-01-dingtalk' : d.platform === 'wecom' ? 'ww_wecom_sec_01' : 'ws-web-directory');
  const targetId = d.refId || d.id;
  const principal: PrincipalRef = {
    targetType: 'department',
    channel: d.platform,
    connectionId,
    targetId,
    displayName: d.name
  };
  principal.canonicalKey = buildCanonicalPrincipalKey(principal);
  return { ...d, connectionId, sourceType: d.platform, principal };
}

export function buildMainWorkspaceState(info: WorkspaceInfo): WorkspaceState {
  const seededUsers = deepClone(mockDirectoryUsers).map(u => attachPrincipalToUser(u));
  const seededDepts = deepClone(mockDirectoryDepartments).map(d => attachPrincipalToDept(d));
  const initialSnapshots: WorkspaceState['directorySnapshots'] = {
    'conn-emp-01-feishu': {
      snapshotVersion: 'snap-fs-20261111-0941',
      syncedAt: '2026-11-11 11:15:02',
      users: seededUsers.filter(u => u.platform === 'feishu'),
      departments: seededDepts.filter(d => d.platform === 'feishu')
    },
    'conn-emp-02-feishu': {
      snapshotVersion: 'snap-fs-20261111-0941',
      syncedAt: '2026-11-11 11:15:02',
      users: seededUsers.filter(u => u.platform === 'feishu').map(u => attachPrincipalToUser(u, 'conn-emp-02-feishu')),
      departments: seededDepts.filter(d => d.platform === 'feishu').map(d => attachPrincipalToDept(d, 'conn-emp-02-feishu'))
    },
    'conn-emp-01-dingtalk': {
      snapshotVersion: 'snap-ding-34m',
      syncedAt: '2026-11-11 11:15:02',
      users: seededUsers.filter(u => u.platform === 'dingtalk'),
      departments: seededDepts.filter(d => d.platform === 'dingtalk')
    }
  };
  const initialSnapshotVersions: WorkspaceState['directorySnapshotVersions'] = {};
  for (const [connId, snap] of Object.entries(initialSnapshots)) {
    initialSnapshotVersions[connId] = {
      [snap.snapshotVersion]: deepClone(snap)
    };
  }
  return {
    info: deepClone(info),
    settings: {
      name: info.name,
      desc: info.desc,
      autoTrackingWorkItems: true,
      defaultApprovalTimeoutHours: '24',
      notificationGroup: '飞书「#线上发布与值班群」'
    },
    members: deepClone(initialMembers),
    models: deepClone(initialModels),
    skills: deepClone(initialWorkspaceSkillsCatalog),
    mcpServers: deepClone(initialWorkspaceMcpCatalog),
    capabilityPackagesCatalog: deepClone(workspaceCapabilityPackagesCatalog),
    directorySyncStatus: deepClone(initialDirectorySyncStatus),
    channelSyncSources: deepClone(initialChannelSyncSources),
    directoryDepartments: seededDepts,
    directoryUsers: seededUsers,
    emailInvitations: deepClone(initialEmailInvitations),
    employees: deepClone(mockEmployees),
    employeeSessions: normalizeInitialSessions(),
    workItems: deepClone(mockWorkItems).map(w => ({ ...w, version: w.version ?? 1 })),
    tasks: deepClone(mockTasks).map(t => ({ ...t, version: t.version ?? 1 })),
    humanApprovals: deepClone(mockHumanApprovals).map(a => ({ ...a, version: a.version ?? 1 })),
    activityEvents: deepClone(initialActivityEvents),
    runtimeMonitor: {
      healthRate: mockMonitoringData.healthRate,
      totalEmployees: mockMonitoringData.totalEmployees,
      runningEmployees: mockMonitoringData.runningEmployees,
      warningEmployees: mockMonitoringData.warningEmployees,
      errorEmployees: mockMonitoringData.errorEmployees,
      totalChannels: mockMonitoringData.totalChannels,
      channels: deepClone(mockMonitoringData.channels).map(c => ({
        ...c,
        status: c.status as 'online' | 'degraded' | 'offline'
      })),
      runtimeHost: deepClone(mockMonitoringData.runtimeHost),
      cronJobs: deepClone(mockMonitoringData.cronJobs).map((j, idx) => ({
        id: `cron-${idx + 1}`,
        ...j
      }))
    },
    wecomVerifySessions: {},
    qrRegistrationSessions: {},
    modelSecrets: {
      'deepseek-v3': 'mock-deepseek-api-key',
      'qwen-max': 'sk-aliyun-live-secret-bc11',
      'claude-35-sonnet': 'sk-ant-live-secret-8812',
      'vllm-local-qwen': 'EMPTY'
    },
    mcpSecrets: {
      'mcp-gitlab': 'glpat-live-secret-112a',
      'mcp-jenkins': 'jnk-live-secret-908f'
    },
    channelSecrets: {
      'conn-emp-01-feishu': 'sec-feishu-emp01-8a9f',
      'ww_wecom_sec_01': 'bsec-wecom-emp01-812e',
      'conn-emp-01-dingtalk': 'sec-dingtalk-emp01-663a',
      'conn-emp-02-feishu': 'sec-feishu-emp02-412c'
    },
    wecomScopedBindings: {},
    directorySyncJobs: {},
    chatExecutions: {},
    directorySnapshots: initialSnapshots,
    directorySnapshotVersions: initialSnapshotVersions
  };
}

function buildTestWorkspaceState(info: WorkspaceInfo): WorkspaceState {
  const base = buildMainWorkspaceState(info);
  base.employees = base.employees.slice(0, 2).map((emp, idx) => ({
    ...emp,
    name: idx === 0 ? `${emp.name} (预发验证版)` : emp.name,
    stats: {
      todayTasks: Math.max(1, Math.floor((emp.stats.todayTasks ?? 0) / 3)),
      todayConversations: Math.max(1, Math.floor((emp.stats.todayConversations ?? 0) / 2)),
      successRate: '100%'
    }
  }));
  base.humanApprovals = base.humanApprovals.slice(0, 1);
  base.workItems = base.workItems.slice(0, 2);
  base.tasks = base.tasks.slice(0, 3);
  base.activityEvents = [
    {
      id: 'ws-test-1',
      time: '11:30',
      category: 'policy_changes',
      content: '预发空间已完成 DeepSeek-V3 模型连通性校验',
      tone: 'info',
      badge: '预发校验'
    },
    ...base.activityEvents.slice(0, 1)
  ];
  return base;
}

export function createInitialMockDatabase(): MockServerDatabase {
  const wsMainInfo: WorkspaceInfo = mockWorkspaces[0] ?? {
    id: 'ws-main',
    name: "youzai's workspace",
    role: '所有者 (Owner)',
    desc: '企业私有部署空间 · 生产环境'
  };
  const wsTestInfo: WorkspaceInfo = mockWorkspaces[1] ?? {
    id: 'ws-test',
    name: '研发架构预发空间',
    role: '管理员',
    desc: '用于测试新员工与复杂工作流'
  };
  return {
    currentUser: null,
    authUsers: [],
    workspaces: {
      [wsMainInfo.id]: buildMainWorkspaceState(wsMainInfo),
      [wsTestInfo.id]: buildTestWorkspaceState(wsTestInfo)
    }
  };
}
