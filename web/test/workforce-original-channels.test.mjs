import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {Window} from 'happy-dom';
import {parse,compileScript} from '@vue/compiler-sfc';
import esbuild from 'esbuild';
const window=new Window({url:'http://127.0.0.1:5173/'});
for(const name of ['window','document','navigator','HTMLElement','Element','SVGElement','Node','Text','Comment','Image','InputEvent','MouseEvent','KeyboardEvent','HTMLInputElement','Document','MutationObserver','ResizeObserver','getComputedStyle','requestAnimationFrame','cancelAnimationFrame']){
 if(name==='window')globalThis.window=window;else if(window[name]!==undefined)Object.defineProperty(globalThis,name,{configurable:true,writable:true,value:typeof window[name]==='function'&&['getComputedStyle','requestAnimationFrame','cancelAnimationFrame'].includes(name)?window[name].bind(window):window[name]});
}
window.open=()=>null; // Never open an external provider window during component tests.
globalThis.fetch=async()=>{throw new Error('Unexpected HTTP: use injected Workforce ports')};
const built=await esbuild.build({stdin:{contents:"export {default as Channels} from './src/direction2/components/workforce/WorkforceChannelsTab.vue'; export {workforcePortsKey} from './src/direction2/api/workforce-ports'; export {activeWorkspaceId} from './src/direction2/api/client'; export {createApp,h,nextTick,reactive} from 'vue'; export {NMessageProvider} from 'naive-ui';",resolveDir:process.cwd(),loader:'ts'},bundle:true,write:false,format:'esm',platform:'node',define:{'import.meta.env.VITE_PLATFORM_REAL':'true','import.meta.env.DEV':'false'},plugins:[{name:'vue-test',setup(build){build.onLoad({filter:/\.vue$/},async({path})=>{const {descriptor}=parse(await fs.readFile(path,'utf8'));return{contents:compileScript(descriptor,{id:path,inlineTemplate:true}).content,loader:'ts'};});}}]});
const {Channels,workforcePortsKey,activeWorkspaceId,createApp,h,nextTick,reactive,NMessageProvider}=await import(`data:text/javascript;base64,${Buffer.from(built.outputFiles[0].text).toString('base64')}`);
const pause=async()=>{await new Promise(r=>setTimeout(r,35));await nextTick()};
const button=text=>[...document.querySelectorAll('button')].find(b=>b.textContent.trim()===text);
const manualField=(index,value)=>{const n=document.querySelectorAll('.ofp-form input')[index];assert.ok(n,`manual field ${index}`);n.value=value;n.dispatchEvent(new window.Event('input',{bubbles:true}));return n};
const session=(platform,extra={})=>({sessionId:`${platform}:registration-1`,workspaceId:'w',employeeId:'e',platform,verificationUrl:platform==='feishu'?'https://accounts.feishu.cn/confirm?challenge=fixture':'https://open-dev.dingtalk.com/qr?challenge=fixture',qrPayload:'',qrSvgDataUrl:'',expiresInSeconds:600,status:'waiting_scan',qrStatus:'waiting_scan',credentialSaved:false,permissionReady:false,permissionStatus:'unknown',connectionState:'pending',...extra});
const binding=(platform)=>({platform,platformName:{feishu:'飞书',dingtalk:'钉钉',wecom:'企业微信'}[platform],connectionId:'b',status:'pending_config',statusText:'等待长连接认证',credentialStatus:'saved',connectionState:'disconnected',permissionReady:false,appId:'cli_original',appKey:'ding_original',botId:'bot_original',usageScope:{mode:'all',whitelistMembers:[],whitelistDepartments:[],blacklistMembers:[],allowedGroupIds:[]},capabilities:{supportsOrdinaryGroup:true,supportsKeywords:platform==='feishu',supportsRecentContext:platform!=='dingtalk',ordinaryGroupEnabled:true,keywordTriggersEnabled:false,keywords:[],feishuRules:[],recentContextEnabled:false,recentContextWindow:20},sharingRules:[],boundGroups:[]});
async function mount({platform='feishu',channels=[],latest,overrides={},groupPoliciesApi}={}){
 const calls=[],snapshots=[];const state=reactive({platform,employee:{id:'e',name:'测试员工小可',version:7,status:'active',channels,humanConfirmationRules:[]}});
 const api={getLatestChannelQrRegistration:async(_id,p)=>({session:latest===null?null:latest??session(p)}),startChannelQrRegistration:async(_id,p)=>session(p),pollChannelQrRegistration:async()=>session(state.platform),confirmChannelQrRegistration:async()=>session(state.platform,{credentialSaved:true,status:'scanned_confirming'}),cancelChannelQrRegistration:async()=>({}),getEmployee:async()=>state.employee,testChannelConnection:async()=>({channelTestReceipt:'signed-receipt',credentialStatus:'verified',latency:'12ms'}),bindChannelCredentials:async(_id,p)=>({employee:{...state.employee,channels:[binding(p)]},channel:binding(p)}),rebindChannelCredentials:async(_id,p)=>({employee:{...state.employee,channels:[binding(p)]},channel:binding(p),switchingRobot:true}),...overrides};
 const tracked=Object.fromEntries(Object.entries(api).map(([name,fn])=>[name,async(...args)=>{calls.push({name,args:JSON.parse(JSON.stringify(args))});return fn(...args)}]));
 activeWorkspaceId.value='w';const host=document.createElement('div');document.body.appendChild(host);
 const app=createApp({render:()=>h(NMessageProvider,null,{default:()=>h(Channels,{employee:state.employee,savedEmployee:JSON.parse(JSON.stringify(state.employee)),activeChannelPlatform:state.platform,'onUpdate:activeChannelPlatform':p=>{state.platform=p},isActiveTab:true,canManage:true,directoryUsers:[],platformCandidateGroups:{feishu:[],dingtalk:[],wecom:[]},feishuRegexTemplates:[],isDevMode:true,syncModuleField:async()=>true,syncServerFieldIntoSnapshot:(_id,value,fields)=>{snapshots.push({value,fields});state.employee=value},handleOptimisticConflictError:()=>false})})});
 app.provide(workforcePortsKey,{workforceApi:tracked,resourcesApi:{},tasksApi:{},groupPoliciesApi});app.mount(host);await pause();return{app,state,calls,snapshots,async cleanup(){app.unmount();host.remove();await pause()}};
}
function qrValues(v,out=[]){if(!v)return out;if(v.component){if(/QrCode/i.test(v.component.type.name??''))out.push(v.component.props.value);qrValues(v.component.subTree,out)}if(Array.isArray(v.children))for(const c of v.children)if(c&&typeof c==='object')qrValues(c,out);return out}
async function enterManual(){if(button('绑定已有机器人').disabled){button('停止等待').click();await pause()}assert.equal(button('绑定已有机器人').disabled,false);button('绑定已有机器人').click();await pause()}
async function fillManual(p){await enterManual();await pause();manualField(0,p==='wecom'?'bot_test':'cli_test');manualField(1,'secret-value');await pause()}

test('A blocked Feishu registration shows the error and can be replaced with a fresh QR', async () => {
  const old = session('feishu', {status:'scanned_confirming', credentialSaved:true, registrationState:'needs_attention',
    feishuFailure:{reason:'获取飞书应用的租户令牌失败', detail:'21001 Access denied due to platform policy.', automaticRetry:false, canReauthorize:true}});
  const m = await mount({latest:old, overrides:{startChannelQrRegistration:async()=>session('feishu',{sessionId:'new-registration'})}});
  try {
    button('创建智能机器人').click();
    await pause();
    assert.match(document.body.textContent, /21001 Access denied due to platform policy/);
    assert.match(document.body.textContent, /自动检查已停止/);
    assert.doesNotMatch(document.body.textContent, /后台会继续验证/);
    assert.equal(button('停止等待'), undefined);
    assert.ok(button('重新检查'));
    assert.equal(m.calls.length, 1);
    button('重新授权').click();
    await pause();
    assert.deepEqual(m.calls.at(-1), {name:'startChannelQrRegistration',args:['e','feishu','w',old.sessionId]});
    assert.deepEqual(qrValues(m.app._instance.subTree), [session('feishu').verificationUrl]);
    assert.equal(button('重新授权'), undefined);
  } finally { await m.cleanup(); }
});

test('Unbound UI has only two choices and creates an official QR only after explicit click', async () => {
  const m = await mount({latest: null});
  try {
    const create = button('创建智能机器人');
    assert.ok(create);
    assert.ok(button('绑定已有机器人'));
    assert.equal(document.querySelector('input[type="password"]'), null);
    assert.equal(document.querySelector('.qr-code-art svg'), null);
    assert.equal(m.calls.length, 0);
    const card = document.querySelector('.unconnected-channel-card');
    assert.equal(card.querySelectorAll('button').length, 2);
    assert.equal(card.querySelector('[role="checkbox"], input[type="checkbox"]'), null);

    create.click();
    create.click();
    await pause();
    assert.equal(m.calls.filter(call => call.name === 'getLatestChannelQrRegistration').length, 1);
    assert.equal(document.querySelector('.qr-code-art svg')?.getAttribute('width'), '168');
    assert.deepEqual(qrValues(m.app._instance.subTree), [session('feishu').verificationUrl]);
    assert.deepEqual(m.calls.filter(call => call.name === 'startChannelQrRegistration'), [
      {name: 'startChannelQrRegistration', args: ['e', 'feishu', 'w']},
    ]);
    assert.doesNotMatch(document.body.textContent, /模拟移动端管理员/);

    await enterManual();
    assert.ok(document.querySelector('.ofp-form input[type="password"]'));
    assert.ok(button('创建智能机器人'));
  } finally {
    await m.cleanup();
  }
});

test('Long verification links enlarge only the Feishu QR and preserve its full payload', async () => {
  for (const [platform, expectedSize] of [['feishu', '256'], ['dingtalk', '168']]) {
    const verificationUrl = `${session(platform).verificationUrl}&addons=${'aB3_-'.repeat(120)}`;
    const m = await mount({platform, latest: session(platform, {verificationUrl, qrPayload: verificationUrl})});
    try {
      button('创建智能机器人').click();
      await pause();
      const svg = document.querySelector('.qr-code-art svg');
      assert.equal(svg?.getAttribute('width'), expectedSize);
      assert.equal(svg?.getAttribute('height'), expectedSize);
      assert.deepEqual(qrValues(m.app._instance.subTree), [verificationUrl]);
      assert.equal(document.querySelector('.qr-pane-body a')?.href, verificationUrl);
      assert.deepEqual(m.calls.map(call => call.name), ['getLatestChannelQrRegistration']);
    } finally {
      await m.cleanup();
    }
  }
});

test('Group settings appear once after saving a binding for the selected channel', async () => {
  const titles = {feishu: '飞书群配置', dingtalk: '钉钉群配置', wecom: '企微群配置'};
  const groupHeadings = () => [...document.querySelectorAll('h4.cs-title')]
    .map(heading => heading.textContent.trim()).filter(title => title.endsWith('群配置'));

  for (const [platform, title] of Object.entries(titles)) {
    const reads = [];
    const groupPoliciesApi = {
      getUsage: async () => {
        reads.push('usage');
        return {version: 1, usage_config: {trigger: {mention: true}, history: {enabled: false, limit: 20, channels: []}}};
      },
      getRules: async () => {
        reads.push('rules');
        return {version: 1, rules: []};
      },
    };
    const otherPlatform = platform === 'dingtalk' ? 'feishu' : 'dingtalk';
    const m = await mount({platform, channels: [binding(otherPlatform)], groupPoliciesApi});
    try {
      assert.ok(button('创建智能机器人'));
      assert.ok(button('绑定已有机器人'));
      assert.equal(document.querySelector('.channel-lifecycle-bar, .channel-config-box details'), null);
      if (platform !== 'feishu') assert.ok(document.querySelector('.ch-notice'));
      await fillManual(platform);
      assert.deepEqual(groupHeadings(), []);
      assert.deepEqual(reads, []);

      button('验证并保存').click();
      await pause();
      assert.deepEqual(groupHeadings(), [title]);
      assert.equal(document.querySelector('.unconnected-channel-card'), null);
      assert.deepEqual(reads, {dingtalk:[], feishu:['usage', 'rules'], wecom:['usage']}[platform]);
      assert.equal(document.querySelectorAll('[aria-label="启用近期群历史"]').length, platform === 'dingtalk' ? 0 : 1);
      assert.equal(Boolean(button('新增群匹配规则')), platform === 'feishu');
      assert.doesNotMatch(document.body.textContent, /群聊 @ 交互支持|群触发与规则策略|群聊 @ 交互与近期群消息补充/);
      if (platform === 'dingtalk') assert.match(document.body.textContent, /群聊 @ 消息响应.*尚未验证/s);
    } finally {
      await m.cleanup();
    }
  }
});

test('Bound channel sections follow the requested order when optional modules are present', async () => {
  const sections = {
    status: '.channel-lifecycle-bar',
    directory: '.ch-form-sections .field-note',
    notice: '.ch-notice',
    group: 'h4.cs-title',
    whitelist: '[aria-label="渠道使用范围提示"]',
    authorization: 'details > summary',
  };
  const order = {
    feishu: ['status', 'directory', 'group', 'whitelist', 'authorization'],
    dingtalk: ['status', 'directory', 'notice', 'group', 'whitelist'],
    wecom: ['status', 'notice', 'group', 'whitelist'],
  };
  const channels = Object.keys(order).map(platform => {
    const channel = binding(platform);
    channel.usageScope.mode = 'whitelist';
    if (platform !== 'wecom') {
      channel.directoryStatus = {status: 'ready', complete: true, user_count: 3, department_count: 2};
    }
    return channel;
  });
  const m = await mount({channels});
  try {
    for (const [platform, expected] of Object.entries(order)) {
      m.state.platform = platform;
      await pause();
      const config = document.querySelector('.channel-config-box');
      const actual = [...config.querySelectorAll(Object.values(sections).join(', '))]
        .map(element => Object.keys(sections).find(section => element.matches(sections[section])));
      assert.deepEqual(actual, expected, platform);
    }
  } finally {
    await m.cleanup();
  }
});

test('Create and manual modes keep the same header and use different official links for each provider', async () => {
  const consoles = {
    feishu: 'https://open.feishu.cn/app',
    dingtalk: 'https://open-dev.dingtalk.com/fe/app#/corp/app',
    wecom: 'https://work.weixin.qq.com/wework_admin/frame#/aiHelper/list?from=manage_tools',
  };
  for (const [platform, consoleUrl] of Object.entries(consoles)) {
    const m = await mount({platform});
    const heading = document.querySelector('.ucc-title-col').textContent;
    try {
      button('创建智能机器人').click();
      await pause();
      if (platform === 'wecom') {
        assert.equal(document.querySelector('.qr-code-art'), null);
        assert.match(document.querySelector('.onboarding-qr-pane').textContent, /企业微信官方窗口扫码/);
      } else {
        const qr = session(platform);
        assert.equal(document.querySelector('.qr-details a').href, qr.verificationUrl);
        assert.deepEqual(qrValues(m.app._instance.subTree), [qr.verificationUrl]);
        assert.equal(document.querySelectorAll('.qr-pane-body a').length, 1);
      }
      assert.equal(document.querySelectorAll('input').length, 0);
      await enterManual();
      assert.equal(document.querySelector('.ucc-title-col').textContent, heading);
      assert.equal(document.querySelector('.ucc-header-row button').textContent.trim(), '创建智能机器人');
      assert.equal(document.querySelectorAll('.ofp-form input').length, 2);
      assert.equal(document.querySelector('.onboarding-form-pane a').href, consoleUrl);
      assert.equal(document.querySelector('.qr-code-art'), null);
    } finally {
      await m.cleanup();
    }
  }
});

test('Creating a robot replaces old expired, cancelled or denied QR registrations', async () => {
  for (const [platform, registrationState] of [['dingtalk', 'expired'], ['feishu', 'expired'], ['feishu', 'cancelled'], ['feishu', 'denied']]) {
    const latest = session(platform, {sessionId: 'old', status: 'expired', registrationState, expiresInSeconds: 0,
      restartNeedsReview: registrationState === 'expired'});
    const m = await mount({platform, latest});
    try {
      button('创建智能机器人').click();
      await pause();
      assert.equal(m.calls.filter(call => call.name === 'startChannelQrRegistration').length, 1);
      assert.equal(button('重新发起扫码'), undefined);
      assert.deepEqual(qrValues(m.app._instance.subTree), [session(platform).verificationUrl]);
    } finally {
      await m.cleanup();
    }
  }
});

test('An open scan panel renews an expired displayed code and shows the new official link', async () => {
  const platform = 'dingtalk';
  const previous = session(platform, {sessionId: 'old', verificationUrl: 'https://open-dev.dingtalk.com/qr?challenge=old'});
  const expired = {...previous, registrationState: 'expired', status: 'expired', expiresInSeconds: 0};
  const next = session(platform);
  let reads = 0;
  let renewed;
  const replacementRequested = new Promise(resolve => { renewed = resolve; });
  const m = await mount({platform, overrides: {
    getLatestChannelQrRegistration: async () => ({session: ++reads === 1 ? previous : expired}),
    pollChannelQrRegistration: async () => expired,
    startChannelQrRegistration: async () => { renewed(); return next; },
  }});
  try {
    button('创建智能机器人').click();
    await pause();
    assert.deepEqual(qrValues(m.app._instance.subTree), [previous.verificationUrl]);
    await Promise.race([replacementRequested, new Promise((_, reject) => {
      const timeout = setTimeout(() => reject(new Error('expired QR was not replaced')), 3500);
      timeout.unref();
    })]);
    await pause();
    assert.deepEqual(qrValues(m.app._instance.subTree), [next.verificationUrl]);
    assert.equal(document.querySelector('.qr-details a').href, next.verificationUrl);
    assert.equal(button('重新发起扫码'), undefined);
  } finally {
    await m.cleanup();
  }
});

test('Interrupted Feishu registration has an explicit restart after checking the previous outcome', async () => {
  const m = await mount({latest: session('feishu', {status: 'expired', registrationState: 'interrupted', expiresInSeconds: 0})});
  try {
    button('创建智能机器人').click();
    await pause();
    assert.equal(m.calls.some(call => call.name === 'startChannelQrRegistration'), false);
    assert.deepEqual(qrValues(m.app._instance.subTree), []);
    button('重新发起扫码').click();
    await pause();
    assert.equal(m.calls.filter(call => call.name === 'startChannelQrRegistration').length, 1);
    assert.deepEqual(qrValues(m.app._instance.subTree), [session('feishu').verificationUrl]);
  } finally {
    await m.cleanup();
  }
});

test('Saved configuration is not a connected or permission-ready claim for any provider',async()=>{for(const platform of ['feishu','wecom','dingtalk']){const m=await mount({platform,channels:[binding(platform)]});try{const tags=[...document.querySelectorAll('.n-tag')].map(n=>n.textContent.trim());assert.ok(tags.includes('等待长连接认证'),platform);assert.equal(tags.includes('权限未就绪'),platform==='feishu');assert.equal(tags.includes('联系人搜索未检测'),platform==='wecom');assert.equal(tags.includes('已连接'),false);assert.equal(tags.includes('权限就绪'),false);assert.equal(m.calls.length,0)}finally{await m.cleanup()}}});

test('WeCom format check and save use one action without claiming authentication',async()=>{
 const m=await mount({platform:'wecom',overrides:{testChannelConnection:async()=>({channelTestReceipt:'',credentialStatus:'format_only',latency:'0ms'})}});
 try {
  await fillManual('wecom');
  assert.equal(button('检查格式'),undefined);
  button('验证并保存').click();await pause();
  assert.deepEqual(m.calls.map(call=>call.name),['testChannelConnection','bindChannelCredentials']);
  assert.equal(m.calls[1].args[2].botId,'bot_test');
  assert.equal(m.calls[1].args[2].channelTestReceipt,'');
  assert.match(document.body.textContent,/等待正式连接验证/);
  assert.doesNotMatch(document.body.textContent,/凭据验证通过/);
 } finally {await m.cleanup()}
});

test('A rejected credential receipt keeps the draft and allows checking the same values again',async()=>{
 const m=await mount({overrides:{bindChannelCredentials:async()=>{throw new Error('验证回执已过期，请重新验证')}}});
 try {
  await fillManual('feishu');button('验证并保存').click();await pause();
  assert.match(document.body.textContent,/回执已过期/);assert.equal(m.snapshots.length,0);
  assert.equal(document.querySelector('.ofp-form input[type="password"]').value,'secret-value');
  assert.equal(button('验证并保存').disabled,false);
 } finally {await m.cleanup()}
});

test('Late verification cannot authorize changed credential drafts',async()=>{
 let finish;const m=await mount({overrides:{testChannelConnection:()=>new Promise(resolve=>{finish=resolve})}});
 try {
  await fillManual('feishu');button('验证并保存').click();await pause();
  manualField(1,'new-secret');await pause();
  finish({channelTestReceipt:'stale-receipt',credentialStatus:'verified',latency:'20ms'});await pause();
  assert.equal(m.calls.some(call=>call.name==='bindChannelCredentials'),false);
  assert.equal(button('验证并保存').disabled,false);
 } finally {await m.cleanup()}
});

test('Switching identities shows current impacts and requires explicit confirmation', async () => {
  let resolvePreview;
  const preview = new Promise(resolve => { resolvePreview = resolve; });
  const m = await mount({channels: [binding('feishu')], overrides: {getChannelRebindImpact: () => preview}});
  try {
    button('更新凭据 / 换绑').click();
    await pause();
    const [app, secret] = document.querySelectorAll('.n-modal input');
    for (const [input, value] of [[app, 'cli_replacement'], [secret, 'new-secret']]) {
      input.value = value;
      input.dispatchEvent(new window.Event('input', {bubbles: true}));
    }
    await pause();
    assert.equal(button('确认换绑新机器人').disabled, true);
    assert.deepEqual(m.calls.map(call => call.name), ['getChannelRebindImpact']);
    resolvePreview({id: 'b', config_revision: 7, rebind_impact: {
      allow_entries: 1, deny_entries: 2, package_names: ['发布'], package_deny_names: ['发布'], sharing_names: ['项目组'], notification_names: []
    }});
    await pause();
    const content = document.querySelector('.n-modal').textContent;
    for (const text of ['1 条允许项、2 条拒绝项', '使用范围可能扩大', '发布', '项目组']) assert.ok(content.includes(text), text);
    assert.equal(button('确认换绑新机器人').disabled, true);
    const checkbox = document.querySelector('.n-modal .n-checkbox');
    checkbox.click();
    await pause();
    assert.equal(button('确认换绑新机器人').disabled, false);
    button('确认换绑新机器人').click();
    await pause();
    const call = m.calls.find(c => c.name === 'rebindChannelCredentials');
    assert.equal(call.args[2].confirmedImpact, true);
    assert.equal(call.args[2].appId, 'cli_replacement');
    assert.equal(call.args[2].expectedVersion, 7);
  } finally {
    await m.cleanup();
  }
});

test('A bound channel with an empty whitelist directs the administrator to usage settings', async () => {
  const current = binding('feishu');
  current.usageScope.mode = 'whitelist';
  const m = await mount({channels: [current]});
  try {
    assert.match(document.body.textContent, /当前渠道还没有允许的人员或部门/);
    assert.ok(button('配置使用范围'));
    m.state.employee.channels[0].usageScope.whitelistDepartments.push('allowed-department');
    await pause();
    assert.equal(button('配置使用范围'), undefined);
  } finally {
    await m.cleanup();
  }
});

test('Changing to WeCom cannot inherit another provider registration status',async()=>{
 const m=await mount({latest:session('feishu',{credentialSaved:true,status:'scanned_confirming'})});try{button('创建智能机器人').click();await pause();assert.match(document.body.textContent,/已取得凭据/);m.state.platform='wecom';await pause();assert.ok(button('创建智能机器人'));assert.equal(document.querySelector('.qr-code-art'),null);button('创建智能机器人').click();await pause();assert.match(document.querySelector('.onboarding-qr-pane').textContent,/企业微信官方窗口扫码/);assert.doesNotMatch(document.body.textContent,/已取得凭据/);assert.equal(button('重新检查'),undefined)}finally{await m.cleanup()}
});

test('Late QR recheck results cannot overwrite a different channel context',async()=>{
 let finish;const m=await mount({overrides:{getLatestChannelQrRegistration:async(_id,p)=>({session:session(p,{credentialSaved:p==='feishu',status:p==='feishu'?'scanned_confirming':'waiting_scan'})}),confirmChannelQrRegistration:()=>new Promise(resolve=>{finish=resolve})}});
 try{button('创建智能机器人').click();await pause();button('重新检查').click();await pause();m.state.platform='dingtalk';await pause();button('创建智能机器人').click();await pause();const expected=session('dingtalk').verificationUrl;assert.deepEqual(qrValues(m.app._instance.subTree),[expected]);finish(session('feishu',{credentialSaved:true,status:'scanned_confirming'}));await pause();assert.deepEqual(qrValues(m.app._instance.subTree),[expected]);assert.equal(button('重新检查'),undefined)}finally{await m.cleanup()}
});

test('Leaving creation discards late lookup and returning does not auto-create or replace old registration',async()=>{
 let finish;let reads=0;const m=await mount({overrides:{getLatestChannelQrRegistration:()=>++reads===1?new Promise(resolve=>{finish=resolve}):Promise.resolve({session:session('feishu',{status:'expired',registrationState:'interrupted'})})}});
 try{assert.equal(m.calls.length,0);button('创建智能机器人').click();await pause();button('绑定已有机器人').click();await pause();finish({session:null});await pause();assert.equal(m.calls.filter(c=>c.name==='startChannelQrRegistration').length,0);assert.ok(button('创建智能机器人'));m.state.platform='dingtalk';await pause();m.state.platform='feishu';await pause();assert.equal(reads,1);button('创建智能机器人').click();await pause();assert.equal(reads,2);assert.equal(m.calls.filter(c=>c.name==='startChannelQrRegistration').length,0);assert.match(document.body.textContent,/未确定|中断|未知|核对/)}finally{await m.cleanup()}
});

test('WeCom popup stays in the click gesture and switching modes discards late credentials', async () => {
  const original = window.open;
  const opened = [];
  const popup = {closed: false, close() { this.closed = true; }};
  window.open = url => { opened.push(url); return popup; };
  let finish;
  const m = await mount({platform: 'wecom', overrides: {
    testChannelConnection: () => new Promise(resolve => { finish = resolve; }),
  }});
  try {
    const create = button('创建智能机器人');
    create.click();
    create.click();
    assert.equal(opened.length, 1);
    assert.equal(new URL(opened[0]).pathname, '/ai/qc/gen');
    await pause();
    assert.equal(document.querySelector('.qr-code-art'), null);
    window.dispatchEvent(new window.MessageEvent('message', {
      origin: 'https://work.weixin.qq.com', source: popup,
      data: {type: 'AUTH_SUCCESS', payload: {botid: 'bot_fixture', secret: 'fixture-secret'}},
    }));
    await pause();
    assert.equal(m.calls.filter(call => call.name === 'testChannelConnection').length, 1);
    button('绑定已有机器人').click();
    await pause();
    finish({credentialStatus: 'format_only', channelTestReceipt: ''});
    await pause();
    assert.equal(m.calls.some(call => call.name === 'bindChannelCredentials'), false);
    assert.equal(m.calls.some(call => /QrRegistration$/.test(call.name)), false);
    assert.equal(document.querySelectorAll('.ofp-form input').length, 2);
  } finally {
    await m.cleanup();
    window.open = original;
  }
});

test('WeCom save recovery first reads the current binding and reuses the original callback',async()=>{
 const original=window.open;let opens=0,attempts=0;
 const popup={closed:false,close(){this.closed=true}};
 window.open=()=>{opens++;return popup};
 const m=await mount({platform:'wecom',overrides:{
  testChannelConnection:async()=>({credentialStatus:'format_only',channelTestReceipt:''}),
  bindChannelCredentials:async()=>{if(++attempts===1)throw new Error('保存结果暂不可用');return {employee:{...m.state.employee,channels:[binding('wecom')]},channel:binding('wecom')}},
 }});
 try{
  button('创建智能机器人').click();await pause();
  window.dispatchEvent(new window.MessageEvent('message',{origin:'https://work.weixin.qq.com',source:popup,
    data:{type:'AUTH_SUCCESS',payload:{botid:'bot_original',secret:'fixture-secret'}}}));
  await pause();assert.ok(button('重试保存'));
  button('重试保存').click();await pause();
  assert.deepEqual(m.calls.map(call=>call.name),['testChannelConnection','bindChannelCredentials','getEmployee','testChannelConnection','bindChannelCredentials']);
  assert.equal(opens,1);assert.equal(m.state.employee.channels[0].platform,'wecom');
 }finally{await m.cleanup();window.open=original}
});

test('Feishu permission readiness remains three-state',async()=>{
 for(const permissionReady of [undefined,false,true]){
  const expected=permissionReady===true?'权限就绪':permissionReady===false?'权限未就绪':'权限未确认';
  const m=await mount({platform:'feishu',channels:[{...binding('feishu'),permissionReady}]});
  try{const tags=[...document.querySelectorAll('.n-tag')].map(n=>n.textContent.trim());assert.ok(tags.includes(expected),String(permissionReady));for(const label of ['权限就绪','权限未就绪','权限未确认'])if(label!==expected)assert.equal(tags.includes(label),false)}finally{await m.cleanup()}
 }
});

test('Only the verified WeCom tab probes permission; old readiness is ignored',async()=>{
 const originalFetch=globalThis.fetch;
 const calls=[];
 globalThis.fetch=async (path, init)=>{
  calls.push({path,method:init.method ?? 'GET',credentials:init.credentials});
  return Response.json({status:'available',binding_id:'b',connection_generation:2,config_revision:5});
 };
 const m=await mount({platform:'wecom',channels:[{...binding('wecom'),connectionGeneration:2,configRevision:5,identityConfirmed:false,permissionReady:true}]});
 const labels=()=>[...document.querySelectorAll('.clb-left .n-tag')].map(n=>n.textContent.trim());
 try {
  assert.ok(labels().includes('联系人搜索未检测'));
  assert.equal(calls.length,0);
  m.state.employee.channels[0].identityConfirmed=true;
  await pause();
  assert.deepEqual(calls,[{path:'/api/v1/workspaces/w/agents/e/channels/wecom/contacts/permission',method:'GET',credentials:'include'}]);
  assert.ok(labels().includes('联系人搜索可用'));
  for(const label of ['权限就绪','权限未就绪','权限未确认']) assert.equal(labels().includes(label),false);
 } finally { await m.cleanup(); globalThis.fetch=originalFetch; }
});

test('Feishu and DingTalk show directory availability from the current snapshot', async () => {
  const snapshot = {status: 'ready', snapshot_version: 1, complete: true, user_count: 3, department_count: 2};
  const cases = [
    [{...snapshot, status: 'syncing', complete: false}, '通讯录不可用'],
    [snapshot, '通讯录可用'],
    [{...snapshot, status: 'syncing'}, '通讯录可用'],
    [{...snapshot, status: 'failed'}, '通讯录可用'],
    [{...snapshot, status: 'invalidated', complete: false}, '通讯录不可用'],
    [undefined, '通讯录未确认'],
  ];
  const labels = () => [...document.querySelectorAll('.clb-left .n-tag')].map(tag => tag.textContent.trim());

  for (const platform of ['feishu', 'dingtalk']) {
    const m = await mount({platform, channels: [{...binding(platform), permissionReady: true}]});
    try {
      const channel = m.state.employee.channels[0];
      for (const [directoryStatus, expected] of cases) {
        channel.directoryStatus = directoryStatus;
        await pause();
        assert.deepEqual(labels().filter(label => label.startsWith('通讯录')), [expected]);
        assert.equal(labels().includes('权限就绪'), platform === 'feishu');
      }
      channel.directoryStatus = snapshot;
      channel.permissionReady = false;
      await pause();
      assert.ok(labels().includes('通讯录可用'));
      assert.equal(labels().includes('权限未就绪'), platform === 'feishu');

      channel.status = 'disabled';
      await pause();
      assert.ok(labels().includes('通讯录不可用'));
    } finally {
      await m.cleanup();
    }
  }
});

test('DingTalk reports directory status before robot identity confirmation',async()=>{
 const channel={...binding('dingtalk'),status:'connected',statusText:'已连接',connectionState:'connected',identityConfirmed:false,
  directoryStatus:{status:'never',complete:false,user_count:0,department_count:0}};
 const m=await mount({platform:'dingtalk',channels:[channel]});
 const permissionLink=()=>[...document.querySelectorAll('a')].find(a=>a.textContent.includes('去钉钉补充通讯录权限'));
 try{
  assert.match(document.querySelector('.connection-status').textContent,/已连接/);
  assert.match(document.body.textContent,/等待自动同步通讯录/);
  assert.doesNotMatch(document.body.textContent,/请向机器人发送一条私聊/);
  m.state.employee.channels[0].directoryStatus={status:'invalidated',complete:false,error_code:'directory_authorization_failed',user_count:0,department_count:0};
  await pause();
  assert.match(document.body.textContent,/通讯录缺少权限或可见范围/);
  assert.ok(permissionLink());
  m.state.employee.channels[0].directoryStatus.error_code='directory_authentication_failed';
  await pause();
  assert.match(document.body.textContent,/通讯录认证失败/);
  assert.equal(permissionLink(),undefined);
  m.state.employee.channels[0].directoryStatus={status:'ready',complete:true,user_count:3,department_count:2};
  await pause();
  assert.match(document.body.textContent,/通讯录已同步 · 3 人 · 2 个部门/);
  assert.match(document.querySelector('.connection-status').textContent,/已连接/);
 }finally{await m.cleanup()}
});

test('Feishu missing check results and unknown resource access are not missing configuration',async()=>{
 const m=await mount({channels:[{...binding('feishu'),permissionReady:undefined}]});
 try{
  const details=document.querySelector('.channel-config-box details');
  const summary=details.querySelector('summary');
  assert.equal(details.open,false);
  assert.match(summary.textContent,/业务能力授权与具体资源访问状态/);
  assert.match(summary.textContent,/尚无权限检查结果/);
  assert.doesNotMatch(summary.textContent,/0 项业务模块已关联/);
  details.open=true;
  m.state.employee.channels[0].businessCapabilities=[
   {id:'unknown',name:'未知资源',platformAuthStatus:'unknown',resourceAccessStatus:'unknown',statusDetail:'尚未检查'},
   {id:'missing',name:'缺失资源',platformAuthStatus:'unauthorized',resourceAccessStatus:'not_configured',statusDetail:'已检查未配置'}
  ];await pause();
  const rows=[...details.querySelectorAll('.cap-matrix-table tbody tr')];
  assert.match(rows[0].textContent,/未确认/);assert.doesNotMatch(rows[0].textContent,/未配置/);
  assert.match(rows[1].textContent,/未配置/);assert.match(rows[1].textContent,/未授权/);
  assert.match(summary.textContent,/2 项业务模块检查结果/);
  assert.doesNotMatch(details.textContent,/尚无权限检查结果/);
 }finally{await m.cleanup()}
});

test('DingTalk Stream connected does not assert group message delivery verified',async()=>{
 const m=await mount({platform:'dingtalk',channels:[{...binding('dingtalk'),connectionState:'connected',permissionReady:true}]});
 try{assert.match(document.body.textContent,/已连接/);assert.match(document.body.textContent,/尚未验证/);assert.match(document.body.textContent,/Stream 已连接不代表实际消息收发成功/);assert.doesNotMatch(document.body.textContent,/已连通|即时响应回复/)}finally{await m.cleanup()}
});

test('WeCom and DingTalk capability notices retain the same warning style before and after binding',async()=>{
 for(const platform of ['wecom','dingtalk'])for(const channels of [[],[binding(platform)]]){
  const m=await mount({platform,channels});try{const notice=document.querySelector('.ch-notice.dingtalk-notice');assert.ok(notice);assert.match(notice.textContent,/平台能力限制/);assert.equal(document.querySelector('.wecom-notice'),null)}finally{await m.cleanup()}
 }
});
